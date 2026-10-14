package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// RIZ-95: an answer keeps the tools its turn ran.
//
// Before this no answer kept any — `finish` wrote every reply with NULL
// `tool_calls` — so the dashboard's chips vanished the moment the answer
// arrived. These pin the path end to end on this side of the database: the
// stream's result events become records, in order, refusals included, and the
// records reach the row.

func TestAToolCallRecordKeepsWhatTheChipReadsAndNothingSensitive(t *testing.T) {
	long := strings.Repeat("é", 20_000) // multi-byte, so a byte cut would show
	rec := toolCallRecord("run_sql",
		fmt.Sprintf(`{"password":"x","sql":%q,"source_id":"src-1"}`, long),
		map[string]interface{}{
			"rows":      []interface{}{map[string]interface{}{"region": "Jakarta"}},
			"row_count": float64(1),
			"url":       "https://metabase.example/dashboard/7",
			"status":    "ok",
		})

	if rec.Name != "run_sql" {
		t.Errorf("name = %q", rec.Name)
	}
	if got := rec.Arguments["password"]; got != "[redacted]" {
		t.Errorf("password = %v, want the audit log's redaction", got)
	}
	sql, _ := rec.Arguments["sql"].(string)
	if n := len([]rune(sql)); n != maxToolCallString {
		t.Errorf("sql kept %d characters, want %d", n, maxToolCallString)
	}
	if !strings.HasSuffix(sql, "é") {
		t.Error("sql was cut through a character")
	}
	if rec.Arguments["source_id"] != "src-1" {
		t.Errorf("source_id = %v", rec.Arguments["source_id"])
	}
	if _, ok := rec.Result["rows"]; ok {
		t.Error("the result kept its rows; a transcript is not a second copy of tenant data")
	}
	if _, ok := rec.Result["row_count"]; ok {
		t.Error("the result kept row_count, which no chip reads")
	}
	if rec.Result["url"] != "https://metabase.example/dashboard/7" || rec.Result["status"] != "ok" {
		t.Errorf("result = %v, want url and status kept", rec.Result)
	}
}

func TestAToolCallRecordOfARefusalIsStillARecord(t *testing.T) {
	rec := toolCallRecord("run_sql", `not json`, nil)
	if rec.Name != "run_sql" || rec.Arguments != nil || rec.Result != nil {
		t.Errorf("record = %#v, want the name alone", rec)
	}
}

// refusal is the sentence the budget guard hands back in place of a result,
// produced by the real tracker rather than copied, so a reworded refusal
// cannot leave this test checking a string nothing sends.
func refusal(t *testing.T) string {
	t.Helper()
	tr := agentbudget.New(agentbudget.Budget{MaxToolCalls: 1})
	ctx := context.Background()
	if _, blocked := tr.Begin(ctx, "run_sql"); blocked {
		t.Fatal("the first call was refused")
	}
	text, blocked := tr.Begin(ctx, "run_sql")
	if !blocked || text == "" {
		t.Fatal("the second call was not refused")
	}
	return text
}

func toolEvent(typ interfaces.AgentEventType, name, args, result string) interfaces.AgentStreamEvent {
	return interfaces.AgentStreamEvent{
		Type:     typ,
		ToolCall: &interfaces.ToolCallEvent{Name: name, Arguments: args, Result: result},
	}
}

func streamOf(evts ...interfaces.AgentStreamEvent) <-chan interfaces.AgentStreamEvent {
	ch := make(chan interfaces.AgentStreamEvent, len(evts))
	for _, e := range evts {
		ch <- e
	}
	close(ch)
	return ch
}

// consume runs the stream the way runStream does and completes the turn the
// way Run does, returning the row that was written.
func consume(t *testing.T, evts ...interfaces.AgentStreamEvent) domain.Message {
	t.Helper()
	r, msgs, _ := attributionRunner(t)
	p := queue.ChatRunPayload{CompanyID: "co-1", ThreadID: "th-1", UserMsgID: "um-1", Channel: domain.ChannelDashboard}

	var numbers []float64
	var doc string
	var calls domain.ToolCallList
	r.consumeStream(context.Background(), p, streamOf(evts...), &numbers, &doc, &calls)
	r.completeWith(context.Background(), p, "Sales rose 6%.", 0, 0, 0, nil, doc, nil, calls...)

	if len(msgs.appended) != 1 {
		t.Fatalf("appended %d messages, want the answer", len(msgs.appended))
	}
	return msgs.appended[0]
}

