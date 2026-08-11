package handlers

import (
	"errors"
	"net/http"
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

	if msg == nil || msg.MessageType != "text" || strings.TrimSpace(msg.Body) == "" {
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

	if _, err := h.chat.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:     domain.ChannelWhatsApp,
		CompanyID:   companyID,
		PhoneNumber: msg.PhoneNumber,
		Message:     msg.Body,
	}); err != nil {
		// 200 with a spoken refusal, not 500: WhatsApp retries a non-2xx, and
		// retrying a turn the tenant cannot pay for delivers the same sentence
		// several times.
		if errors.Is(err, domain.ErrInsufficientCredits) {
			_ = h.wa.SendMessage(msg.PhoneNumber, app.CreditsExhaustedMessage)
			c.Status(http.StatusOK)
			return
		}
		logrus.WithError(err).Error("chat enqueue failed")
		c.Status(http.StatusInternalServerError)
		return
	}

	c.Status(http.StatusOK)
}
