package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/whatsapp"
	"github.com/fauzanebd/argentum/pkg/models"
)

// WebhookHandler routes inbound WhatsApp / Twilio messages to the unified
// chat pipeline. Phone numbers must already be on a company's allowlist or
// the message is rejected.
//
// The signature on the inbound request is the whole of this endpoint's
// authentication — it is mounted outside middleware.Auth by design, because the
// caller is Meta or Twilio and holds no Argentum credential. Everything a
// verified request reaches is a tenant's: their warehouse through run_sql,
// their registered endpoints through http_action, and any action kind they
// marked auto-approved.
type WebhookHandler struct {
	chat    *app.ChatEnqueuer
	company *app.CompanyService
	wa      whatsapp.Provider
	// transport is the deployment's configured provider. It is deliberately not
	// derived from the request: doing that (T-H1) let a caller select the
	// Twilio branch on a Meta deployment by sending one header.
	transport whatsapp.Transport
	verifyTok string
}

func NewWebhookHandler(chat *app.ChatEnqueuer, company *app.CompanyService, wa whatsapp.Provider, transport whatsapp.Transport, verifyTok string) *WebhookHandler {
	return &WebhookHandler{chat: chat, company: company, wa: wa, transport: transport, verifyTok: verifyTok}
}

// Register installs both verification (GET) and message (POST) endpoints.
func (h *WebhookHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/whatsapp", h.verify)
	rg.POST("/whatsapp", h.receive)
}

// verify handles the WhatsApp Business API GET handshake.
func (h *WebhookHandler) verify(c *gin.Context) {
	// The handshake belongs to Meta. Twilio has no equivalent, and answering
	// one on a Twilio deployment would echo an attacker's own `hub.challenge`
	// back to them for the asking.
	if h.transport != whatsapp.TransportMeta {
		c.Status(http.StatusForbidden)
		return
	}
	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")
	challenge := c.Query("hub.challenge")
	if mode == "subscribe" && h.wa.VerifyToken(token, h.verifyTok) {
		c.String(http.StatusOK, challenge)
		return
	}
	c.Status(http.StatusForbidden)
}

