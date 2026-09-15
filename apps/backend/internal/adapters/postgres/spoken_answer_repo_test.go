package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Source-reading tests, for voice_clip_repo_test.go's reason. The statements
// running against real rows is the scratch arm (live-gate §7l).

func spokenAnswerStatements(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("spoken_answer_repo.go")
	if err != nil {
		t.Fatalf("read spoken_answer_repo.go: %v", err)
	}
	var out []string
	for _, m := range regexp.MustCompile("(?s)const q = `(.*?)`").FindAllStringSubmatch(stripComments(string(raw)), -1) {
		out = append(out, m[1])
	}
	if len(out) != 5 {
		t.Fatalf("found %d statements in spoken_answer_repo.go, want 5; this test is no longer reading what it thinks it is", len(out))
	}
	return out
}

// Every statement but the sweep's is scoped by company, and the sweep's writes
// nothing and carries no answer.
func TestSpokenAnswerCrossCompanyReadCarriesNoAnswer(t *testing.T) {
	unscoped := 0
	for _, stmt := range spokenAnswerStatements(t) {
		if strings.Contains(stmt, "company_id = $") {
			continue
		}
		unscoped++
		if regexp.MustCompile(`(?i)\b(DELETE|INSERT|UPDATE)\b`).MatchString(stmt) {
			t.Errorf("an unscoped statement writes:\n%s", strings.TrimSpace(stmt))
		}
		selected := strings.SplitN(stmt, "FROM", 2)[0]
		for _, col := range []string{"spoken_text", "refusal", "message_id", "voice", "model"} {
			if strings.Contains(selected, col) {
				t.Errorf("the cross-company read selects %q:\n%s", col, strings.TrimSpace(stmt))
			}
		}
	}
	if unscoped != 1 {
		t.Errorf("%d statements have no company predicate, want exactly the sweep's one", unscoped)
	}
}

// The company a row is filed under is the message's conversation's, read inside
// the insert — never a value the caller passed in a column list — and a second
// save for the same message replaces the first.
func TestSpokenAnswerSaveTakesTheCompanyFromTheConversation(t *testing.T) {
	var insert string
	for _, stmt := range spokenAnswerStatements(t) {
		if strings.Contains(stmt, "INSERT") {
			insert = stmt
		}
	}
	for _, want := range []string{
		"t.company_id, m.id",
		"JOIN conversation_threads t ON t.id = m.thread_id",
		"WHERE m.id = $3 AND t.company_id = $2",
		"ON CONFLICT (message_id) WHERE message_id IS NOT NULL DO UPDATE",
	} {
		if !strings.Contains(insert, want) {
			t.Errorf("the insert does not contain %q:\n%s", want, strings.TrimSpace(insert))
		}
	}
}
