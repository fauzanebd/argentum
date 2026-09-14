package guardrails

import (
	"strings"
	"testing"
)

// The specimen, captured 2026-09-11 by replaying the backend's own request
// shape against OpenRouter with `decart` pinned. The prose is the model's
// reasoning, the tail is the call it never got to make, and the whole thing is
// what a user saw instead of an answer.
const decartSpecimen = "Pengguna ingin mengetahui member dengan pembelanjaan paling banyak bulan ini. " +
	"Saya perlu mengakses database dengan source_id yang diberikan: 785c6d43-33e1-4d36-a4ce-66058d9da8c3.\n\n" +
	"Langkah-langkah:\n1. Cek skema database untuk memahami struktur tabel yang tersedia\n" +
	"2. Identifikasi tabel yang berisi data transaksi/member\n3. Query untuk menjawab pertanyaan\n\n" +
	"Mari saya mulai dengan melihat skema database." +
	`functions.get_schema:0{"source_id":"785c6d43-33e1-4d36-a4ce-66058d9da8c3"}`

func TestTheCapturedLeakIsCaught(t *testing.T) {
	got, leaked := CheckToolCallLeak(decartSpecimen, TurnEvidence{}, "member dengan pembelanjaan paling banyak bulan ini")
	if !leaked {
		t.Fatal("the specimen that shipped a non-answer to a user was passed through")
	}
	// The reasoning must not survive into the replacement: publishing it is
	// the failure this guard exists to stop, not a milder version of it.
	if strings.Contains(got, "Langkah-langkah") || strings.Contains(got, "functions.get_schema") {
		t.Errorf("the replacement carries the reasoning or the leaked call:\n%s", got)
	}
	if !looksIndonesian(got) {
		t.Errorf("an Indonesian question got an English replacement:\n%s", got)
	}
}

func TestLeakShapes(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  bool
	}{
		{"single call", `Let me look.functions.get_schema:0{"source_id":"x"}`, true},
		{"two calls run together", `Checking.functions.get_schema:0{"source_id":"x"}functions.list_metrics:1{}`, true},
		{"newline before arguments", "Checking.\nfunctions.list_metrics:0\n{}", true},
		{"undecoded special tokens", `Checking.<|tool_call_begin|>functions.list_metrics<|tool_call_argument_begin|>{}`, true},
		{"undecoded section token", `<|tool_calls_section_begin|>`, true},
		// T-N6. A leaked nudge is the worst-reading one of these: the reply
		// narrates asking a colleague — "Finance holds the ledger, asking them" —
		// and the question was never posted and no turn was queued, so the
		// person waits for an answer that is not coming.
		{"a leaked nudge", "Finance holds the purchase ledger, so I will ask them." +
			`functions.nudge_agent:0{"agent":"Finance","question":"Was a goods-in posted for SKU 4471?"}`, true},
		{"prose about asking a colleague", "I asked Finance whether a goods-in was posted for SKU 4471.", false},

		// The negatives matter more than the positives here: this guard
		// replaces the entire reply, so a false positive costs a user an
		// answer they were entitled to.
		{"empty", "", false},
		{"an ordinary answer", "Top member bulan ini adalah Andi, dengan pembelanjaan Rp 12.400.000.", false},
		{"names its own tools in prose", "I ran list_metrics and get_schema, then queried the orders table.", false},
		{"a tool chip transcribed", "functions.get_schema returned 14 tables.", false},
		{"prose with a colon and a number", "See functions.md:12 for the helper list.", false},
		{"SQL in a fence", "```sql\nSELECT member_id, SUM(total) FROM orders GROUP BY 1\n```", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := CheckToolCallLeak(tt.reply, TurnEvidence{}, "")
			if got != tt.want {
				t.Errorf("CheckToolCallLeak(%q) leaked = %v; want %v", tt.reply, got, tt.want)
			}
		})
	}
}

// A turn can leak on its third iteration after two that worked. The user is
// owed the difference between "nothing happened" and "some of it did".
func TestAPartialTurnNamesWhatRan(t *testing.T) {
	ev := TurnEvidence{ToolCalls: 2, DataCalls: 1, DataRows: 8, Tools: []string{"get_schema", "run_sql"}}
	got, leaked := CheckToolCallLeak(`Now the metrics.functions.list_metrics:2{}`, ev, "who spent the most this month")
	if !leaked {
		t.Fatal("a leak after successful tools was passed through")
	}
	for _, tool := range ev.Tools {
		if !strings.Contains(got, tool) {
			t.Errorf("the replacement does not name %s, which is what makes it recoverable:\n%s", tool, got)
		}
	}
	if looksIndonesian(got) {
		t.Errorf("an English question got an Indonesian replacement:\n%s", got)
	}
}

func TestAPassedReplyIsReturnedUnchanged(t *testing.T) {
	const reply = "Top member bulan ini adalah Andi."
	got, leaked := CheckToolCallLeak(reply, TurnEvidence{ToolCalls: 2}, "siapa member teratas")
	if leaked {
		t.Fatal("a clean reply was replaced")
	}
	if got != reply {
		t.Errorf("reply = %q; want %q unchanged", got, reply)
	}
}

func TestLeakedToolCallNames(t *testing.T) {
	got := LeakedToolCallNames(`x.functions.get_schema:0{"a":1}functions.list_metrics:1{}functions.get_schema:2{}`)
	want := []string{"get_schema", "list_metrics"}
	if len(got) != len(want) {
		t.Fatalf("LeakedToolCallNames = %v; want %v (deduplicated, in order)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("LeakedToolCallNames[%d] = %q; want %q", i, got[i], want[i])
		}
	}
	if names := LeakedToolCallNames(`<|tool_calls_section_begin|>`); len(names) != 0 {
		t.Errorf("LeakedToolCallNames = %v; want none — an undecoded token carries no name", names)
	}
}
