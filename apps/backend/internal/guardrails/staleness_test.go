package guardrails

import (
	"strings"
	"testing"
)

const staleNote = "this source last loaded 3 days ago (2026-09-08 02:00 UTC), which is beyond its 1 day staleness threshold"

func TestAStaleTurnGetsTheNoticeAppended(t *testing.T) {
	reply := "Revenue last week was Rp 1.2 Miliar."
	got, added := CheckStaleness(reply, TurnEvidence{Freshness: "stale", FreshnessNote: staleNote})
	if !added {
		t.Fatal("a stale turn was not dated")
	}
	if !strings.HasPrefix(got, reply) {
		t.Errorf("the answer was changed rather than appended to:\n%s", got)
	}
	if !strings.Contains(got, "2026-09-08") {
		t.Errorf("the notice does not say when the data is from:\n%s", got)
	}
}

// The three verdicts that must leave the answer alone. Warn is deliberately one
// of them: it dates the tool payload so the model can mention it, and does not
// staple a caveat to every answer for six hours of ordinary ETL lag.
func TestEveryVerdictBelowStaleLeavesTheAnswerAlone(t *testing.T) {
	reply := "Revenue last week was Rp 1.2 Miliar."
	for _, v := range []string{"", "unknown", "fresh", "warn"} {
		got, added := CheckStaleness(reply, TurnEvidence{Freshness: v, FreshnessNote: staleNote})
		if added || got != reply {
			t.Errorf("verdict %q amended the answer: %q", v, got)
		}
	}
}

// The one case where a caveat would be the whole message.
func TestAnEmptyReplyIsNotDated(t *testing.T) {
	for _, reply := range []string{"", "   ", "\n"} {
		if _, added := CheckStaleness(reply, TurnEvidence{Freshness: "stale", FreshnessNote: staleNote}); added {
			t.Errorf("an empty reply (%q) was given a freshness notice and nothing else", reply)
		}
	}
}

// A model that already dated its answer must not be made to say it twice.
func TestAModelThatAlreadySaidItIsNotToldTwice(t *testing.T) {
	reply := "Revenue was Rp 1.2 Miliar, though note the data only runs to 2026-09-08."
	got, added := CheckStaleness(reply, TurnEvidence{Freshness: "stale", FreshnessNote: staleNote})
	if added {
		t.Errorf("the notice was appended to a reply that already carried the date:\n%s", got)
	}
}

// …but a vague gesture at freshness is not the statement this guard makes. The
// point of the notice is the date, and a reply without one has not made it.
func TestAVagueHedgeDoesNotSuppressTheNotice(t *testing.T) {
	reply := "Revenue was Rp 1.2 Miliar, though the data may be somewhat out of date."
	if _, added := CheckStaleness(reply, TurnEvidence{Freshness: "stale", FreshnessNote: staleNote}); !added {
		t.Error("a hedge with no date suppressed the notice; the date is the claim")
	}
}

func TestAStaleTurnWithNoNoteStillSaysSomething(t *testing.T) {
	got, added := CheckStaleness("Revenue was Rp 1.2 Miliar.", TurnEvidence{Freshness: "stale"})
	if !added {
		t.Fatal("a stale turn with no note was not dated at all")
	}
	if !strings.Contains(got, "refreshed") {
		t.Errorf("fallback notice = %q", got)
	}
}

// This guard adds; it never withholds. A stale answer is still the answer.
func TestTheOriginalAnswerSurvivesInFull(t *testing.T) {
	reply := "Revenue last week was Rp 1.2 Miliar, up 4% on the week before."
	got, _ := CheckStaleness(reply, TurnEvidence{Freshness: "stale", FreshnessNote: staleNote})
	for _, fragment := range []string{"Rp 1.2 Miliar", "up 4%", "the week before"} {
		if !strings.Contains(got, fragment) {
			t.Errorf("%q was lost from the answer", fragment)
		}
	}
}

func TestAsOfDateFindsTheDateAndNothingElse(t *testing.T) {
	cases := map[string]string{
		staleNote:                "2026-09-08",
		"no date in here at all": "",
		"loaded 12-34-56 ago":    "",
		"2026-09-08":             "2026-09-08",
	}
	for note, want := range cases {
		if got := asOfDate(note); got != want {
			t.Errorf("asOfDate(%q) = %q; want %q", note, got, want)
		}
	}
}
