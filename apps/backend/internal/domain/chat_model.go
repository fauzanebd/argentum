package domain

// ChatModelOption is one model a workspace admin may pick as the chat model
// (RIZ-97): the primary tier, which writes every answer on every channel.
//
// The offered set is an allow-list in code (app.ChatModels), not free text,
// because a model id nobody priced bills at DefaultPricing and one without
// tool support cannot run a turn at all. The rates are the pricing table's,
// copied in when the option is served, so the picker shows what a turn on it
// will actually cost — credits are that same number.
type ChatModelOption struct {
	// Model is the provider id the resolver hands the client, e.g.
	// "deepseek/deepseek-v4.1-flash".
	Model string `json:"model"`
	// Label is what a person reads ("DeepSeek V4.1 Flash"). The dashboard
	// appends "(default)" to the default option itself, so the label stays
	// usable in a sentence.
	Label string `json:"label"`
	// Hint is one line on when to pick it. Empty for the deployment's own
	// model when that is not on the allow-list (local dev).
	Hint string `json:"hint"`
	// Default marks the option equal to LLM_MODEL: picking it removes the
	// workspace's override rather than pinning today's default.
	Default bool `json:"default"`
	// InputPer1KUSD and OutputPer1KUSD are the pricing table's rates. Zero
	// with PricingKnown false only for an unpriced deployment default; every
	// allow-listed model has a row, and a test holds that.
	InputPer1KUSD  float64 `json:"input_per_1k_usd"`
	OutputPer1KUSD float64 `json:"output_per_1k_usd"`
	PricingKnown   bool    `json:"pricing_known"`
}

// ChatModelSetting is a workspace's chat model as Settings > General shows it:
// what it runs on now, what it may choose between, and whether it may choose.
type ChatModelSetting struct {
	// Model is the resolved primary model: the override row's model when the
	// workspace has one, LLM_MODEL otherwise.
	Model string `json:"model"`
	// DefaultModel is LLM_MODEL. Choosing it deletes the override, so the
	// workspace follows the deployment from then on.
	DefaultModel string `json:"default_model"`
	// Locked is true when the workspace's primary row carries its own
	// provider — an API key, a base URL or an interface, set up by support.
	// The picker is not shown and a PUT is refused: writing a model over that
	// row could point the tenant's key at a model their provider does not
	// serve, and the row is not the admin's to reshape from here.
	Locked  bool              `json:"locked"`
	Options []ChatModelOption `json:"options"`
}
