package app

// The three shapes the skills surface answers with (T-K6, T-K7).
//
// **They are in a file of their own for the reason `budget_state.go` is**, and
// it is a build rule rather than taste: `packages/api-types` is generated from
// a named list of files, and a wire shape declared beside a service the
// dashboard has no business seeing would either go ungenerated — leaving the
// dashboard hand-writing a mirror of a Go struct, which is the defect T-02b
// deleted four files to end — or drag `SkillService` itself into the published
// types.
//
// Nothing here has behaviour. The services in `skill_service.go` and
// `skill_draft.go` build these; `internal/transport/http/handlers/wire.go`
// wraps them in the envelopes each route answers with.

// SkillPreview is what an author is shown while typing: the two strings the
// model will see, and the counts that decide whether the save is refused.
//
// **It is rendered by the server rather than by the form**, and that is the
// whole point of the endpoint. A dashboard that assembled `- name — trigger`
// itself, or drew its own frame markers, would be a second implementation of
// the two things this feature is — and the moment it drifted, the preview would
// be reassuring an author about bytes nobody sends.
type SkillPreview struct {
	// IndexLine is the line that rides every turn's system prompt.
	IndexLine string `json:"index_line"`
	// FramedBody is what `load_skill` returns, markers included. It is
	// `skill.Frame`'s output, so an author who pasted a fence marker into their
	// procedure sees it neutralised here rather than discovering it in a turn.
	FramedBody string `json:"framed_body"`

	// The counts, in runes, because the caps are in runes: a rule that let an
	// English procedure be 8,000 characters and an Indonesian one 5,300 would
	// be a cap on the alphabet.
	NameChars      int `json:"name_chars"`
	WhenToUseChars int `json:"when_to_use_chars"`
	BodyChars      int `json:"body_chars"`
	IndexLineChars int `json:"index_line_chars"`

	// Refusal is what Validate would say if this were saved now, or empty.
	// Served so the form can show the sentence the API would answer with
	// rather than inventing its own wording for the same rule.
	Refusal string `json:"refusal,omitempty"`
}

// SkillIndexCost is what this workspace's procedures cost every turn.
//
// **The bound is invisible until somebody looks at the index**, which is the
// failure T-K9's own negative case measures from the other side: a tenant whose
// twenty-first procedure is never offered has no way to find that out from the
// list screen. Dropped names are the answer.
type SkillIndexCost struct {
	// Chars and Lines are what the composed block actually costs, header
	// included — the header is not free and it is two-thirds of the cost of a
	// workspace with one skill.
	Chars int `json:"chars"`
	Lines int `json:"lines"`

	MaxChars int `json:"max_chars"`
	MaxLines int `json:"max_lines"`

	// Dropped names the procedures that did not fit, in the order they were
	// lost. Non-empty means some of this workspace's own procedures are not
	// offered to any agent on any turn.
	Dropped []string `json:"dropped"`
}

// SkillDraft is what the button returns: three fields and the provenance to
// display beside them.
type SkillDraft struct {
	Name      string `json:"name"`
	WhenToUse string `json:"when_to_use"`
	Body      string `json:"body"`
	// ThreadID is what it was drafted from, so the form can say so. A tenant
	// about to save a procedure should be able to see which conversation put
	// those words on the screen.
	ThreadID string `json:"thread_id"`
	// Messages and ToolCalls are how much the draft actually saw. A draft from
	// a two-message thread with no tool calls is a draft from almost nothing,
	// and the person deciding whether to trust it should be told.
	Messages  int `json:"messages"`
	ToolCalls int `json:"tool_calls"`
}
