package tools

import (
	"context"

	"github.com/fauzanebd/argentum/internal/freshness"
)

// FreshnessProber is the one question a data tool asks about the source it just
// read: how current is it (T-F2).
//
// Declared here as a consumer interface, like PIIPolicyLookup and SchemaProvider
// beside it, so the tools do not acquire internal/app and so a test can answer
// it without a database. A nil prober is the honest default: every verdict is
// `unknown`, and unknown says nothing to anybody.
type FreshnessProber interface {
	For(ctx context.Context, companyID, sourceID string) freshness.Report
}

// probeFreshness asks the prober, tolerating a build that has none.
func probeFreshness(ctx context.Context, p FreshnessProber, companyID, sourceID string) freshness.Report {
	if p == nil || companyID == "" || sourceID == "" {
		return freshness.Report{Verdict: freshness.Unknown}
	}
	return p.For(ctx, companyID, sourceID)
}

// attachFreshness puts the source's currency on a tool result, beside
// attachProbe and attachRedaction and for the same reason: the model should be
// able to reason about it and phrase it, rather than be handed a sentence to
// repeat.
//
// **Unknown attaches nothing at all**, which is what makes this safe to turn on
// for every tenant at once. A source nobody configured, and a source whose probe
// failed, produce a payload byte-identical to the one this tool returned before
// T-F2 existed — so the model's behaviour on every deployment that has not
// opted in is unchanged, and there is no sentence for it to over-read.
//
// The server states staleness anyway, deterministically, in
// guardrails.CheckStaleness. That division is T-16's: what the product's
// trustworthiness rests on is not left to a model's discretion, and what makes
// an answer read naturally is.
func attachFreshness(payload map[string]interface{}, rep freshness.Report) {
	if rep.Verdict == "" || rep.Verdict == freshness.Unknown {
		return
	}
	block := map[string]interface{}{
		"verdict": string(rep.Verdict),
	}
	if !rep.ObservedAt.IsZero() {
		block["data_as_of"] = rep.ObservedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if rep.Note != "" {
		block["note"] = rep.Note
	}
	switch rep.Verdict {
	case freshness.Stale:
		block["guidance"] = "This source has not loaded recently. State when the data is from before " +
			"quoting any figure from it, and do not describe a drop to zero or a missing recent period " +
			"as a business result — it may simply not have loaded yet."
	case freshness.Warn:
		block["guidance"] = "Mention when this data is from if the question is about a recent period."
	}
	payload["data_freshness"] = block
}
