package app

import "strings"

// ModelPricing is the per-1000-token rate for a single model. Rates here are
// what we charge customers — they should track real provider pricing but are
// allowed to drift conservatively.
type ModelPricing struct {
	InputCostPer1K  float64
	OutputCostPer1K float64
}

// modelPricing is keyed on the lowercase model string returned by
// interfaces.LLM.Name(). Refresh from provider pricing pages when adding
// models. Unknown models fall back to UsageService.pricing (DefaultPricing).
var modelPricing = map[string]ModelPricing{
	// OpenAI — https://openai.com/api/pricing/
	"gpt-4o":      {InputCostPer1K: 0.0025, OutputCostPer1K: 0.010},
	"gpt-4o-mini": {InputCostPer1K: 0.00015, OutputCostPer1K: 0.0006},
	"gpt-4-turbo": {InputCostPer1K: 0.010, OutputCostPer1K: 0.030},

	// Anthropic — Claude 4.x family. https://www.anthropic.com/pricing
	"claude-opus-4-7":           {InputCostPer1K: 0.005, OutputCostPer1K: 0.025},
	"claude-sonnet-4-6":         {InputCostPer1K: 0.003, OutputCostPer1K: 0.015},
	"claude-opus-4-6":           {InputCostPer1K: 0.005, OutputCostPer1K: 0.025},
	"claude-opus-4-5":           {InputCostPer1K: 0.005, OutputCostPer1K: 0.025},
	"claude-haiku-4-5":          {InputCostPer1K: 0.001, OutputCostPer1K: 0.005},
	"claude-haiku-4-5-20251001": {InputCostPer1K: 0.001, OutputCostPer1K: 0.005},
	"claude-sonnet-4-5":         {InputCostPer1K: 0.003, OutputCostPer1K: 0.015},
	"claude-opus-4-1":           {InputCostPer1K: 0.015, OutputCostPer1K: 0.075},
	"claude-sonnet-4":           {InputCostPer1K: 0.003, OutputCostPer1K: 0.015},
	"claude-opus-4":             {InputCostPer1K: 0.015, OutputCostPer1K: 0.075},

	// Anthropic — Claude 3.x family (legacy).
	"claude-3-5-sonnet-20241022": {InputCostPer1K: 0.003, OutputCostPer1K: 0.015},
	"claude-3-5-sonnet-latest":   {InputCostPer1K: 0.003, OutputCostPer1K: 0.015},
	"claude-3-5-haiku-20241022":  {InputCostPer1K: 0.0008, OutputCostPer1K: 0.004},
	"claude-3-5-haiku-latest":    {InputCostPer1K: 0.0008, OutputCostPer1K: 0.004},
	"claude-3-opus-20240229":     {InputCostPer1K: 0.015, OutputCostPer1K: 0.075},

	// Gemini — https://ai.google.dev/pricing
	"gemini-1.5-pro":        {InputCostPer1K: 0.00125, OutputCostPer1K: 0.005},
	"gemini-1.5-flash":      {InputCostPer1K: 0.000075, OutputCostPer1K: 0.0003},
	"gemini-2.5-flash":      {InputCostPer1K: 0.0003, OutputCostPer1K: 0.0025},
	"gemini-2.5-flash-lite": {InputCostPer1K: 0.0001, OutputCostPer1K: 0.0004},
}

// lookupModelPricing finds rates for a model. Match is case-insensitive; if
// the exact key isn't found, the suffix after the last '/' or '.' is tried —
// catches gateway prefixes like "openai/gpt-4o" or "anthropic.claude-...".
func lookupModelPricing(model string) (ModelPricing, bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	if p, ok := modelPricing[key]; ok {
		return p, true
	}
	for _, sep := range []string{"/", "."} {
		if i := strings.LastIndex(key, sep); i >= 0 && i < len(key)-1 {
			if p, ok := modelPricing[key[i+1:]]; ok {
				return p, true
			}
		}
	}
	return ModelPricing{}, false
}
