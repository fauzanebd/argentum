package guardrails

import "testing"

// The reply from the 2026-08-18 gate, verbatim. It carries no figure at all,
// which is why every existing instrument passed it.
func TestTheGateReplyIsAClaim(t *testing.T) {
	reply := "Done — your dashboard is now called **Q4 2024 Sales Review**.\n" +
		"The URL stays the same, so any existing links will continue to work."
	name, ok := ClaimsCompletedAction(reply)
	if !ok {
		t.Fatal("the reply that started T-Q13 is not detected as a claim")
	}
	t.Logf("matched %s", name)
}

func TestCompletionLanguageIsAClaim(t *testing.T) {
	for _, reply := range []string{
		"I've updated the dashboard to use a line chart.",
		"I have successfully created the schedule for Monday mornings.",
		"I renamed the dashboard for you.",
		"Your dashboard has been renamed.",
		"The report has been scheduled for 09:00 every Monday.",
		"The dashboard is now called Q4 2024 Sales Review.",
		// Indonesian, from the first version rather than added after a gate
		// found the English-only instrument missing one — the T-Q3 lesson.
		"Judul dasbor sudah saya ubah menjadi Q4 2024.",
		"Laporan telah dijadwalkan setiap Senin pagi.",
		"Dasbor berhasil dibuat.",
	} {
		if _, ok := ClaimsCompletedAction(reply); !ok {
			t.Errorf("not detected as a claim: %q", reply)
		}
	}
}

// The false positives that would make this counter unreadable. Every one of
// these is an honest reply.
func TestHonestRepliesAreNotClaims(t *testing.T) {
	for _, reply := range []string{
		// The refusal the same gate produced on its third attempt — this is what
		// a turn that ran out of budget says, and it must never be counted.
		"My exploration budget for this turn has been exhausted, so I was not able to rename it. " +
			"The dashboard is still called Q4 2024 Sales.",
		"I have not updated the dashboard yet — which panel did you mean?",
		"The dashboard has not been renamed because two dashboards match that description.",
		"Would you like me to rename it?",
		"I can rename that dashboard if you confirm the new title.",
		"December revenue was 3,863,405,700 across 310 transactions.",
		"Saya belum mengubah dasbor tersebut karena ada dua dasbor dengan nama serupa.",
	} {
		if name, ok := ClaimsCompletedAction(reply); ok {
			t.Errorf("honest reply counted as a claim (%s): %q", name, reply)
		}
	}
}

// A reply about work from an earlier turn is a true statement about the past.
// Counting it would fire the instrument on honest replies, which is the one
// direction of error that makes a counter useless later.
func TestAClaimAboutAnEarlierTurnIsNotThisTurnsClaim(t *testing.T) {
	for _, reply := range []string{
		"The dashboard I created earlier is still called Q4 2024 Sales.",
		"I updated the panel previously; nothing has changed since.",
		"Dasbor itu sudah saya buat sebelumnya.",
	} {
		if name, ok := ClaimsCompletedAction(reply); ok {
			t.Errorf("a prior-turn statement counted as a claim (%s): %q", name, reply)
		}
	}
}

// A claim in one sentence is not excused by a prior-turn marker in another.
func TestAPriorTurnSentenceDoesNotExcuseANewClaim(t *testing.T) {
	reply := "I built that dashboard earlier. I have now renamed it to Q4 2024 Sales Review."
	if _, ok := ClaimsCompletedAction(reply); !ok {
		t.Error("a fresh claim beside a prior-turn sentence was not detected")
	}
}

// Links and code blocks are not prose. A markdown link to /dashboards/<uuid> is
// in almost every one of these replies.
func TestClaimDetectionIgnoresLinksAndCode(t *testing.T) {
	reply := "Here is the query I would run:\n```sql\n-- updated the totals\nSELECT 1;\n```\n" +
		"Shall I go ahead?"
	if name, ok := ClaimsCompletedAction(reply); ok {
		t.Errorf("a code comment was read as a claim (%s)", name)
	}
}