func TestTheAnswerKeepsEveryCallInOrderRefusalsIncluded(t *testing.T) {
	msg := consume(t,
		toolEvent(interfaces.AgentEventToolCall, "get_schema", `{}`, ""),
		toolEvent(interfaces.AgentEventToolResult, "get_schema", `{}`, `{"tables":["orders"]}`),
		toolEvent(interfaces.AgentEventToolCall, "run_sql", `{"sql":"SELECT 1"}`, ""),
		toolEvent(interfaces.AgentEventToolResult, "run_sql", `{"sql":"SELECT 1"}`, `{"row_count":1,"rows":[{"n":1}]}`),
		// The turn ran out of budget here: the guard answers with a sentence.
		toolEvent(interfaces.AgentEventToolCall, "run_sql", `{"sql":"SELECT 2"}`, ""),
		toolEvent(interfaces.AgentEventToolResult, "run_sql", `{"sql":"SELECT 2"}`, refusal(t)),
		toolEvent(interfaces.AgentEventToolResult, "compute", `{"expression":"a/b"}`, `{"value":0.5}`),
	)

	got := msg.ToolCalls
	if len(got) != 4 {
		t.Fatalf("saved %d calls, want 4 — one per result, not one per event: %#v", len(got), got)
	}
	for i, want := range []string{"get_schema", "run_sql", "run_sql", "compute"} {
		if got[i].Name != want {
			t.Errorf("call %d = %s, want %s", i, got[i].Name, want)
		}
	}
	if got[1].Arguments["sql"] != "SELECT 1" || got[2].Arguments["sql"] != "SELECT 2" {
		t.Errorf("arguments = %v, %v; each call keeps its own statement", got[1].Arguments, got[2].Arguments)
	}
	// The refused call counts, with no result: a sentence is not a payload.
	if got[2].Result != nil {
		t.Errorf("refused call result = %v, want none", got[2].Result)
	}
	if got[1].Result != nil {
		t.Errorf("run_sql result = %v, want nothing the chip reads", got[1].Result)
	}
}

func TestAnAnswerWithNoToolsIsWrittenAsBefore(t *testing.T) {
	msg := consume(t, interfaces.AgentStreamEvent{Type: interfaces.AgentEventContent, Content: "Hello."})
	if msg.ToolCalls != nil {
		t.Errorf("tool_calls = %#v, want nil so the column stays NULL", msg.ToolCalls)
	}

	// And every other ending, which passes nothing at all.
	r, msgs, _ := attributionRunner(t)
	r.completeWith(context.Background(), queue.ChatRunPayload{ThreadID: "th-1", UserMsgID: "um-2"},
		"hello", 0, 0, 0, nil, "", nil)
	if msgs.appended[0].ToolCalls != nil {
		t.Errorf("tool_calls = %#v, want nil", msgs.appended[0].ToolCalls)
	}
}

func TestAnAnswerKeepsTheFirstFiftyCallsAndSaysItDroppedTheRest(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()

	var evts []interfaces.AgentStreamEvent
	for i := range 60 {
		evts = append(evts, toolEvent(interfaces.AgentEventToolResult, "run_sql",
			fmt.Sprintf(`{"sql":"SELECT %d"}`, i), `{"row_count":1}`))
	}
	msg := consume(t, evts...)

	if len(msg.ToolCalls) != maxToolCallRecords {
		t.Fatalf("saved %d calls, want %d", len(msg.ToolCalls), maxToolCallRecords)
	}
	if first, last := msg.ToolCalls[0].Arguments["sql"], msg.ToolCalls[49].Arguments["sql"]; first != "SELECT 0" || last != "SELECT 49" {
		t.Errorf("kept %v … %v, want the first fifty", first, last)
	}
	var logged bool
	for _, e := range hook.AllEntries() {
		if strings.Contains(e.Message, "tool calls past the cap") {
			logged = true
			if e.Level != logrus.InfoLevel || e.Data["dropped"] != 10 || e.Data["company_id"] != "co-1" {
				t.Errorf("log = %s %v, want Info with dropped=10 and the company", e.Level, e.Data)
			}
		}
	}
	if !logged {
		t.Error("ten calls were dropped silently")
	}
}
