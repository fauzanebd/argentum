package app

import (
	"encoding/json"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tools"
)

// What an answer keeps of the tools its turn ran (RIZ-95).
//
// Until RIZ-95 it kept nothing: `finish` wrote the reply with no tool calls on
// any path, so a finished answer showed no chips and the only record of the
// work was the digest row (T-Q6), which is memory the dashboard never draws.
// The record is built from the `tool_result` event alone, because that event
// carries the call's own arguments beside its result — the SDK pairs them, so
// nothing here has to.

// toolCallResultKeys is everything of a result the dashboard's tool chip reads
// (`apps/dashboard/src/features/chat/tool-call-card.tsx`: the SQL, the
// dashboard link, the scheduled task's id, name and schedule) plus `status`
// and `error`, which say how the call ended. A chip that starts reading a new
// key needs it added here, or it will see the key live and lose it on reload.
//
// A whitelist rather than the whole result, because the whole result of
// `run_sql` is its rows: a second copy of tenant data in every transcript, for
// a chip that never shows one.
var toolCallResultKeys = []string{
	"sql", "dashboard_url", "url", "task_id", "id", "name", "cron_expression", "status", "error",
}

const (
	// maxToolCallString bounds every string an answer keeps, in characters. A
	// generated SQL statement is a few hundred; this is there for the model
	// that pastes a document into an argument, not for a real query.
	maxToolCallString = 16_000
	// maxToolCallRecords bounds the list. The turn budget stops real turns far
	// short of it (`AGENT_MAX_TOOL_CALLS` defaults to 12, and refused calls
	// after it are records too); this is the ceiling for a budget raised by
	// hand.
	maxToolCallRecords = 50
)

// toolCallRecord is one call as the answer keeps it. argsJSON is the raw
// argument string the model sent; res is the parsed result, empty when the
// result was not JSON — a budget refusal, a tool that failed with a plain
// error, `load_skill`'s framed text. Such a call is still a record with its
// name and arguments: it ran, or was refused, and the count says so.
func toolCallRecord(name, argsJSON string, res map[string]interface{}) domain.ToolCallRecord {
	rec := domain.ToolCallRecord{Name: name}

	// The audit log's redaction, so the two stores of what a tool was asked
	// cannot disagree about what was sensitive. RedactJSON answers `{}` for
	// anything it could not read, which is right: it drops what it could not
	// inspect.
	var args map[string]interface{}
	if argsJSON != "" {
		_ = json.Unmarshal(tools.RedactJSON(json.RawMessage(argsJSON)), &args)
	}
	if capped, ok := capStrings(args).(map[string]interface{}); ok && len(capped) > 0 {
		rec.Arguments = capped
	}

	kept := map[string]interface{}{}
	for _, key := range toolCallResultKeys {
		switch v := res[key].(type) {
		case string:
			kept[key] = capStrings(v)
		case float64, bool, json.Number, int, int64:
			kept[key] = v
		}
	}
	if len(kept) > 0 {
		rec.Result = kept
	}
	return rec
}

// capStrings cuts every string in v to maxToolCallString characters, at any
// depth. Characters, not bytes: a cut through a multi-byte rune would end the
// string on a replacement character.
func capStrings(v interface{}) interface{} {
	switch t := v.(type) {
	case string:
		if r := []rune(t); len(r) > maxToolCallString {
			return string(r[:maxToolCallString])
		}
		return t
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = capStrings(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = capStrings(val)
		}
		return out
	default:
		return v
	}
}

// capToolCalls keeps the first maxToolCallRecords calls. The later ones are
// dropped rather than the earlier, because the start of a turn — the schema
// it read, the first query — is what a reader auditing the answer looks for;
// and the drop is logged, because a list that silently stops is a list that
// looks complete.
func capToolCalls(p queue.ChatRunPayload, calls domain.ToolCallList) domain.ToolCallList {
	if len(calls) <= maxToolCallRecords {
		return calls
	}
	logrus.WithFields(logrus.Fields{
		"company_id": p.CompanyID,
		"thread_id":  p.ThreadID,
		"message_id": p.UserMsgID,
		"kept":       maxToolCallRecords,
		"dropped":    len(calls) - maxToolCallRecords,
	}).Info("tool calls past the cap not saved on the answer")
	return calls[:maxToolCallRecords]
}
