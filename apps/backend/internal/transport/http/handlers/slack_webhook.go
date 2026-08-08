package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/slack"
)

// SlackWebhookHandler is the public ingress for Slack Events API callbacks.
// Tenants point their Slack app's "Event Subscriptions → Request URL" at
// /webhook/slack/events/:app_id so the handler can resolve the company — and
// therefore the signing secret — before trusting anything in the body.
//
// Inbound flow: verify the v0 signature → drop redeliveries → dispatch:
//   - url_verification → echo challenge.
//   - event_callback   → require app_mention (channels) or a DM message, run
//     the allowlist check, enqueue chat:run. Silent 200 on reject, because a
//     non-2xx is a retry and the answer will not change.
type SlackWebhookHandler struct {
	slackSvc *app.SlackService
	chatEnq  *app.ChatEnqueuer
	dedupe   slack.Deduper
	replier  slack.Provider
}

func NewSlackWebhookHandler(slackSvc *app.SlackService, chatEnq *app.ChatEnqueuer) *SlackWebhookHandler {
	return &SlackWebhookHandler{slackSvc: slackSvc, chatEnq: chatEnq}
}

// WithDeduper makes redelivery detection durable across API replicas. Optional
// only in the sense that the handler starts without it — a deployment that
// skips it falls back to the retry header, which catches the ordinary case and
// not the failover one.
func (h *SlackWebhookHandler) WithDeduper(d slack.Deduper) *SlackWebhookHandler {
	h.dedupe = d
	return h
}

// WithReplier gives the API process an outbound Slack client, for the reason
// the Lark handler states: a credit refusal happens before there is anything
// to enqueue, so the worker never sees it and this is the only process that
// can speak it.
func (h *SlackWebhookHandler) WithReplier(p slack.Provider) *SlackWebhookHandler {
	h.replier = p
	return h
}

func (h *SlackWebhookHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/slack/events/:app_id", h.events)
}

