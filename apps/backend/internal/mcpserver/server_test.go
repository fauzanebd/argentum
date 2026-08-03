package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// T-14: the adaptation, and the two things it must not get wrong — a tool
// reached without the scope it needs, and a tool exposed that was never meant
// to be.

type stubTool struct {
	name    string
	desc    string
	params  map[string]interfaces.ParameterSpec
	gotArgs string
	gotCtx  context.Context
	out     string
	err     error
	calls   int
}

func (t *stubTool) Name() string                                    { return t.name }
func (t *stubTool) Description() string                             { return t.desc }
func (t *stubTool) Parameters() map[string]interfaces.ParameterSpec { return t.params }
func (t *stubTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *stubTool) Execute(ctx context.Context, input string) (string, error) {
	t.calls++
	t.gotArgs, t.gotCtx = input, ctx
	return t.out, t.err
}

func call(t *testing.T, tool interfaces.Tool, scope domain.Scope, held []domain.Scope, args string) *sdk.CallToolResult {
	t.Helper()
	ctx := WithScopes(context.Background(), held)
	req := &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{
		Name: tool.Name(), Arguments: json.RawMessage(args),
	}}
	res, err := handlerFor(tool, scope)(ctx, req)
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	return res
}

func text(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result carries no content")
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}
	return tc.Text
}

// The acceptance criterion, stated as a test: a read:metrics-only key cannot
// run_sql.
func TestAToolIsRefusedWithoutItsScope(t *testing.T) {
	tool := &stubTool{name: "run_sql", out: "rows"}

	res := call(t, tool, domain.ScopeReadData, []domain.Scope{domain.ScopeReadMetrics}, `{"query":"select 1"}`)

	if !res.IsError {
		t.Fatal("a key without read:data ran run_sql")
	}
	if tool.calls != 0 {
		t.Errorf("the tool ran %d times for an unscoped caller", tool.calls)
	}
	// The message names the scope, because the operator reading it in their
	// client is the person who can add it.
	if got := text(t, res); !strings.Contains(got, "read:data") {
		t.Errorf("refusal = %q, want it to name the missing scope", got)
	}
}

func TestAToolRunsWithItsScope(t *testing.T) {
	tool := &stubTool{name: "run_sql", out: "1 row"}

	res := call(t, tool, domain.ScopeReadData,
		[]domain.Scope{domain.ScopeReadMetrics, domain.ScopeReadData}, `{"query":"select 1"}`)

	if res.IsError {
		t.Fatalf("a scoped call was refused: %s", text(t, res))
	}
	if tool.calls != 1 {
		t.Fatalf("tool ran %d times, want 1", tool.calls)
	}
	// The arguments go through untouched: the tool's own parser is the one the
	// agent's calls use, and a second parser here would be a second thing to
	// disagree.
	if tool.gotArgs != `{"query":"select 1"}` {
		t.Errorf("arguments = %q, want them passed through verbatim", tool.gotArgs)
	}
	if got := text(t, res); got != "1 row" {
		t.Errorf("result = %q", got)
	}
}

// A tool that refused — a guardrail, a spent budget, a bad query — is an answer
// the client's agent can act on, not a protocol failure that kills the session.
func TestAToolErrorIsAResultNotATransportError(t *testing.T) {
	tool := &stubTool{name: "run_sql", err: errors.New("only SELECT queries are supported")}

	res := call(t, tool, domain.ScopeReadData, []domain.Scope{domain.ScopeReadData}, `{}`)

	if !res.IsError {
		t.Fatal("a failed tool call was reported as a success")
	}
	if got := text(t, res); !strings.Contains(got, "only SELECT") {
		t.Errorf("result = %q, want the tool's own reason", got)
	}
}

// An empty MCP argument object reaches the tool as `{}` rather than as an empty
// string, because that is what the tools' parsers accept.
func TestNoArgumentsBecomesAnEmptyObject(t *testing.T) {
	tool := &stubTool{name: "list_sources", out: "[]"}

	call(t, tool, domain.ScopeReadData, []domain.Scope{domain.ScopeReadData}, "")

	if tool.gotArgs != "{}" {
		t.Errorf("arguments = %q, want an empty object", tool.gotArgs)
	}
}

// The surface is a deliberate list. `generate_document`, `schedule_task` and
// `propose_action` each spend money or change something outside Argentum, and
// an MCP client is an agent we did not write, running without our system prompt.
func TestTheSurfaceExcludesEverythingThatWrites(t *testing.T) {
	for _, name := range []string{"generate_document", "schedule_task", "propose_action", "create_agent"} {
		if _, ok := ScopeFor(name); ok {
			t.Errorf("%q is exposed over MCP; the surface is reads plus the two Metabase writes", name)
		}
	}
	for _, name := range []string{"run_sql", "get_schema", "list_sources", "list_metrics", "query_metric"} {
		if _, ok := ScopeFor(name); !ok {
			t.Errorf("%q is not exposed; the ticket names it", name)
		}
	}
}

