package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// ActionAuditor is the narrow contract the audit decorator needs: one write,
// no reads. Declared here rather than depending on
// domain.AgentActionRepository whole, because a tool wrapper that could also
// list the log is a wrapper that could be asked to filter it.
type ActionAuditor interface {
	Create(ctx context.Context, a *domain.AgentAction) error
}

// WithAudit wraps a tool so every execution leaves exactly one row in
// agent_actions (T-05, finding S-5).
//
// It decorates the tool interface instead of calling a recorder from inside
// each tool for the same reason agentbudget.Guard does: s.Tools is the whole
// registry, so a tool added next year is audited without its author knowing
// this package exists. Auditing inside each tool would be seven call sites and
// an eighth one somebody forgets — and the one that gets forgotten is the one
// an incident asks about.
//
// Wrap OUTSIDE agentbudget.Guard. A budget-refused call returns a refusal
// string and a nil error, so from in here it is indistinguishable from a
// successful run unless the guard has already had its say — and a call the
// agent tried to make and was refused is precisely what an auditor wants to
// see.
func WithAudit(t interfaces.Tool, rec ActionAuditor) interfaces.Tool {
	if rec == nil {
		return t
	}
	return &audited{Tool: t, rec: rec}
}

// WithAuditAll wraps every tool in the registry. A nil recorder leaves the
// registry untouched, so a process with no control-plane DB still runs.
func WithAuditAll(list []interfaces.Tool, rec ActionAuditor) []interfaces.Tool {
	if rec == nil {
		return list
	}
	out := make([]interfaces.Tool, len(list))
	for i, t := range list {
		out[i] = WithAudit(t, rec)
	}
	return out
}

type audited struct {
	interfaces.Tool
	rec ActionAuditor
}

func (a *audited) Run(ctx context.Context, input string) (string, error) {
	return a.Execute(ctx, input)
}

func (a *audited) Execute(ctx context.Context, args string) (string, error) {
	start := time.Now()
	out, err := a.Tool.Execute(ctx, args)
	a.record(ctx, a.Tool.Name(), args, out, err, time.Since(start))
	return out, err
}

// maxArgsBytes bounds one row's stored arguments. The full SQL text is kept
// deliberately — it is the point of the log — and no query approaches this;
// what does is a generate_document spec carrying an embedded table. Past the
// cap the row records that it happened and how big it was, because a row
// missing its arguments is still evidence the call was made.
const maxArgsBytes = 64 * 1024

// maxErrorBytes bounds error_text. A driver error can carry a whole failed
// statement back; the log already has the arguments.
const maxErrorBytes = 4 * 1024

// record writes the audit row. It never fails the tool call: a tool that
// errored because its audit row could not be written would turn a logging
// outage into a customer-visible outage, and the reply the agent gives is more
// important than the record of it. Failures are logged at Warn with the tool
// name so the gap is visible in the same place the incident is.
func (a *audited) record(ctx context.Context, tool, args, out string, execErr error, took time.Duration) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		// Every path that reaches a registered tool sets the tenant at the top
		// of the turn. Reaching here means one does not, and the audit gap is
		// the smaller half of that bug.
		logrus.WithField("tool", tool).Warn("agent action not audited: no tenant in context")
		return
	}

	kind, ref := tenantctx.Actor(ctx)
	if kind == "" {
		kind = string(domain.ActorKindUser)
	}

	status, errText := classify(out, execErr)
	redacted, rows, sourceID := digestArgs(args, out)

	action := &domain.AgentAction{
		CompanyID: companyID,
		ThreadID:  tenantctx.ThreadID(ctx),
		MessageID: tenantctx.MessageID(ctx),
		ActorKind: domain.ActorKind(kind),
		ActorRef:  ref,
		Channel:   domain.Channel(tenantctx.Channel(ctx)),
		// Which of the tenant's agents made this call (T-S2). Off the context
		// rather than a constructor argument: this decorator wraps the whole
		// registry once at boot, and the agent is per turn.
		AgentID:      agentscope.AgentID(ctx),
		ToolName:     tool,
		SourceID:     sourceID,
		ArgsRedacted: redacted,
		ArgsHash:     hashArgs(args),
		ResultStatus: status,
		ErrorText:    errText,
		RowsReturned: rows,
		DurationMS:   int(took.Milliseconds()),
		RequestID:    tenantctx.RequestID(ctx),
	}

	// Detached from the turn's context on purpose: a turn cancelled by its
	// deadline is exactly when the record of what it managed to run matters,
	// and inheriting the cancellation would drop those rows first.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := a.rec.Create(writeCtx, action); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID,
			"thread_id":  action.ThreadID,
			"tool":       tool,
		}).Warn("agent action audit write failed; the call itself succeeded")
	}
}

