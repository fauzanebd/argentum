package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// fakeTool returns whatever it is told to, so the decorator can be tested
// against every shape a real tool produces without a database behind it.
type fakeTool struct {
	name   string
	result string
	err    error
	calls  int
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return "fake" }
func (f *fakeTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{}
}
func (f *fakeTool) Run(ctx context.Context, input string) (string, error) {
	return f.Execute(ctx, input)
}
func (f *fakeTool) Execute(context.Context, string) (string, error) {
	f.calls++
	return f.result, f.err
}

type fakeAuditor struct {
	mu   sync.Mutex
	rows []*domain.AgentAction
	err  error
}

func (a *fakeAuditor) Create(_ context.Context, act *domain.AgentAction) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.rows = append(a.rows, act)
	return nil
}

func (a *fakeAuditor) only(t *testing.T) *domain.AgentAction {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.rows) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1", len(a.rows))
	}
	return a.rows[0]
}

func turnCtx() context.Context {
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	ctx = tenantctx.WithThreadID(ctx, "th-1")
	ctx = tenantctx.WithMessageID(ctx, "msg-1")
	ctx = tenantctx.WithChannel(ctx, string(domain.ChannelWhatsApp))
	return tenantctx.WithActor(ctx, string(domain.ActorKindSchedule), "task-9")
}

// The ticket's first acceptance item: one row per call, success or failure,
// carrying the identity the turn ran under.
func TestAuditRecordsOneRowPerCall(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: `{"row_count":3,"truncated":false}`}, rec)

	if _, err := tool.Execute(turnCtx(), `{"sql":"SELECT 1","source_id":"src-7"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	row := rec.only(t)
	if row.ToolName != "run_sql" || row.CompanyID != "co-1" || row.ThreadID != "th-1" {
		t.Errorf("row = %+v, want run_sql on co-1/th-1", row)
	}
	if row.MessageID != "msg-1" {
		t.Errorf("message_id = %q, want msg-1", row.MessageID)
	}
	if row.ActorKind != domain.ActorKindSchedule || row.ActorRef != "task-9" {
		t.Errorf("actor = %s/%s, want schedule/task-9", row.ActorKind, row.ActorRef)
	}
	if row.Channel != domain.ChannelWhatsApp {
		t.Errorf("channel = %q, want whatsapp", row.Channel)
	}
	if row.SourceID != "src-7" {
		t.Errorf("source_id = %q, want src-7", row.SourceID)
	}
	if row.ResultStatus != domain.ActionStatusOK {
		t.Errorf("status = %q, want ok", row.ResultStatus)
	}
	if row.RowsReturned == nil || *row.RowsReturned != 3 {
		t.Errorf("rows_returned = %v, want 3", row.RowsReturned)
	}
}

// A support conversation about the public API starts with a request id and
// nothing else, so the row a request produced has to carry it (T-A1). The
// worker writes these rows in another process, minutes after the HTTP call
// returned, which is why this travels in the context rather than as an
// argument somebody could forget to pass.
func TestAuditRowCarriesTheOriginatingRequestID(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: `{"row_count":1}`}, rec)
	ctx := tenantctx.WithRequestID(turnCtx(), "req_0123456789abcdef")

	if _, err := tool.Execute(ctx, `{"sql":"SELECT 1"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := rec.only(t).RequestID; got != "req_0123456789abcdef" {
		t.Errorf("request_id = %q, want the id the HTTP caller was given", got)
	}
}

// T-S2's fifth acceptance item, on the decorator that writes the rows. "Which
// agent ran this query" is the question the roster exists to make answerable,
// and it is answerable only if every row carries the id.
func TestAuditRowCarriesTheAgentTheTurnRanAs(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: `{"row_count":1}`}, rec)
	ctx := agentscope.WithScope(turnCtx(), agentscope.Scope{AgentID: "ag-fin", Name: "Finance"})

	if _, err := tool.Execute(ctx, `{"sql":"SELECT 1"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := rec.only(t).AgentID; got != "ag-fin" {
		t.Errorf("agent_id = %q, want ag-fin", got)
	}
}

// A tool call made outside a chat turn — the schema-cache refresh, a reindex —
// belongs to no agent, and recording one would be a fabrication in the one
// table whose value is that it is not.
func TestAuditRowHasNoAgentWhenTheTurnRanUnscoped(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "get_schema", result: `{}`}, rec)

	if _, err := tool.Execute(turnCtx(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := rec.only(t).AgentID; got != "" {
		t.Errorf("agent_id = %q, want empty", got)
	}
}

// Most turns never had an HTTP request: a cron tick, a watcher, a channel
// webhook. Empty is the truth for those, not a gap to fill.
func TestAuditRowHasNoRequestIDWhenTheTurnDidNotStartWithOne(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: `{"row_count":1}`}, rec)

	if _, err := tool.Execute(turnCtx(), `{"sql":"SELECT 1"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := rec.only(t).RequestID; got != "" {
		t.Errorf("request_id = %q, want empty", got)
	}
}

func TestAuditStatusPerOutcome(t *testing.T) {
	cases := []struct {
		name   string
		result string
		err    error
		want   domain.ActionStatus
	}{
		{"success", `{"row_count":1}`, nil, domain.ActionStatusOK},
		{"failure", "", errors.New("query execution failed"), domain.ActionStatusError},
		{"truncated", `{"row_count":100,"truncated":true}`, nil, domain.ActionStatusTruncated},
		{
			// The refusal agentbudget.Guard returns when the turn is spent. It
			// comes back with a nil error, which is why the audit layer cannot
			// simply test err != nil.
			"budget refusal",
			`{"budget_exhausted":true,"reason":"tool-call budget spent (12 of 12)"}`,
			nil,
			domain.ActionStatusBlocked,
		},
		{"non-json result", "plain text", nil, domain.ActionStatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeAuditor{}
			tool := WithAudit(&fakeTool{name: "run_sql", result: tc.result, err: tc.err}, rec)
			_, _ = tool.Execute(turnCtx(), `{"sql":"SELECT 1"}`)

			row := rec.only(t)
			if row.ResultStatus != tc.want {
				t.Errorf("status = %q, want %q", row.ResultStatus, tc.want)
			}
			if tc.err != nil && row.ErrorText == "" {
				t.Error("error row carries no error_text")
			}
			if tc.err == nil && row.ErrorText != "" {
				t.Errorf("error_text = %q on a non-error row", row.ErrorText)
			}
		})
	}
}