// receive parses the inbound payload (Twilio form-encoded or WhatsApp JSON),
// resolves the company by phone number, and enqueues a chat:run task.
func (h *WebhookHandler) receive(c *gin.Context) {
	var msg *models.Message
	// media is what Twilio said about the message's attachments. Twilio only:
	// Meta names the kind in the message's own `type` and has no equivalent of
	// either field, so both stay zero there and stay out of its log lines.
	var media twilioMedia

	if h.transport == whatsapp.TransportTwilio {
		if err := c.Request.ParseForm(); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		// Twilio signs the URL it was configured with, query string included,
		// so the string reconstructed here has to carry one if the tenant's
		// console entry does. The scheme is fixed rather than read off the
		// request: a TLS terminator in front of this process leaves
		// c.Request.TLS nil on a request Twilio made over https, and Twilio's
		// console will not accept a plain-http callback anyway.
		webhookURL := "https://" + c.Request.Host + c.Request.URL.RequestURI()
		if !h.wa.VerifyWebhook([]byte(c.Request.PostForm.Encode()), c.GetHeader("X-Twilio-Signature"), webhookURL) {
			// 401 and stop. This logged "continuing in dev mode" and fell
			// through (T-H1), so any POST with a plausible `From` enqueued a
			// turn against whichever tenant owns that number.
			logrus.WithField("client_ip", c.ClientIP()).
				Warn("whatsapp webhook: Twilio signature verification failed")
			c.Status(http.StatusUnauthorized)
			return
		}
		msg = &models.Message{
			ID:          c.PostForm("MessageSid"),
			PhoneNumber: strings.TrimPrefix(c.PostForm("From"), "whatsapp:"),
			Body:        c.PostForm("Body"),
			MessageType: "text",
		}
		// Only the first attachment's type is read. WhatsApp sends one item per
		// message — an album arrives as several messages — and the reply is the
		// same whatever the kind; the kind is there for the log line. A message
		// with text keeps "text" even when it carries a photo: its caption is a
		// question, and it is answered as one (RIZ-85, R4).
		media = twilioMedia{
			num:         parseNumMedia(c.PostForm("NumMedia")),
			contentType: c.PostForm("MediaContentType0"),
		}
		if media.num > 0 && strings.TrimSpace(msg.Body) == "" {
			msg.MessageType = whatsapp.TwilioMediaKind(media.contentType)
		}
	} else {
		body, err := c.GetRawData()
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		if !h.wa.VerifyWebhook(body, c.GetHeader("X-Hub-Signature-256"), "") {
			logrus.WithField("client_ip", c.ClientIP()).
				Warn("whatsapp webhook: Meta signature verification failed")
			c.Status(http.StatusUnauthorized)
			return
		}
		parsed, perr := h.wa.ParseWebhook(body)
		if perr != nil {
			c.Status(http.StatusOK)
			return
		}
		msg = parsed
	}

	if msg == nil {
		c.Status(http.StatusOK)
		return
	}

	// Three outcomes, where this used to be one silent drop (RIZ-85). A voice
	// note on WhatsApp is how a lot of Indonesian users ask anything, and to them
	// a bot that says nothing back looks broken rather than limited.
	//
	//   - text: answered as it always was. A photo with a caption is text.
	//   - an attachment with no text: one fixed reply asking them to type, after
	//     the allowlist — an unknown number hears the not-authorised line instead,
	//     exactly as it would for text, so this reply confirms nothing about
	//     which numbers are known.
	//   - neither — a reaction, a shared location, an empty Twilio form: nobody
	//     is waiting on an answer, so nothing is sent, but the drop is logged.
	//     Before this, none of these left a trace.
	//
	// Meta's status callbacks (sent, delivered, read) never reach here: they
	// carry no message, ParseWebhook refuses them above, and logging one per
	// outbound message would bury the lines this ticket added.
	hasText := msg.MessageType == "text" && strings.TrimSpace(msg.Body) != ""
	attachment := !hasText && whatsapp.IsAttachment(msg.MessageType)
	if !hasText && !attachment {
		// No phone number, no body: a log line is read by more people than the
		// conversation it describes. The message id is enough to find it in the
		// provider's console.
		logrus.WithFields(logrus.Fields{
			"transport":    string(h.transport),
			"message_sid":  msg.ID,
			"message_type": msg.MessageType,
		}).Info("whatsapp: inbound message not handled; dropped")
		c.Status(http.StatusOK)
		return
	}

	companyID, err := h.company.ResolveCompanyByPhone(c.Request.Context(), msg.PhoneNumber)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			logrus.WithField("from", msg.PhoneNumber).Info("inbound from unknown phone number — dropping")
			_ = h.wa.SendMessage(msg.PhoneNumber,
				"This phone number is not authorised. Ask your Argentum admin to add it in the dashboard.")
			c.Status(http.StatusOK)
			return
		}
		c.Status(http.StatusInternalServerError)
		return
	}

	if attachment {
		h.askForText(c, msg, media, companyID)
		return
	}

	if _, err := h.chat.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:     domain.ChannelWhatsApp,
		CompanyID:   companyID,
		PhoneNumber: msg.PhoneNumber,
		Message:     msg.Body,
	}); err != nil {
		// 200 with a spoken refusal, not 500: WhatsApp retries a non-2xx, and
		// retrying a turn the tenant cannot pay for — or one whose agent is
		// closed to this number (T-Z8) — delivers the same sentence several
		// times.
		if refusal, ok := app.SpokenRefusal(err); ok {
			_ = h.wa.SendMessage(msg.PhoneNumber, refusal)
			c.Status(http.StatusOK)
			return
		}
		logrus.WithError(err).Error("chat enqueue failed")
		c.Status(http.StatusInternalServerError)
		return
	}

	c.Status(http.StatusOK)
}

// twilioMedia is the two attachment fields Twilio's form carries beside Body.
type twilioMedia struct {
	num         int
	contentType string
}

// parseNumMedia reads Twilio's `NumMedia`. Absent or malformed is zero: the
// field is Twilio's and signed, so a value that does not parse is not an attack
// to refuse, it is a message with no attachment we can count.
func parseNumMedia(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// askForText answers an attachment with no text: one fixed message asking the
// sender to type (whatsapp.AttachmentReply), and no turn. Nothing is enqueued,
// so nothing is charged — the attachment is not read, and slice B is what will.
//
// Always 200, including when the reply could not be sent. The provider retries a
// non-2xx, and a retry of this request is a second copy of the same sentence the
// moment the outage ends — the reason the credit refusal above answers 200 too.
// The failure is a Warn instead, carrying the error, and keeps "attachment
// without text" in its message so one search counts every such message whether
// or not its reply arrived. That count is what sizes slice B.
func (h *WebhookHandler) askForText(c *gin.Context, msg *models.Message, media twilioMedia, companyID string) {
	// The fields RIZ-85 names and no others: no phone number, no body, no media
	// URL. The URL in particular is a bearer link to the recording on Twilio's
	// side, readable by whoever reads the log.
	fields := logrus.Fields{
		"transport":   string(h.transport),
		"message_sid": msg.ID,
		"company_id":  companyID,
		"media_kind":  msg.MessageType,
	}
	if h.transport == whatsapp.TransportTwilio {
		fields["content_type"] = media.contentType
		fields["num_media"] = media.num
	}
	if err := h.wa.SendMessage(msg.PhoneNumber, whatsapp.AttachmentReply); err != nil {
		logrus.WithError(err).WithFields(fields).
			Warn("whatsapp: attachment without text; the reply asking the sender to type was not sent")
		c.Status(http.StatusOK)
		return
	}
	logrus.WithFields(fields).Info("whatsapp: attachment without text; asked the sender to type")
	c.Status(http.StatusOK)
}