// New builds a server over whatever the registry holds and silently skips what
// is not on the surface — which is also how a deployment without Metabase
// behaves, since the tool is simply not in the registry.
func TestNewExposesOnlyTheSurface(t *testing.T) {
	srv := New([]interfaces.Tool{
		&stubTool{name: "run_sql"},
		&stubTool{name: "generate_document"},
		&stubTool{name: "query_metric"},
	})
	if srv == nil {
		t.Fatal("New returned nil")
	}
	// The SDK has no exported tool listing on the server value, so the assertion
	// that matters — that generate_document is absent — is ScopeFor's above.
	// This case exists to prove New does not panic on a registry holding tools
	// it will not expose, which is the ordinary production shape.
}

func TestSchemaForRendersAnObjectSchema(t *testing.T) {
	raw := schemaFor(map[string]interfaces.ParameterSpec{
		"query":  {Type: "string", Description: "The SQL to run", Required: true},
		"limit":  {Type: "integer", Default: 100},
		"format": {Type: "string", Enum: []any{"json", "csv"}},
	})

	var schema struct {
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema does not parse: %v (%s)", err, raw)
	}
	if schema.Type != "object" {
		t.Errorf("type = %q", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Errorf("required = %v, want [query]", schema.Required)
	}
	if schema.Properties["query"]["description"] != "The SQL to run" {
		t.Errorf("query property = %v", schema.Properties["query"])
	}
	if schema.Properties["limit"]["default"] != float64(100) {
		t.Errorf("limit default = %v", schema.Properties["limit"]["default"])
	}
	if len(schema.Properties["format"]["enum"].([]any)) != 2 {
		t.Errorf("format enum = %v", schema.Properties["format"]["enum"])
	}
}

// --- the HTTP half ---

type stubAuth struct {
	key *domain.APIKey
	err error
	got string
}

func (a *stubAuth) Authenticate(_ context.Context, token string) (*domain.APIKey, error) {
	a.got = token
	if a.err != nil {
		return nil, a.err
	}
	return a.key, nil
}

func TestNoKeyIsRefusedBeforeASessionExists(t *testing.T) {
	auth := &stubAuth{err: errors.New("no such key")}
	h := Handler(nil, auth)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if auth.got != "" {
		t.Error("a request with no Authorization header still consulted the key store")
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("no WWW-Authenticate header; a client cannot tell it needs a credential")
	}
}

// One message for unknown, revoked and expired alike: a caller who learns which
// of the three they hit is a caller probing key space with feedback.
func TestEveryAuthFailureReadsTheSame(t *testing.T) {
	h := Handler(nil, &stubAuth{err: errors.New("key is revoked")})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer ak_live_whatever")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if strings.Contains(w.Body.String(), "revoked") {
		t.Errorf("the body distinguishes the failure: %s", w.Body.String())
	}
}

// The context an authenticated request builds is the one the audit decorator
// reads, which is how every MCP call writes an `agent_actions` row attributed
// to the key without this package writing one.
func TestAnAuthenticatedRequestCarriesTheTenant(t *testing.T) {
	auth := &stubAuth{key: &domain.APIKey{
		ID: "key-1", CompanyID: "co-1", Scopes: []domain.Scope{domain.ScopeReadData},
	}}

	var seen context.Context
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Context() })
	authenticated(inner, auth).ServeHTTP(
		httptest.NewRecorder(),
		withBearer(httptest.NewRequest(http.MethodPost, "/", nil), "ak_live_whatever"),
	)

	if seen == nil {
		t.Fatal("the request never reached the inner handler")
	}
	if got := tenantctx.CompanyID(seen); got != "co-1" {
		t.Errorf("company = %q", got)
	}
	kind, ref := tenantctx.Actor(seen)
	if kind != string(domain.ActorKindAPIKey) || ref != "key-1" {
		t.Errorf("actor = (%q, %q), want the api key", kind, ref)
	}
	if got := tenantctx.Channel(seen); got != string(domain.ChannelAPI) {
		t.Errorf("channel = %q, want api", got)
	}
	if got := scopesFrom(seen); len(got) != 1 || got[0] != domain.ScopeReadData {
		t.Errorf("scopes = %v", got)
	}
}

func withBearer(r *http.Request, token string) *http.Request {
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// /health answers without a key: a probe is not a caller.
func TestHealthNeedsNoKey(t *testing.T) {
	h := Handler(nil, &stubAuth{err: errors.New("no such key")})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
