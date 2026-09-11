package app

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/freshness"
)

// internal/agentbudget and internal/guardrails both sit below internal/freshness
// in the import graph, so both spell the verdicts as literal strings rather than
// importing the constants. This is the test that stops the two sides drifting:
// it lives here, in the one package that imports all three, and it fails the
// moment somebody renames a verdict without touching the readers.
func TestTheVerdictWireStringsAreWhatTheReadersExpect(t *testing.T) {
	cases := map[freshness.Verdict]string{
		freshness.Unknown: "unknown",
		freshness.Fresh:   "fresh",
		freshness.Warn:    "warn",
		freshness.Stale:   "stale",
	}
	for v, want := range cases {
		if string(v) != want {
			t.Errorf("verdict %v serialises as %q; internal/agentbudget and internal/guardrails read %q", v, string(v), want)
		}
	}
}