// A blocked call is one the tool never saw. Recording it is the point; running
// it would be the bug.
func TestAuditBlockedCallStillRecordedWithoutRunning(t *testing.T) {
	rec := &fakeAuditor{}
	inner := &fakeTool{name: "run_sql", result: `{"row_count":1}`}
	guarded := agentbudget.Guard(inner)
	tool := WithAudit(guarded, rec)

	// A one-call budget: the second call is refused by the guard.
	ctx := agentbudget.WithTracker(turnCtx(), agentbudget.New(agentbudget.Budget{MaxToolCalls: 1}))
	if _, err := tool.Execute(ctx, `{"sql":"SELECT 1"}`); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if _, err := tool.Execute(ctx, `{"sql":"SELECT 2"}`); err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if inner.calls != 1 {
		t.Errorf("tool ran %d times, want 1 — the second call was supposed to be refused", inner.calls)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rows) != 2 {
		t.Fatalf("audit rows = %d, want 2 (the refused call is still a row)", len(rec.rows))
	}
	if rec.rows[0].ResultStatus != domain.ActionStatusOK {
		t.Errorf("first row = %q, want ok", rec.rows[0].ResultStatus)
	}
	if rec.rows[1].ResultStatus != domain.ActionStatusBlocked {
		t.Errorf("second row = %q, want blocked", rec.rows[1].ResultStatus)
	}
}

// AGENTS.md §2: never persist a decrypted DSN. The log keeps the SQL and
// drops the credential, whichever name it arrives under.
func TestAuditRedactsCredentials(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: "{}"}, rec)

	args := `{
		"sql": "SELECT total FROM sales WHERE region = 'APAC'",
		"dsn": "postgres://demo:demo@localhost:5433/demo_analytics",
		"note": "postgres://admin:hunter2@warehouse.internal:5432/prod",
		"keyword_form": "host=db user=admin password=hunter2 dbname=prod",
		"nested": {"api_key": "sk-live-abc123", "label": "APAC"},
		"list": ["mysql://root:toor@10.0.0.1/db", "plain"]
	}`
	if _, err := tool.Execute(turnCtx(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored := string(rec.only(t).ArgsRedacted)
	for _, secret := range []string{"hunter2", "toor", "sk-live-abc123", "demo:demo"} {
		if strings.Contains(stored, secret) {
			t.Errorf("args_redacted leaked %q: %s", secret, stored)
		}
	}
	// The SQL is the reason the log exists; redaction must not eat it.
	if !strings.Contains(stored, "SELECT total FROM sales") {
		t.Errorf("args_redacted dropped the SQL: %s", stored)
	}
	if !strings.Contains(stored, "APAC") {
		t.Errorf("args_redacted dropped an innocent value: %s", stored)
	}
}

func TestAuditHashesRawArgsNotRedactedOnes(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: "{}"}, rec)

	// Same shape, different secret: after redaction both store "[redacted]",
	// so a hash taken over the stored form would collide.
	_, _ = tool.Execute(turnCtx(), `{"dsn":"postgres://a:one@h/db"}`)
	_, _ = tool.Execute(turnCtx(), `{"dsn":"postgres://a:two@h/db"}`)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rows) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(rec.rows))
	}
	if rec.rows[0].ArgsHash == rec.rows[1].ArgsHash {
		t.Error("two different credentials produced the same args_hash")
	}
	if len(rec.rows[0].ArgsHash) != 64 {
		t.Errorf("args_hash = %q, want a sha256 hex digest", rec.rows[0].ArgsHash)
	}
}

