package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/llmtenant"
)

// PrimaryResolver is the half of llmtenant.Resolver the models route needs:
// the profile a turn for this workspace would run on.
type PrimaryResolver interface {
	Resolve(ctx context.Context, companyID string, tier domain.LLMTier) (*llmtenant.EffectiveProfile, error)
}

// ConfigHandler exposes server-side configuration that the dashboard needs to
// render settings screens (e.g. which LLM models are wired up for chat,
// summaries, and topic classification), and the one model a workspace may
// choose for itself: its chat model (RIZ-97).
//
// **Dashboard-only.** `/api` routes with no `/v1` twin, so no openapi entry:
// the ticket keeps the choice in Settings, where an admin makes it.
type ConfigHandler struct {
	cfg *config.Config
	// resolver and chatModels are nil on a router built without a database
	// (cmd/api's policy tests). The models route then answers from env as it
	// did before RIZ-97, and the PUT answers 503, so the route still registers
	// and the role table still sees it.
	resolver   PrimaryResolver
	chatModels *app.ChatModelService
}

// NewConfigHandler wires it. resolver and chatModels may be nil; see the
// struct.
func NewConfigHandler(cfg *config.Config, resolver PrimaryResolver, chatModels *app.ChatModelService) *ConfigHandler {
	return &ConfigHandler{cfg: cfg, resolver: resolver, chatModels: chatModels}
}

// The sentences an admin reads when a save is refused. Spelled once, here, so
// the copy the owner approved is in one place; the dashboard shows the server's
// sentence inside its own "Could not save the chat model: …" line.
const (
	chatModelMsgOwnProvider = "This workspace runs on its own model provider. Contact support to change its chat model."
	chatModelMsgUnavailable = "Choosing a chat model is not available right now. Please try again later."
	chatModelMsgFailed      = "could not save the chat model"
	chatModelMsgEmpty       = "choose a chat model"
	modelsMsgFailed         = "could not read this workspace's models"
)

// Register installs the routes. Call after Auth middleware.
func (h *ConfigHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/config/models", h.models)
	rg.PUT("/config/chat-model", h.setChatModel)
}

// models returns the model used for each agent role plus the per-1K token
// rates from the pricing table. The classifier role reports the light model
// when LLM_CLASSIFIER_MODEL is unset (mirrors BuildClassifier's fallback so
// the UI shows what would actually run).
//
// Primary is the caller's workspace's, resolved the way a turn resolves it, so
// a workspace on DeepSeek is not told it runs Kimi (R7). Light and classifier
// stay env: no workspace can change them from here.
//
// A failure to resolve is a 500, not a quiet fall back to env: this response
// is what the Settings card and the chat header say the workspace runs on, and
// reporting LLM_MODEL for a workspace whose row could not be read is the very
// misreport the ticket fixed. The card is absent on an error, as it always was.
func (h *ConfigHandler) models(c *gin.Context) {
	ctx := c.Request.Context()
	cid := companyID(c)

	primaryModel, primaryInterface := h.cfg.LLMModel, h.cfg.EffectiveLLMInterface()
	if h.resolver != nil && cid != "" {
		p, err := h.resolver.Resolve(ctx, cid, domain.LLMTierPrimary)
		if err != nil {
			logrus.WithError(err).WithField("company_id", cid).Error("resolve primary model for settings")
			c.JSON(http.StatusInternalServerError, gin.H{"error": modelsMsgFailed})
			return
		}
		primaryModel, primaryInterface = p.Model, p.Interface
	}

	classifierModel := h.cfg.ClassifierModel
	if classifierModel == "" {
		classifierModel = h.cfg.LightLLMModel
	}
	resp := ModelsResponse{
		Primary:    buildModelInfo(ModelRolePrimary, primaryModel, primaryInterface),
		Light:      buildModelInfo(ModelRoleLight, h.cfg.LightLLMModel, h.cfg.EffectiveLightLLMInterface()),
		Classifier: buildModelInfo(ModelRoleClassifier, classifierModel, h.cfg.EffectiveLightLLMInterface()),
	}

	if h.chatModels != nil && cid != "" {
		setting, err := h.chatModels.Get(ctx, cid)
		if err != nil {
			logrus.WithError(err).WithField("company_id", cid).Error("read chat model for settings")
			c.JSON(http.StatusInternalServerError, gin.H{"error": modelsMsgFailed})
			return
		}
		resp.ChatModel = setting
	}
	c.JSON(http.StatusOK, resp)
}

// setChatModel is `PUT /api/config/chat-model`, admin in apiPolicy. It answers
// the new setting, so the card can show what was saved without a second read.
//
// A body that does not parse is treated as naming no model, which the service
// refuses as not offered: there is no third outcome worth a separate code.
func (h *ConfigHandler) setChatModel(c *gin.Context) {
	if h.chatModels == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": chatModelMsgUnavailable})
		return
	}
	var req ChatModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = ChatModelRequest{}
	}
	setting, err := h.chatModels.Set(c.Request.Context(), companyID(c), userID(c), req.Model)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, setting)
	case errors.Is(err, domain.ErrModelNotOffered):
		msg := chatModelMsgEmpty
		if m := strings.TrimSpace(req.Model); m != "" {
			msg = fmt.Sprintf("%s is not offered on this workspace", m)
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": msg, "code": "model_not_offered"})
	case errors.Is(err, domain.ErrOwnProvider):
		c.JSON(http.StatusConflict, gin.H{"error": chatModelMsgOwnProvider, "code": "own_provider"})
	default:
		logrus.WithError(err).WithField("company_id", companyID(c)).Error("save chat model")
		c.JSON(http.StatusInternalServerError, gin.H{"error": chatModelMsgFailed})
	}
}

func buildModelInfo(role ModelRole, model, iface string) ModelInfo {
	info := ModelInfo{Role: role, Model: model, Interface: iface}
	if p, ok := app.LookupModelPricing(model); ok {
		info.InputPer1KUSD = p.InputCostPer1K
		info.OutputPer1KUSD = p.OutputCostPer1K
		info.PricingKnown = true
	}
	return info
}
