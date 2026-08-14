package guardrails

import (
	"fmt"
	"strings"
)

// CheckEmptyReply is the last thing between a finished turn and a blank
// message.
//
// Observed twice in 58 scored turns of the 2026-08-14 eval run
// (docs/coverage/eval-q1.md), and the clean specimen is the one worth keeping:
// `chart-monthly-trend` called `get_schema`, `create_visualization` and
// `create_dashboard`, every call succeeded, a dashboard was actually built —
// and the reply was the empty string. The user sees a blank answer, no error,
// and no reason to think anything happened. It is not deterministic: the same
// case passed in one run and produced nothing forty minutes later on identical
// code.
//
// Nothing else in the turn path tests for this. CheckFabrication returns early
// on an empty reply (it has no figure to judge), CheckGrounding has no figures
// to compare, and Analytics.ProcessOutput is skipped entirely — correct for a
// redaction rule, and it means the last component to touch the reply hands the
// empty string straight on to be persisted and published.
//
// So this does not diagnose the cause; it converts the failure into a sentence.
// The two upstream candidates — a final provider message carrying no text, and
// a reply lost in the streaming assembly, which builds from delta events — are
// separated by the log line the caller writes beside this, not by the
// replacement text. What the user gets meanwhile is recoverable: "I built the
// dashboard and could not summarise it" can be acted on and a blank cannot.
//
// Returns the replacement reply and true when the original must not be sent.
func CheckEmptyReply(reply string, ev TurnEvidence, userInput string) (string, bool) {
	if strings.TrimSpace(reply) != "" {
		return reply, false
	}
	return emptyReplyAnswer(ev, userInput), true
}

// emptyReplyAnswer names the work the turn did, because that is the whole
// difference between a recoverable message and an apology. It follows
// incompleteAnswer's language rule: an English question gets an English
// answer, and the same Indonesian detection is reused, so the reply-language
// discipline the system prompt enforces is not broken by the guard that
// replaces the reply.
func emptyReplyAnswer(ev TurnEvidence, userInput string) string {
	id := looksIndonesian(userInput)

	if ev.ToolCalls == 0 {
		if id {
			return "Maaf — giliran ini selesai tanpa menghasilkan jawaban, dan tidak ada kueri " +
				"yang sempat dijalankan. Ini kesalahan di sisi kami, bukan masalah pada pertanyaan " +
				"Anda. Silakan kirim ulang pertanyaannya."
		}
		return "Sorry — this turn finished without producing an answer, and without running " +
			"anything either. That is a fault on our side, not a problem with your question. " +
			"Please send it again."
	}

	steps := namedSteps(ev.Tools)
	if id {
		if steps == "" {
			return "Saya sudah menjalankan pekerjaan untuk pertanyaan ini, tetapi giliran ini " +
				"berakhir tanpa jawaban tertulis. Ini kesalahan di sisi kami. Apa pun yang " +
				"dihasilkan langkah-langkah tadi tetap tersimpan — tanyakan lagi dan akan saya " +
				"rangkum."
		}
		return fmt.Sprintf(
			"Saya sudah menjalankan pekerjaan untuk pertanyaan ini — %s — tetapi giliran ini "+
				"berakhir tanpa jawaban tertulis. Ini kesalahan di sisi kami. Apa pun yang "+
				"dihasilkan langkah-langkah tadi tetap tersimpan — tanyakan lagi dan akan saya "+
				"rangkum.", steps)
	}
	if steps == "" {
		return "I did the work for this, but the turn ended without a written answer. That is a " +
			"fault on our side. Anything those steps produced still exists — ask me again and " +
			"I will summarise it."
	}
	return fmt.Sprintf(
		"I did the work for this — %s — but the turn ended without a written answer. That is a "+
			"fault on our side. Anything those steps produced still exists — ask me again and "+
			"I will summarise it.", steps)
}

// maxNamedSteps bounds how many tool names reach the user. A turn may call the
// same tool six times; the point of the list is to say what kind of work was
// done, not to transcribe the turn.
const maxNamedSteps = 4

// namedSteps renders the turn's tools in call order, deduplicated. Tool names
// are shown raw because the product already shows them raw: the dashboard puts
// a `run_sql` chip above the answer, so `create_dashboard` in a sentence names
// something the user has seen this surface call itself.
func namedSteps(tools []string) string {
	seen := make(map[string]bool, len(tools))
	names := make([]string, 0, maxNamedSteps)
	for _, t := range tools {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		if len(names) == maxNamedSteps {
			return strings.Join(names, ", ") + " and more"
		}
		names = append(names, t)
	}
	return strings.Join(names, ", ")
}