// run_sql accepts a bare SQL string when the model's JSON is malformed. The
// audit row must still say what was run.
func TestAuditKeepsMalformedArguments(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: "{}"}, rec)

	if _, err := tool.Execute(turnCtx(), "SELECT count(*) FROM orders"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var stored map[string]interface{}
	if err := json.Unmarshal(rec.only(t).ArgsRedacted, &stored); err != nil {
		t.Fatalf("args_redacted is not valid JSON: %v", err)
	}
	if stored["_raw"] != "SELECT count(*) FROM orders" {
		t.Errorf("args_redacted = %v, want the raw string preserved", stored)
	}
}

// A result that carries no row count is not a result of zero rows.
func TestAuditRowsReturnedNilWithoutRowCount(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "create_visualization", result: `{"url":"http://mb/1"}`}, rec)
	_, _ = tool.Execute(turnCtx(), `{}`)
	if got := rec.only(t).RowsReturned; got != nil {
		t.Errorf("rows_returned = %v, want nil", *got)
	}
}

// The audit write is detached from the turn, because a turn killed by its own
// deadline is when the record matters most.
func TestAuditRecordsAfterContextCancelled(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: `{"row_count":1}`}, rec)

	ctx, cancel := context.WithCancel(turnCtx())
	cancel()
	_, _ = tool.Execute(ctx, `{"sql":"SELECT 1"}`)

	if row := rec.only(t); row.ToolName != "run_sql" {
		t.Errorf("row = %+v, want the call recorded despite cancellation", row)
	}
}

// An audit outage must not become a customer-visible outage.
func TestAuditFailureDoesNotFailTheCall(t *testing.T) {
	rec := &fakeAuditor{err: errors.New("control DB down")}
	tool := WithAudit(&fakeTool{name: "run_sql", result: "ok"}, rec)

	out, err := tool.Execute(turnCtx(), `{"sql":"SELECT 1"}`)
	if err != nil {
		t.Errorf("Execute returned %v; the tool succeeded and the audit did not", err)
	}
	if out != "ok" {
		t.Errorf("result = %q, want the tool's own output", out)
	}
}

// Without a tenant there is nothing to scope the row to. Dropping it is the
// safe branch; panicking on the agent's hot path is not.
func TestAuditSkipsRowWithoutTenant(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "run_sql", result: "ok"}, rec)

	if _, err := tool.Execute(context.Background(), `{"sql":"SELECT 1"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rows) != 0 {
		t.Errorf("audit rows = %d, want 0 without a tenant", len(rec.rows))
	}
}

// A registry with no recorder is the eval harness and cmd/api's schema tool;
// they must keep working, unwrapped.
func TestWithAuditAllNilRecorderIsIdentity(t *testing.T) {
	inner := &fakeTool{name: "run_sql"}
	list := WithAuditAll([]interfaces.Tool{inner}, nil)
	if len(list) != 1 || list[0] != interfaces.Tool(inner) {
		t.Errorf("WithAuditAll with a nil recorder rewrapped the registry")
	}
}

// Oversized arguments must not write an unbounded row, but the row itself is
// still evidence the call happened.
func TestAuditCapsOversizeArguments(t *testing.T) {
	rec := &fakeAuditor{}
	tool := WithAudit(&fakeTool{name: "generate_document", result: "{}"}, rec)

	big, err := json.Marshal(map[string]string{"spec": strings.Repeat("x", maxArgsBytes+1)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := tool.Execute(turnCtx(), string(big)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	row := rec.only(t)
	if len(row.ArgsRedacted) > maxArgsBytes {
		t.Errorf("args_redacted is %d bytes, want it capped at %d", len(row.ArgsRedacted), maxArgsBytes)
	}
	if !strings.Contains(string(row.ArgsRedacted), "_oversize_bytes") {
		t.Errorf("args_redacted = %s, want it to say why it is missing", row.ArgsRedacted)
	}
}
