package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/lark"
)

// LarkWebhookHandler is the public ingress for Lark event callbacks. Tenants
// configure their Lark app's "Event subscription" URL to point at
// /webhook/lark/events/:app_id so the handler can resolve the company before
// signature verification.
//
// Inbound flow: parse envelope (decrypt if encrypted) → verify
// verification_token matches → if event has X-Lark-Signature, verify HMAC →
// dispatch:
//   - url_verification → echo challenge.
//   - im.message.receive_v1 → require @mention of bot_open_id, run allowlist
//     check, enqueue chat:run. Silent 200 on reject.
type LarkWebhookHandler struct {
	larkSvc *app.LarkService
	chatEnq *app.ChatEnqueuer
	replier lark.Provider
}

func NewLarkWebhookHandler(larkSvc *app.LarkService, chatEnq *app.ChatEnqueuer) *LarkWebhookHandler {
	return &LarkWebhookHandler{larkSvc: larkSvc, chatEnq: chatEnq}
}

// WithReplier gives the API process an outbound Lark client. Until T-03 it
// had none — every reply is written by the worker after the agent runs — but
// a refusal happens before there is anything to enqueue, so the only process
// that can speak it is this one.
func (h *LarkWebhookHandler) WithReplier(p lark.Provider) *LarkWebhookHandler {
	h.replier = p
	return h
}

func (h *LarkWebhookHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/lark/events/:app_id", h.events)
}

func (h *LarkWebhookHandler) events(c *gin.Context) {
	appID := c.Param("app_id")
	if appID == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	cred, err := h.larkSvc.ResolveCompanyByAppID(c.Request.Context(), appID)
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

	env, plain, err := lark.ParseEnvelope(body, cred.EncryptKey)
	if err != nil {
		logrus.WithError(err).WithField("app_id", appID).Warn("lark webhook: parse envelope")
		c.Status(http.StatusBadRequest)
		return
	}

	// Verify HMAC signature when Lark sent one. Lark only signs when
	// encrypt_key is configured.
	if cred.EncryptKey != "" {
		sig := c.GetHeader("X-Lark-Signature")
		ts := c.GetHeader("X-Lark-Request-Timestamp")
		nonce := c.GetHeader("X-Lark-Request-Nonce")
		if sig != "" {
			if err := lark.VerifySignature(cred.EncryptKey, ts, nonce, sig, body); err != nil {
				logrus.WithError(err).WithField("app_id", appID).Warn("lark webhook: signature verify failed")
				c.Status(http.StatusUnauthorized)
				return
			}
		}
	}

	// url_verification: respond with the challenge. Verify verification_token
	// matches what we have on file so an attacker who guesses the URL can't
	// trick us into echoing arbitrary strings.
	if env.Type == "url_verification" {
		if env.Token != cred.VerificationToken {
			c.Status(http.StatusUnauthorized)
			return
		}
		c.JSON(http.StatusOK, gin.H{"challenge": env.Challenge})
		return
	}

	// All other events carry the verification token in header.token (v2).
	if env.Header.Token != "" && env.Header.Token != cred.VerificationToken {
		logrus.WithField("app_id", appID).Warn("lark webhook: verification_token mismatch")
		c.Status(http.StatusUnauthorized)
		return
	}

	if env.Header.EventType != "im.message.receive_v1" {
		// Ignore other event types — quietly ack so Lark stops retrying.
		c.Status(http.StatusOK)
		return
	}

	var ev lark.MessageReceiveEvent
	if err := json.Unmarshal(env.Event, &ev); err != nil {
		logrus.WithError(err).WithField("app_id", appID).Warn("lark webhook: parse message event")
		c.Status(http.StatusBadRequest)
		return
	}
	_ = plain // available for future audit logging

	// Mention-only trigger. If the bot's open_id is unknown we cannot enforce
	// this — skip silently and let the admin configure it.
	if cred.BotOpenID == "" {
		logrus.WithField("app_id", appID).Warn("lark webhook: bot_open_id not configured; ignoring")
		c.Status(http.StatusOK)
		return
	}
	if !lark.MentionsBot(ev.Message.Mentions, cred.BotOpenID) {
		c.Status(http.StatusOK)
		return
	}

	// Only text messages are supported today.
	if ev.Message.MessageType != "text" {
		c.Status(http.StatusOK)
		return
	}

	text, err := lark.DecodeText(ev.Message.Content)
	if err != nil || text == "" {
		c.Status(http.StatusOK)
		return
	}
	text = lark.StripMentions(text)
	if text == "" {
		c.Status(http.StatusOK)
		return
	}

	openID := ev.Sender.SenderID.OpenID
	if openID == "" {
		c.Status(http.StatusOK)
		return
	}

	allowed, err := h.larkSvc.IsUserAllowed(c.Request.Context(), cred.CompanyID, openID)
	if err != nil {
		logrus.WithError(err).Warn("lark webhook: allowlist lookup")
		c.Status(http.StatusInternalServerError)
		return
	}
	if !allowed {
		// Silent drop per product decision.
		c.Status(http.StatusOK)
		return
	}

	threadKey := firstNonEmpty(ev.Message.ThreadID, ev.Message.RootID, ev.Message.MessageID)

	if _, err := h.chatEnq.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:       domain.ChannelLark,
		CompanyID:     cred.CompanyID,
		LarkOpenID:    openID,
		LarkChatID:    ev.Message.ChatID,
		LarkThreadKey: threadKey,
		LarkMessageID: ev.Message.MessageID,
		Message:       text,
	}); err != nil {
		if errors.Is(err, domain.ErrInsufficientCredits) {
			if h.replier != nil {
				if rerr := h.replier.Reply(c.Request.Context(), cred.CompanyID, ev.Message.MessageID, app.CreditsExhaustedMessage); rerr != nil {
					logrus.WithError(rerr).WithField("company_id", cred.CompanyID).
						Warn("lark webhook: could not deliver the credit refusal")
				}
			}
			// 200 either way — Lark retries a non-2xx, and the tenant's
			// position will not have changed by the retry.
			c.Status(http.StatusOK)
			return
		}
		logrus.WithError(err).WithField("app_id", appID).Error("lark webhook: enqueue failed")
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
