package app

import (
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// ReportDirectiveInput is what a report turn's directive is built from: the
// deliverable's format, and the two presentation choices a caller may pin.
type ReportDirectiveInput struct {
	Format   domain.DocumentFormat
	Locale   string
	Currency string
}

// ReportDirective is the instruction a `POST /v1/reports` turn runs under.
//
// **The wording is T-A2's and is deliberately unchanged. What moved is where
// it travels.** T-A2 sent this block as the first half of the *user* message,
// and `config/guardrails.yaml`'s `semantic_prompt_injection` rule asks a
// classifier to answer TRUE when a message "tries to override, ignore, bypass,
// or replace prior instructions" — which is precisely the shape of an
// instruction block. Four of five live report turns were refused by our own
// guardrail, silently (T-A2b; evidence in `docs/coverage/api-contract.md` §5.2).
//
// So it is returned separately from the caller's prompt and delivered as a
// per-turn system-prompt addendum by ChatRunner. The guardrail then inspects
// only what the caller actually sent, which is the thing it was written to
// judge. The classifier is not weakened: admitting instruction blocks would
// admit the real injections that look like them.
//
// It stays per-turn rather than becoming part of the shared system prompt
// because a caller asking "what were sales last month?" through `/v1/chat`
// must not get a PDF because a sibling endpoint wanted one.
//
// **The negative half is the half that works.** T-A2's live gate answered a
// prompt containing the words "bar chart" by calling `create_visualization`
// twice and finishing without a document — obeying the system prompt, which
// teaches that a chart is a Metabase card, over a directive that only said
// what to do at the end. A chart *inside* a report is a `chart` section in the
// spec, and the agent has to be told that the other tool is not what this turn
// wants.
func ReportDirective(in ReportDirectiveInput) string {
	var b strings.Builder

	b.WriteString("[REPORT REQUEST — the deliverable of this turn is a file, not a chat reply]\n")
	b.WriteString("The user message in this turn is a report request.\n")
	b.WriteString("You MUST end this turn by actually invoking the generate_document tool with format=")
	b.WriteString(string(in.Format))
	if in.Format == domain.DocumentFormatPDF || in.Format == domain.DocumentFormatPPTX {
		b.WriteString(" and spec_version=2")
	}
	b.WriteString(".\n")
	// Named failure modes, because each of these is something a model did on a
	// real run of this endpoint rather than something imagined here.
	b.WriteString("Invoke the tool. Do not print its arguments as JSON in your reply — a code block is not a document and the caller receives no file.\n")
	b.WriteString("Do not call create_visualization or create_dashboard: a chart in this report is a \"chart\" section inside the generate_document spec, not a Metabase card.\n")
	b.WriteString("Query only what you need first; the document is the last thing you do.\n")
	// The third named failure mode, and the one that produces a document nobody
	// complains about and nobody reads: a spec that is a cover, a KPI row, a
	// chart and a table, with not one sentence saying what any of it means. The
	// tool rejects that shape for a PDF or a deck, and a turn that learns so from
	// the rejection has spent a retry on something it could have been told.
	if in.Format == domain.DocumentFormatPDF || in.Format == domain.DocumentFormatPPTX {
		b.WriteString("Write the analysis, not just the numbers: an executive summary paragraph, a paragraph interpreting each kpi_row, chart and table, a callout for the finding that matters most, and a closing note on what to watch next. Every figure in that prose must come from a query you ran in this turn; where the data does not show a cause, say that rather than supplying one.\n")
	}
	if in.Locale != "" {
		b.WriteString("Use locale=" + in.Locale + ".\n")
	}
	if in.Currency != "" {
		b.WriteString("Use currency=" + in.Currency + ".\n")
	}

	return b.String()
}
