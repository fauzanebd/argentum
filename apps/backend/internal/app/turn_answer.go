package app

import "strings"

// answerBuffer collects a streaming turn's prose per tool-calling iteration
// rather than in one builder (T-Q11).
//
// **What it is for.** Asked how many transactions November 2024 held, the
// answer of record read:
//
//	There were 1,667 transactions in November 2024. There were 1,667
//	transactions in November 2024. There were 300 transactions in November 2024.
//
// 300 is what the turn's own run_sql returned; 1,667 is in no table. The turn
// carried two iterations, and the concatenation of its 44 delta events *was*
// the stored content — the model guessed a figure before calling the tool,
// wrote it again, then wrote the true one once the result came back, and every
// sentence was kept. Prose written before a tool result is a working note: the
// model that wrote it had not yet seen the result it went on to ask for.
//
// So the record is the last iteration that produced prose. The stream is not
// narrowed — a reader watching the model think still sees every delta, because
// this only decides what completeWith persists.
//
// **Order of arrival is not order of iteration.** agent-sdk-go can withhold
// intermediate content and replay it after the last iteration
// (`filterIntermediateContent` in pkg/llm/*/streaming.go), and its final
// synthesis call emits content tagged `final_call` with no iteration number at
// all. Picking "the last thing that arrived" would therefore store the
// narration on one path and nothing on the other, so the choice is made on the
// iteration number, with the final call — which is by construction the last
// word — winning outright.
type answerBuffer struct {
	// byIteration is prose keyed by the iteration that produced it. Key 0 is
	// content the provider did not tag, which is how every event looks on a
	// provider that stamps nothing.
	byIteration  map[int]*strings.Builder
	maxIteration int
	// tagged records whether any content event carried an iteration number. If
	// none did, this buffer behaves exactly as the single builder it replaced —
	// the fallback that keeps a provider this was never measured against from
	// having its replies emptied.
	tagged bool
	// final is the SDK's synthesis call, made after the iteration budget is
	// spent and without tools. It answers from everything the turn retrieved,
	// so it is the answer whenever it exists.
	final    strings.Builder
	hasFinal bool
}

func newAnswerBuffer() *answerBuffer {
	return &answerBuffer{byIteration: map[int]*strings.Builder{}}
}

// Write records one content event's prose, filed by the iteration its metadata
// names.
func (a *answerBuffer) Write(md map[string]interface{}, s string) {
	if s == "" {
		return
	}
	if isFinalCall(md) {
		a.hasFinal = true
		a.final.WriteString(s)
		return
	}
	n := iterationOf(md)
	if n > 0 {
		a.tagged = true
		if n > a.maxIteration {
			a.maxIteration = n
		}
	}
	b := a.byIteration[n]
	if b == nil {
		b = &strings.Builder{}
		a.byIteration[n] = b
	}
	b.WriteString(s)
}

// Replace drops everything and keeps s as the whole answer. The guardrail
// branch of runStream uses it: a refusal is the reply, not a part of one.
func (a *answerBuffer) Replace(s string) {
	*a = answerBuffer{byIteration: map[int]*strings.Builder{}}
	a.byIteration[0] = &strings.Builder{}
	a.byIteration[0].WriteString(s)
}

// String is what the turn stores.
//
// The walk skips iterations that produced only whitespace, because a blank
// iteration is not an answer — and it stops at nothing, so a turn whose every
// iteration was blank returns "" and reaches rescueEmptyReply exactly as it
// does today.
func (a *answerBuffer) String() string {
	if a.hasFinal && strings.TrimSpace(a.final.String()) != "" {
		return a.final.String()
	}
	if !a.tagged {
		// No provider stamped anything: one bucket, one answer, byte-identical
		// to the concatenation this replaced.
		if b := a.byIteration[0]; b != nil {
			return b.String()
		}
		return ""
	}
	for n := a.maxIteration; n >= 0; n-- {
		if b := a.byIteration[n]; b != nil && strings.TrimSpace(b.String()) != "" {
			return b.String()
		}
	}
	return ""
}

// Dropped is how much prose the turn wrote and this buffer did not keep, in
// bytes. Zero on the ordinary single-iteration turn; non-zero is worth a log
// line, because it is the only visible trace that a reply was narrowed.
func (a *answerBuffer) Dropped() int {
	kept := len(a.String())
	total := len(a.final.String())
	for _, b := range a.byIteration {
		total += len(b.String())
	}
	if total <= kept {
		return 0
	}
	return total - kept
}

// isFinalCall reads the flag the SDK puts on the synthesis call it makes once
// the iteration budget is spent (`final_call: true`, pkg/llm/openai/
// streaming.go). Those events carry no iteration number, so without this they
// would file under 0 and lose to every tagged iteration above them.
func isFinalCall(md map[string]interface{}) bool {
	if md == nil {
		return false
	}
	b, _ := md["final_call"].(bool)
	return b
}
