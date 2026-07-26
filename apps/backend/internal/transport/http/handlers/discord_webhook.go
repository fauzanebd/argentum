package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/discord"
)

// DiscordWebhookHandler verifies Discord's Ed25519-signed interactions
// requests and responds to the PING handshake. Slash commands are not
// wired yet — anything other than PING gets a friendly "DM me instead" ack
// so clicking a stale command doesn't look broken.
type DiscordWebhookHandler struct {
	svc *app.DiscordService
}

func NewDiscordWebhookHandler(svc *app.DiscordService) *DiscordWebhookHandler {
	return &DiscordWebhookHandler{svc: svc}
}

// Register installs the interactions endpoint under the webhook group.
// Caller mounts under "/webhook" without auth, matching the WhatsApp route.
func (h *DiscordWebhookHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/discord/interactions", h.interactions)
}

// interactionPayload is the minimal shape we need to dispatch. Discord
// sends a fully rich body; we ignore the parts we don't act on.
type interactionPayload struct {
	Type          int    `json:"type"`
	ApplicationID string `json:"application_id"`
}

func (h *DiscordWebhookHandler) interactions(c *gin.Context) {
	signature := c.GetHeader("X-Signature-Ed25519")
	timestamp := c.GetHeader("X-Signature-Timestamp")
	if signature == "" || timestamp == "" {
		c.Status(http.StatusUnauthorized)
		return
	}
	body, err := c.GetRawData()
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	var p interactionPayload
	if err := json.Unmarshal(body, &p); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if p.ApplicationID == "" {
		c.Status(http.StatusBadRequest)
		return
	}

	row, err := h.svc.ResolveCompanyByApplication(c.Request.Context(), p.ApplicationID)
	if err != nil {
		logrus.WithError(err).WithField("application_id", p.ApplicationID).
			Warn("discord interaction: unknown application")
		c.Status(http.StatusUnauthorized)
		return
	}

	if err := discord.VerifySignature(row.PublicKey, signature, timestamp, body); err != nil {
		logrus.WithError(err).WithField("application_id", p.ApplicationID).
			Warn("discord interaction: signature verify failed")
		c.Status(http.StatusUnauthorized)
		return
	}

	switch p.Type {
	case discord.InteractionTypePing:
		c.JSON(http.StatusOK, gin.H{"type": discord.InteractionResponsePong})
	default:
		// No slash commands registered yet — direct the user to DM the bot.
		c.JSON(http.StatusOK, gin.H{
			"type": discord.InteractionResponseChannelMessageWithSource,
			"data": gin.H{
				"content": "Slash commands aren't enabled. DM the bot or @mention it instead.",
				"flags":   1 << 6, // EPHEMERAL
			},
		})
	}
}
