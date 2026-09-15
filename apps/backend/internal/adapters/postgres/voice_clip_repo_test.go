package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Source-reading tests, for retention_repo_test.go's reason: the properties are
// about what the statements *say*, and a fake database answers whatever it was
// written to answer. The statements themselves running is live-gate §7j.

func voiceClipStatements(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("voice_clip_repo.go")
	if err != nil {
		t.Fatalf("read voice_clip_repo.go: %v", err)
	}
	code := stripComments(string(raw))
	var out []string
	for _, m := range regexp.MustCompile("(?s)const q = `(.*?)`").FindAllStringSubmatch(code, -1) {
		out = append(out, m[1])
	}
	// Five since T-W9 added ForMessage, the one read that returns what was said.
	if len(out) != 5 {
		t.Fatalf("found %d statements in voice_clip_repo.go, want 5; this test is no longer reading what it thinks it is", len(out))
	}
	return out
}

// A read that returns a transcript returns the caller's own (T-W9): the ids it
// is asked about come from a browser, so the company, the person and the
// conversation are all in the statement, not trusted from the ids.
func TestVoiceClipTranscriptReadIsTheSendersOwn(t *testing.T) {
	reads := 0
	for _, stmt := range voiceClipStatements(t) {
		// Statements that begin with SELECT: Create is an INSERT … SELECT whose
		// column list names transcript too, and it writes one rather than reading.
		if !strings.HasPrefix(strings.TrimSpace(stmt), "SELECT") ||
			!strings.Contains(strings.SplitN(stmt, "FROM", 2)[0], "transcript") {
			continue
		}
		reads++
		for _, predicate := range []string{"company_id = $", "user_id = $", "thread_id = $"} {
			if !strings.Contains(stmt, predicate) {
				t.Errorf("a read returning transcripts has no %q predicate:\n%s", strings.TrimSuffix(predicate, " = $"), strings.TrimSpace(stmt))
			}
		}
	}
	if reads != 1 {
		t.Errorf("%d reads return transcripts, want exactly ForMessage", reads)
	}
}

// Every write is scoped by company: a DELETE with no company predicate empties
// every tenant's clips, and an INSERT that took the conversation on trust would
// record one company's clip against another's conversation.
func TestEveryVoiceClipWriteIsTenantScoped(t *testing.T) {
	for _, stmt := range voiceClipStatements(t) {
		isWrite := regexp.MustCompile(`(?i)\b(DELETE|INSERT)\b`).MatchString(stmt)
		if isWrite && !strings.Contains(stmt, "company_id = $") {
			t.Errorf("a write has no company_id predicate:\n%s", strings.TrimSpace(stmt))
		}
	}
}

// The sweep's read crosses companies by design. What it may not carry is
// anything a person said or who said it.
func TestVoiceClipCrossCompanyReadCarriesNothingSaid(t *testing.T) {
	unscoped := 0
	for _, stmt := range voiceClipStatements(t) {
		if strings.Contains(stmt, "company_id = $") {
			continue
		}
		unscoped++
		if regexp.MustCompile(`(?i)\b(DELETE|INSERT|UPDATE)\b`).MatchString(stmt) {
			t.Errorf("an unscoped statement writes:\n%s", strings.TrimSpace(stmt))
		}
		for _, col := range []string{"transcript", "user_id", "thread_id,", "language", "mime_type"} {
			if strings.Contains(strings.SplitN(stmt, "FROM", 2)[0], col) {
				t.Errorf("the cross-company read selects %q:\n%s", strings.TrimSuffix(col, ","), strings.TrimSpace(stmt))
			}
		}
	}
	if unscoped != 1 {
		t.Errorf("%d statements have no company predicate, want exactly the sweep's one", unscoped)
	}
}

func TestVoiceClipStatementsAreConstant(t *testing.T) {
	raw, err := os.ReadFile("voice_clip_repo.go")
	if err != nil {
		t.Fatalf("read voice_clip_repo.go: %v", err)
	}
	if strings.Contains(stripComments(string(raw)), "fmt.Sprintf(") {
		t.Error("voice_clip_repo.go builds SQL with fmt.Sprintf; every predicate here must be a bound parameter")
	}
}
