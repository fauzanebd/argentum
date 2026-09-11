package app

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-N3's grammar, which is Appendix A of the roadmap stated as a table. The
// function is pure, so this file is the whole specification of who answers.

func room(names ...string) []*domain.ThreadParticipant {
	out := make([]*domain.ThreadParticipant, 0, len(names))
	for _, n := range names {
		out = append(out, &domain.ThreadParticipant{
			AgentID: "ag-" + strings.ToLower(strings.ReplaceAll(n, " ", "-")), AgentName: n,
		})
	}
	return out
}

func TestAddressingGrammar(t *testing.T) {
	three := room("Finance", "Ops", "People")

	cases := []struct {
		name         string
		text         string
		participants []*domain.ThreadParticipant
		wantIDs      []string
		wantCleaned  string
	}{
		{
			name: "nobody addressed is today's behaviour",
			text: "what happened to margin?", participants: three,
			wantIDs: nil, wantCleaned: "what happened to margin?",
		},
		{
			name: "one agent",
			text: "@Finance what happened to margin?", participants: three,
			wantIDs: []string{"ag-finance"}, wantCleaned: "what happened to margin?",
		},
		{
			name: "two agents, in the order addressed",
			text: "@Ops @Finance what happened?", participants: three,
			wantIDs: []string{"ag-ops", "ag-finance"}, wantCleaned: "what happened?",
		},
		{
			name: "case-insensitive",
			text: "@finance and @OPS?", participants: three,
			wantIDs: []string{"ag-finance", "ag-ops"}, wantCleaned: "and ?",
		},
		{
			name: "the same agent twice is one turn",
			text: "@Finance @Finance please", participants: three,
			wantIDs: []string{"ag-finance"}, wantCleaned: "please",
		},
		{
			name: "@all addresses the room",
			text: "@all what do you make of this?", participants: three,
			wantIDs:     []string{"ag-finance", "ag-ops", "ag-people"},
			wantCleaned: "what do you make of this?",
		},
		{
			name: "@everyone is its synonym",
			text: "@everyone thoughts?", participants: three,
			wantIDs:     []string{"ag-finance", "ag-ops", "ag-people"},
			wantCleaned: "thoughts?",
		},
		{
			name: "an unrecognised handle is text, not addressing",
			text: "@notanagent what happened?", participants: three,
			wantIDs: nil, wantCleaned: "@notanagent what happened?",
		},
		{
			name: "an email address is text",
			text: "email me at finance@example.com", participants: three,
			wantIDs: nil, wantCleaned: "email me at finance@example.com",
		},
		{
			name: "a name must match at a word boundary",
			text: "@opsummary please", participants: three,
			wantIDs: nil, wantCleaned: "@opsummary please",
		},
		{
			name: "a reserved handle must match at a word boundary too",
			text: "@allocation please", participants: three,
			wantIDs: nil, wantCleaned: "@allocation please",
		},
		{
			name: "a room of one still parses",
			text: "@Finance hi", participants: room("Finance"),
			wantIDs: []string{"ag-finance"}, wantCleaned: "hi",
		},
		{
			name: "no participants means no addressing",
			text: "@Finance hi", participants: nil,
			wantIDs: nil, wantCleaned: "@Finance hi",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAddressing(tc.text, tc.participants)
			if strings.Join(got.AgentIDs, ",") != strings.Join(tc.wantIDs, ",") {
				t.Errorf("AgentIDs = %v, want %v", got.AgentIDs, tc.wantIDs)
			}
			if got.Cleaned != tc.wantCleaned {
				t.Errorf("Cleaned = %q, want %q", got.Cleaned, tc.wantCleaned)
			}
		})
	}
}

// Longest name first, or a room holding both "Finance" and "Finance Team"
// resolves "@Finance Team" to the shorter one plus a stray word.
func TestTheLongerAgentNameWins(t *testing.T) {
	two := room("Finance", "Finance Team")

	got := ParseAddressing("@Finance Team what happened?", two)

	if len(got.AgentIDs) != 1 || got.AgentIDs[0] != "ag-finance-team" {
		t.Fatalf("AgentIDs = %v, want [ag-finance-team]", got.AgentIDs)
	}
	if got.Cleaned != "what happened?" {
		t.Errorf("Cleaned = %q, want %q", got.Cleaned, "what happened?")
	}
}

// The shorter name still resolves when it is the one addressed.
func TestTheShorterNameStillResolves(t *testing.T) {
	two := room("Finance", "Finance Team")

	got := ParseAddressing("@Finance what happened?", two)

	if len(got.AgentIDs) != 1 || got.AgentIDs[0] != "ag-finance" {
		t.Fatalf("AgentIDs = %v, want [ag-finance]", got.AgentIDs)
	}
}

// A message that addresses somebody and says nothing must not become an empty
// message. validate() refuses those, which is the right answer, and it can only
// do so if the text survives.
func TestAnAddressWithNoMessageKeepsItsText(t *testing.T) {
	got := ParseAddressing("@Finance", room("Finance"))

	if got.Cleaned == "" {
		t.Error("Cleaned is empty; validate() would report 'message required' against nothing")
	}
}

// Newlines survive. collapseSpaces squeezes spaces and leaves them alone, which
// strings.Fields would not.
func TestAMultiLineMessageKeepsItsLines(t *testing.T) {
	got := ParseAddressing("@Ops first line\nsecond line", room("Ops"))

	if !strings.Contains(got.Cleaned, "\n") {
		t.Errorf("Cleaned = %q, want the newline kept", got.Cleaned)
	}
}

func TestReservedHandlesCannotBeAgentNames(t *testing.T) {
	for _, n := range []string{"all", "All", " everyone ", "EVERYONE"} {
		if !IsReservedHandle(n) {
			t.Errorf("IsReservedHandle(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"Finance", "allocation", "everyone else"} {
		if IsReservedHandle(n) {
			t.Errorf("IsReservedHandle(%q) = true, want false", n)
		}
	}
}