// classify maps a tool's return into a status. The ordering matters: a refused
// call never ran, so it is blocked regardless of what its (synthetic) payload
// looks like.
func classify(out string, execErr error) (domain.ActionStatus, string) {
	if execErr != nil {
		return domain.ActionStatusError, truncate(execErr.Error(), maxErrorBytes)
	}
	if agentbudget.IsRefusal(out) {
		return domain.ActionStatusBlocked, ""
	}
	if resultTruncated(out) {
		return domain.ActionStatusTruncated, ""
	}
	return domain.ActionStatusOK, ""
}

// digestArgs produces the three things the row derives from a call's inputs
// and outputs: the redacted argument object, the row count the result carried
// (nil when it carried none), and which data source was addressed.
func digestArgs(args, out string) (redacted []byte, rows *int, sourceID string) {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil || parsed == nil {
		// run_sql accepts a bare SQL string when the model's JSON is malformed
		// (see NewRunSQLTool), so the audit log has to accept it too rather
		// than record the call as argument-less.
		parsed = map[string]interface{}{"_raw": args}
	}
	if s, ok := parsed["source_id"].(string); ok {
		sourceID = s
	}
	return marshalRedacted(parsed), resultRows(out), sourceID
}

func marshalRedacted(parsed map[string]interface{}) []byte {
	out, err := json.Marshal(redactValue(parsed))
	if err != nil {
		return []byte(`{"_unserializable":true}`)
	}
	if len(out) > maxArgsBytes {
		oversize, _ := json.Marshal(map[string]interface{}{"_oversize_bytes": len(out)})
		return oversize
	}
	return out
}

// sensitiveKey matches argument names whose value is a credential by
// definition. No tool takes one today — tools address a source by id and the
// DSN never leaves the resolver — so this is the guard against the tool that
// does, not against the seven that exist.
var sensitiveKey = regexp.MustCompile(`(?i)(dsn|password|passwd|secret|token|api_?key|credential|private_?key|authorization)`)

// credentialValue matches the two shapes a DSN takes: a URI carrying
// user:password@host, and the keyword form with a password= field. Matching on
// the value rather than only on the key is what catches a credential passed
// under an innocent name.
var credentialValue = regexp.MustCompile(`(?i)([a-z][a-z0-9+.\-]*://[^\s/@:]+:[^\s/@]+@)|((^|[\s;])password\s*=)`)

const redactedMarker = "[redacted]"

// redactValue walks an argument tree and removes anything credential-shaped.
// It errs toward removing too much: a SELECT whose text happens to contain
// "password=" is dropped whole, because AGENTS.md §2 forbids persisting a
// decrypted DSN and a lost query is recoverable from the thread.
func redactValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if sensitiveKey.MatchString(k) {
				out[k] = redactedMarker
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = redactValue(val)
		}
		return out
	case string:
		if credentialValue.MatchString(t) {
			return redactedMarker
		}
		return t
	default:
		return v
	}
}

// hashArgs fingerprints the arguments as the tool received them — before
// redaction, so two calls that differ only inside a redacted field do not
// collide, and a repeated call is recognisable even when its stored form shows
// nothing.
func hashArgs(args string) string {
	sum := sha256.Sum256([]byte(args))
	return hex.EncodeToString(sum[:])
}

// resultRows reads row_count off a tool result. Nil when the result is not
// JSON or has no row_count: most tools return no rows at all, and recording
// that as zero would make an empty query and a dashboard link look alike.
func resultRows(out string) *int {
	var payload struct {
		RowCount *int `json:"row_count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil
	}
	return payload.RowCount
}

func resultTruncated(out string) bool {
	var payload struct {
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return false
	}
	return payload.Truncated
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + "…"
}
