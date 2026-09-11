package guardrails

import (
	"fmt"
	"regexp"
	"strings"
)

// leakedToolCall matches a model's *native* tool-call syntax sitting in
// assistant prose, where only structured `tool_calls` belong.
//
// Two shapes, because two things can go wrong upstream. The first is a
// provider that decoded the model's special tokens but never mapped them onto
// the OpenAI wire format, which is how Kimi K2's calls arrive as
// `functions.get_schema:0{"source_id":…}` — a name, the call's index, and its
// JSON arguments run together. The second is a provider that did not decode
// them at all, leaving `<|tool_call_begin|>` in the text.
//
// The `\s*\{` is load-bearing. Without it the pattern is `functions.` followed
// by a word and a number, which an answer about a database could legitimately
// contain; with it, the match requires a JSON argument object immediately
// after, which prose does not produce by accident. This guard replaces the
// whole reply, so a false positive costs a user their answer — it is tuned to
// miss rather than to over-catch.
var leakedToolCall = regexp.MustCompile(`functions\.[A-Za-z0-9_.-]+:\d+\s*\{|<\|tool_calls?(?:_section)?_begin\|>`)

// CheckToolCallLeak catches the turn that asked for a tool and was never given
// one.
//
// **What it is looking at.** When an OpenRouter endpoint answers a
// tool-calling request with the model's native syntax as text instead of
// structured `tool_calls`, agent-sdk-go's streaming loop sees an assistant
// message carrying no tool calls, decides the model is finished, and breaks
// after a single iteration. Nothing errors. The reply that reaches the user is
// the model's *reasoning* — "I need to check if there is a defined metric for
// this. Let me call list_metrics first" — with the unexecuted call glued to the
// end of it, and it stops mid-thought because the model had not started
// answering yet. Measured against `moonshotai/kimi-k2.6` on 2026-09-11; see
// llmroute.BrokenToolCallProviders for which endpoints do it.
//
// **Why it replaces rather than trims.** Stripping the leaked call would leave
// the reasoning, and the reasoning is not an answer — publishing it hands the
// user a confident-looking paragraph about work that never happened, which is
// worse than the blank CheckEmptyReply exists to catch, because a blank is
// obviously broken and this is not.
//
// **Why it is worth having at all**, given that llmroute now keeps routing off
// those endpoints: the deny-list is a list, and lists are always one provider
// behind. This is what makes the next one a logged, countable failure with a
// recoverable message instead of a week of turns that quietly stopped.
//
// Returns the replacement reply and true when the original must not be sent.
func CheckToolCallLeak(reply string, ev TurnEvidence, userInput string) (string, bool) {
	if !leakedToolCall.MatchString(reply) {
		return reply, false
	}
	return toolCallLeakAnswer(ev, userInput), true
}

// toolCallLeakAnswer follows emptyReplyAnswer's language rule — an Indonesian
// question gets an Indonesian answer — and its shape: say that this is our
// fault, name any work that did complete, and ask for the question again,
// because a retry genuinely fixes it. Routing is per request, so the next
// attempt is very unlikely to land on the same endpoint.
func toolCallLeakAnswer(ev TurnEvidence, userInput string) string {
	id := looksIndonesian(userInput)

	if ev.ToolCalls == 0 {
		if id {
			return "Maaf — saya tidak sempat menjalankan satu langkah pun untuk pertanyaan ini. " +
				"Penyedia model mengembalikan permintaan itu dalam bentuk yang tidak bisa kami " +
				"jalankan. Ini kesalahan di sisi kami, bukan masalah pada pertanyaan Anda. " +
				"Silakan kirim ulang pertanyaannya."
		}
		return "Sorry — I never got to run a single step for this. The model provider returned " +
			"that request in a form we could not execute. That is a fault on our side, not a " +
			"problem with your question. Please send it again."
	}

	steps := namedSteps(ev.Tools)
	if id {
		return fmt.Sprintf(
			"Saya sempat menjalankan sebagian pekerjaan untuk pertanyaan ini — %s — tetapi "+
				"langkah berikutnya kembali dalam bentuk yang tidak bisa kami jalankan, jadi "+
				"giliran ini berhenti sebelum ada jawaban. Ini kesalahan di sisi kami. Silakan "+
				"kirim ulang pertanyaannya.", steps)
	}
	return fmt.Sprintf(
		"I got part of the way through this — %s — but the next step came back in a form we "+
			"could not execute, so the turn stopped before there was an answer. That is a fault "+
			"on our side. Please send the question again.", steps)
}

// LeakedToolCallNames reports the tool names found in a leaked reply, for the
// log line beside the replacement. What makes one of these findable afterwards
// is knowing what the agent was trying to do when it stopped, and the
// replacement text deliberately does not carry it.
func LeakedToolCallNames(reply string) []string {
	const prefix = "functions."
	var names []string
	seen := map[string]bool{}
	for _, m := range leakedToolCall.FindAllString(reply, -1) {
		if !strings.HasPrefix(m, prefix) {
			continue
		}
		name, _, ok := strings.Cut(strings.TrimPrefix(m, prefix), ":")
		if !ok || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}
