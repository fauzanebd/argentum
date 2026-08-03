package guardrails

import (
	"strings"
	"testing"
)

func TestStatesFigure(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  bool
	}{
		// The two observed fabrications, verbatim.
		{"C-1 dollar figure", "Total Sales for December 2024: $1,234,567.89", true},
		{"empty-result rupiah", "**Total Sales for December 2024:** **IDR 1,488,000**", true},

		{"rupiah grouped", "Total penjualan Rp 3.863.405.700", true},
		{"magnitude suffix", "Total penjualan sekitar Rp 3,86 Miliar", true},
		{"english magnitude", "That comes to 2.5 million in revenue", true},
		{"grouped count", "We have 1,348 transactions on file", true},
		{"currency suffix", "The total was 1,250.00 USD", true},

		// Refusals and honest answers legitimately carry numbers. Blocking
		// these would make the guardrail worse than the failure it prevents.
		{"year range", "I have data for July–December 2024 only.", false},
		{"iso dates", "The data covers 2024-07-01 to 2024-12-31.", false},
		{"small bare count", "3 sources are connected to this organisation.", false},
		{"percentage", "Margins are usually expressed as a percentage, e.g. 27.6%.", false},
		{"no numbers at all", "I could not complete that query.", false},

		// A presigned URL is a wall of digits and separators, and a SQL block
		// the agent is explaining may quote literals. Neither is a claim.
		{"markdown link", "Here is your file: [December report](https://minio.local/x/1,234,567.pdf?sig=9.999.999)", false},
		{"code fence", "```sql\nselect sum(sales_amount) -- e.g. 1.234.567\nfrom fact_sales\n```", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatesFigure(tt.reply); got != tt.want {
				t.Errorf("StatesFigure(%q) = %v, want %v", tt.reply, got, tt.want)
			}
		})
	}
}

func TestCheckFabrication(t *testing.T) {
	figure := "Total Sales for December 2024: IDR 1,488,000"

	tests := []struct {
		name        string
		reply       string
		ev          TurnEvidence
		wantBlocked bool
	}{
		{
			name:        "C-1: budget exhausted, no rows, figure stated",
			reply:       figure,
			ev:          TurnEvidence{ToolCalls: 3, DataCalls: 1, Exhausted: true, Reason: "iteration budget spent"},
			wantBlocked: true,
		},
		{
			name:        "empty result, figure stated",
			reply:       figure,
			ev:          TurnEvidence{ToolCalls: 2, DataCalls: 1, EmptyResults: 1},
			wantBlocked: true,
		},
		{
			name:        "schema lookups only, figure stated",
			reply:       figure,
			ev:          TurnEvidence{ToolCalls: 2},
			wantBlocked: true,
		},
		{
			name:        "rows returned: the figure is grounded",
			reply:       figure,
			ev:          TurnEvidence{ToolCalls: 2, DataCalls: 1, DataRows: 1},
			wantBlocked: false,
		},
		{
			name:        "honest incomplete answer passes",
			reply:       "I could not complete that query — no rows matched December 2024.",
			ev:          TurnEvidence{ToolCalls: 2, DataCalls: 1, EmptyResults: 1},
			wantBlocked: false,
		},
		{
			// A follow-up turn ("show that in millions") restates a figure
			// from an earlier turn without querying. No tool ran, so there is
			// nothing to have fabricated against.
			name:        "no tools ran: follow-up turn is left alone",
			reply:       figure,
			ev:          TurnEvidence{},
			wantBlocked: false,
		},
		{
			// The live bug: "yes, make that PDF" after a turn that already had
			// the data. generate_document is the only call, and the reply — a
			// link plus a summary of what is in the file — was replaced with a
			// message saying no query ran, after the user watched it stream.
			name:        "the turn only wrote the file: a restatement, not a fabrication",
			reply:       figure + "\n\n[Download the report](https://minio.local/x/report.pdf)",
			ev:          TurnEvidence{ToolCalls: 1, DeliverableCalls: 1},
			wantBlocked: false,
		},
		{
			// The carve-out does not extend to a turn that asked the data a
			// question and got nothing back. Writing a file out of that answer
			// does not make the answer grounded.
			name:        "a file written over an empty query is still blocked",
			reply:       figure,
			ev:          TurnEvidence{ToolCalls: 3, DataCalls: 1, EmptyResults: 1, DeliverableCalls: 1},
			wantBlocked: true,
		},
		{
			// Nor to a turn cut short: the reserved document call (see
			// agentbudget) writes a file on a spent budget, and a figure in that
			// reply still has to have come from a row.
			name:        "a file written on an exhausted budget is still blocked",
			reply:       figure,
			ev:          TurnEvidence{ToolCalls: 12, DataCalls: 2, DeliverableCalls: 1, Exhausted: true},
			wantBlocked: true,
		},
		{
			// A card is built from SQL the model never saw the rows of, so it
			// grounds nothing. Only the deliverable earns the carve-out.
			name:        "a chart card does not excuse an ungrounded figure",
			reply:       figure,
			ev:          TurnEvidence{ToolCalls: 2},
			wantBlocked: true,
		},
		{
			name:        "greeting",
			reply:       "Hi! Ask me a question about your business data.",
			ev:          TurnEvidence{},
			wantBlocked: false,
		},
		{
			name:        "empty reply",
			reply:       "",
			ev:          TurnEvidence{ToolCalls: 3, Exhausted: true},
			wantBlocked: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, blocked := CheckFabrication(tt.reply, tt.ev, "What were our total sales last month?")
			if blocked != tt.wantBlocked {
				t.Fatalf("blocked = %v, want %v (reply %q)", blocked, tt.wantBlocked, tt.reply)
			}
			if !blocked {
				if out != tt.reply {
					t.Errorf("unblocked reply was rewritten: %q", out)
				}
				return
			}
			if StatesFigure(out) {
				t.Errorf("replacement itself states a figure: %q", out)
			}
		})
	}
}

