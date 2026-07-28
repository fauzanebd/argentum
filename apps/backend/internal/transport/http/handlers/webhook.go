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
type WebhookHandler struct {
	chat      *app.ChatEnqueuer
	company   *app.CompanyService
	wa        whatsapp.Provider
	verifyTok string
}

func NewWebhookHandler(chat *app.ChatEnqueuer, company *app.CompanyService, wa whatsapp.Provider, verifyTok string) *WebhookHandler {
	return &WebhookHandler{chat: chat, company: company, wa: wa, verifyTok: verifyTok}
}

// Register installs both verification (GET) and message (POST) endpoints.
func (h *WebhookHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/whatsapp", h.verify)
	rg.POST("/whatsapp", h.receive)
}

// verify handles the WhatsApp Business API GET handshake.
func (h *WebhookHandler) verify(c *gin.Context) {
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
	isTwilio := c.GetHeader("X-Twilio-Signature") != "" || c.ContentType() == "application/x-www-form-urlencoded"
	var msg *models.Message
	var err error

	if isTwilio {
		if err := c.Request.ParseForm(); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		twilioSig := c.GetHeader("X-Twilio-Signature")
		webhookURL := "https://" + c.Request.Host + c.Request.URL.Path
		if !h.wa.VerifyWebhook([]byte(c.Request.PostForm.Encode()), twilioSig, webhookURL) {
			logrus.Warn("invalid Twilio signature (continuing in dev mode)")
		}
		from := strings.TrimPrefix(c.PostForm("From"), "whatsapp:")
		msg = &models.Message{
			ID:          c.PostForm("MessageSid"),
			PhoneNumber: from,
			Body:        c.PostForm("Body"),
			MessageType: "text",
		}
	} else {
		body, err := c.GetRawData()
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		sig := c.GetHeader("X-Hub-Signature-256")
		if !h.wa.VerifyWebhook(body, sig, "") {
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
	_ = err

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
