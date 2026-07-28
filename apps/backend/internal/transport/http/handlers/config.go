package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
)

// ConfigHandler exposes server-side configuration that the dashboard needs to
// render settings screens (e.g. which LLM models are wired up for chat,
// summaries, and topic classification).
type ConfigHandler struct {
	cfg *config.Config
}

func NewConfigHandler(cfg *config.Config) *ConfigHandler {
	return &ConfigHandler{cfg: cfg}
}

// Register installs the routes. Call after Auth middleware.
func (h *ConfigHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/config/models", h.models)
}

type modelInfo struct {
	Role           string  `json:"role"`
	Model          string  `json:"model"`
	Interface      string  `json:"interface"`
	InputPer1KUSD  float64 `json:"input_per_1k_usd"`
	OutputPer1KUSD float64 `json:"output_per_1k_usd"`
	PricingKnown   bool    `json:"pricing_known"`
}

// models returns the resolved model used for each agent role plus the per-1K
// token rates from the pricing table. The classifier role reports the light
// model when LLM_CLASSIFIER_MODEL is unset (mirrors BuildClassifier's
// fallback so the UI shows what would actually run).
func (h *ConfigHandler) models(c *gin.Context) {
	primary := buildModelInfo("primary", h.cfg.LLMModel, h.cfg.EffectiveLLMInterface())
	light := buildModelInfo("light", h.cfg.LightLLMModel, h.cfg.EffectiveLightLLMInterface())

	classifierModel := h.cfg.ClassifierModel
	classifierInterface := h.cfg.EffectiveLightLLMInterface()
	if classifierModel == "" {
		classifierModel = h.cfg.LightLLMModel
	}
	classifier := buildModelInfo("classifier", classifierModel, classifierInterface)

	c.JSON(http.StatusOK, gin.H{
		"primary":    primary,
		"light":      light,
		"classifier": classifier,
	})
}

func buildModelInfo(role, model, iface string) modelInfo {
	info := modelInfo{Role: role, Model: model, Interface: iface}
	if p, ok := app.LookupModelPricing(model); ok {
		info.InputPer1KUSD = p.InputCostPer1K
		info.OutputPer1KUSD = p.OutputCostPer1K
		info.PricingKnown = true
	}
	return info
}