// The replacement must not break the reply-language discipline the system
// prompt spends its first guideline on: an Indonesian question gets an
// Indonesian refusal.
func TestIncompleteAnswerFollowsQuestionLanguage(t *testing.T) {
	ev := TurnEvidence{ToolCalls: 2, DataCalls: 1, EmptyResults: 1}
	figure := "Total penjualan Desember 2024: Rp 1.488.000"

	id, blocked := CheckFabrication(figure, ev, "Berapa total penjualan bulan lalu?")
	if !blocked {
		t.Fatal("Indonesian fabrication was not blocked")
	}
	if !strings.Contains(id, "kueri") {
		t.Errorf("Indonesian question got a non-Indonesian reply: %q", id)
	}

	en, blocked := CheckFabrication(figure, ev, "What were our total sales last month?")
	if !blocked {
		t.Fatal("English fabrication was not blocked")
	}
	if !strings.Contains(en, "query") {
		t.Errorf("English question got a non-English reply: %q", en)
	}
}

// The message has to say which of the three ways the turn failed, or the user
// cannot tell "your filter matched nothing" from "I ran out of steps".
func TestIncompleteAnswerNamesTheCause(t *testing.T) {
	tests := []struct {
		name string
		ev   TurnEvidence
		want string
	}{
		{"exhausted", TurnEvidence{ToolCalls: 4, Exhausted: true}, "ran out of steps"},
		{"empty", TurnEvidence{ToolCalls: 2, DataCalls: 1, EmptyResults: 1}, "matched no rows"},
		{"never queried", TurnEvidence{ToolCalls: 2}, "did not get as far as running a query"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, blocked := CheckFabrication("Total: $1,234,567.89", tt.ev, "What were our sales?")
			if !blocked {
				t.Fatal("not blocked")
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("message = %q, want it to contain %q", out, tt.want)
			}
		})
	}
}