func (h *SlackWebhookHandler) events(c *gin.Context) {
	appID := c.Param("app_id")
	if appID == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	cred, err := h.slackSvc.ResolveCompanyByAppID(c.Request.Context(), appID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusInternalServerError)
		return
	}
	if !cred.Enabled {
		c.Status(http.StatusServiceUnavailable)
		return
	}

	body, err := c.GetRawData()
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	// Slack signs every request, including the setup challenge. Verify before
	// the body is interpreted at all: an unverified webhook lets anyone on the
	// internet ask questions as any user of any tenant.
	if err := slack.VerifySignature(
		cred.SigningSecret,
		c.GetHeader("X-Slack-Request-Timestamp"),
		c.GetHeader("X-Slack-Signature"),
		body,
		time.Now(),
	); err != nil {
		logrus.WithError(err).WithField("app_id", appID).Warn("slack webhook: signature verify failed")
		c.Status(http.StatusUnauthorized)
		return
	}

	env, err := slack.ParseEnvelope(body)
	if err != nil {
		logrus.WithError(err).WithField("app_id", appID).Warn("slack webhook: parse envelope")
		c.Status(http.StatusBadRequest)
		return
	}

	if env.Type == slack.TypeURLVerification {
		c.JSON(http.StatusOK, gin.H{"challenge": env.Challenge})
		return
	}
	if env.Type != slack.TypeEventCallback {
		// app_rate_limited and anything Slack adds later — ack so it stops.
		c.Status(http.StatusOK)
		return
	}

	if h.isRedelivery(c, appID, env) {
		c.Status(http.StatusOK)
		return
	}

	ev, err := slack.ParseEvent(env.Event)
	if err != nil {
		if errors.Is(err, slack.ErrNotMessageEvent) {
			c.Status(http.StatusOK)
			return
		}
		logrus.WithError(err).WithField("app_id", appID).Warn("slack webhook: parse event")
		c.Status(http.StatusBadRequest)
		return
	}

	// Prefer the configured bot user id; fall back to the one Slack sends in
	// `authorizations` and persist it, so an admin never has to hunt for the
	// id the way Lark's bot_open_id requires.
	botUserID := cred.BotUserID
	if discovered := env.BotUserID(); discovered != "" {
		botUserID = discovered
		if cred.BotUserID != discovered {
			if err := h.slackSvc.LearnBotUserID(c.Request.Context(), cred.CompanyID, discovered); err != nil {
				logrus.WithError(err).WithField("company_id", cred.CompanyID).
					Warn("slack webhook: persist bot_user_id")
			}
		}
	}

	if !ev.Actionable(botUserID) {
		c.Status(http.StatusOK)
		return
	}

	text := slack.StripMentions(ev.Text)
	if text == "" {
		c.Status(http.StatusOK)
		return
	}

	allowed, err := h.slackSvc.IsUserAllowed(c.Request.Context(), cred.CompanyID, ev.User)
	if err != nil {
		logrus.WithError(err).Warn("slack webhook: allowlist lookup")
		c.Status(http.StatusInternalServerError)
		return
	}
	if !allowed {
		// Silent drop, matching Lark and Discord.
		c.Status(http.StatusOK)
		return
	}

	teamID := env.TeamID
	if teamID == "" {
		teamID = ev.Team
	}

	if _, err := h.chatEnq.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:        domain.ChannelSlack,
		CompanyID:      cred.CompanyID,
		SlackTeamID:    teamID,
		SlackChannelID: ev.Channel,
		SlackUserID:    ev.User,
		SlackMessageTS: ev.TS,
		SlackThreadTS:  ev.ThreadTS,
		Message:        text,
	}); err != nil {
		if errors.Is(err, domain.ErrInsufficientCredits) {
			if h.replier != nil {
				// Into the thread the question was asked in, so the refusal
				// appears where the asker is looking.
				if rerr := h.replier.Reply(c.Request.Context(), cred.CompanyID,
					ev.Channel, ev.ThreadKey(), app.CreditsExhaustedMessage); rerr != nil {
					logrus.WithError(rerr).WithField("company_id", cred.CompanyID).
						Warn("slack webhook: could not deliver the credit refusal")
				}
			}
			// 200 either way — a non-2xx is a Slack retry, and the tenant's
			// balance will not have changed by the time it arrives.
			c.Status(http.StatusOK)
			return
		}
		logrus.WithError(err).WithField("app_id", appID).Error("slack webhook: enqueue failed")
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

// isRedelivery reports whether this event has already been handled.
//
// Two checks, because they fail differently. The retry header is what Slack
// stamps on an ordinary redelivery and costs nothing to read. The Redis claim
// catches the rest — a failover redelivery arrives with no header, and a
// retry may land on a different replica than the delivery it repeats.
//
// A Redis error processes the event: dropping a real question because a cache
// is unreachable is worse than the duplicate answer it would prevent.
func (h *SlackWebhookHandler) isRedelivery(c *gin.Context, appID string, env *slack.Envelope) bool {
	if retry := c.GetHeader("X-Slack-Retry-Num"); retry != "" {
		logrus.WithFields(logrus.Fields{
			"app_id":   appID,
			"retry":    retry,
			"reason":   c.GetHeader("X-Slack-Retry-Reason"),
			"event_id": env.EventID,
		}).Info("slack webhook: ignoring delivery retry")
		return true
	}
	if h.dedupe == nil || env.EventID == "" {
		return false
	}
	first, err := h.dedupe.FirstSight(c.Request.Context(), appID, env.EventID)
	if err != nil {
		logrus.WithError(err).WithField("app_id", appID).
			Warn("slack webhook: dedupe check failed; processing the event")
		return false
	}
	if !first {
		logrus.WithFields(logrus.Fields{"app_id": appID, "event_id": env.EventID}).
			Info("slack webhook: event already handled")
	}
	return !first
}
