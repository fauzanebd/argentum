package actions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeStore answers FindByName from a fixed map, so a test can drive http_action
// without a database. A nil entry is a not-found.
type fakeStore struct {
	byName map[string]*domain.HTTPEndpoint
}

func (f *fakeStore) FindByName(_ context.Context, name string) (*domain.HTTPEndpoint, error) {
	ep, ok := f.byName[name]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return ep, nil
}

// fakeEgress records the request it was handed and returns a canned response, so a
// test asserts what http_action built without a network. failWith, when set, is
// returned instead — the shape an SSRF refusal takes.
type fakeEgress struct {
	gotMethod  string
	gotURL     string
	gotHeaders map[string]string
	gotBody    string
	status     int
	body       string
	failWith   error
}

func (f *fakeEgress) Do(_ context.Context, method, rawURL string, headers map[string]string, body []byte) (int, []byte, error) {
	f.gotMethod, f.gotURL, f.gotHeaders, f.gotBody = method, rawURL, headers, string(body)
	if f.failWith != nil {
		return 0, nil, f.failWith
	}
	status := f.status
	if status == 0 {
		status = 200
	}
	return status, []byte(f.body), nil
}

func httpParams(t *testing.T, endpoint string, values map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"endpoint": endpoint, "params": values})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func endpoint(name, method, urlTemplate, header, body string) *domain.HTTPEndpoint {
	return &domain.HTTPEndpoint{
		Name: name, Method: method, URLTemplate: urlTemplate, Header: header, BodyTemplate: body,
	}
}

func TestHTTPActionValidate(t *testing.T) {
	a := NewHTTPAction(&fakeStore{}, &fakeEgress{})
	if err := a.Validate(httpParams(t, "create_ticket", map[string]any{"id": 5})); err != nil {
		t.Fatalf("Validate(valid) = %v; want nil", err)
	}
	// An empty endpoint name is refused at propose time — before a human ever sees
	// the card.
	if err := a.Validate(httpParams(t, "", nil)); err == nil {
		t.Fatal("Validate(no endpoint) = nil; want error")
	}
}

func TestHTTPActionDescribeNamesEndpointAndParams(t *testing.T) {
	a := NewHTTPAction(&fakeStore{}, &fakeEgress{})
	got, err := a.Describe(httpParams(t, "create_ticket", map[string]any{"subject": "outage"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "create_ticket") || !strings.Contains(got, "subject=outage") {
		t.Fatalf("Describe = %q; want the endpoint name and the supplied params", got)
	}
}

func TestHTTPActionExecuteRendersAndCalls(t *testing.T) {
	eg := &fakeEgress{status: 201, body: `{"id":42}`}
	store := &fakeStore{byName: map[string]*domain.HTTPEndpoint{
		"create_ticket": endpoint("create_ticket", "post",
			"https://tickets.acme.com/v2/projects/{{.project}}/tickets",
			`{"Authorization":"Bearer secret","Content-Type":"application/json"}`,
			`{"subject":"{{.subject}}"}`),
	}}
	a := NewHTTPAction(store, eg)

	res, err := a.Execute(context.Background(),
		httpParams(t, "create_ticket", map[string]any{"project": "ops", "subject": "disk full"}))
	if err != nil {
		t.Fatalf("Execute = %v; want nil", err)
	}
	// The path placeholder was filled, the method upper-cased, the header and body
	// rendered — and the credential rode in the header, not the ledger.
	if eg.gotMethod != "POST" {
		t.Fatalf("method = %q; want POST", eg.gotMethod)
	}
	if eg.gotURL != "https://tickets.acme.com/v2/projects/ops/tickets" {
		t.Fatalf("url = %q; want the filled path", eg.gotURL)
	}
	if eg.gotHeaders["Authorization"] != "Bearer secret" {
		t.Fatalf("headers = %v; want the endpoint's Authorization", eg.gotHeaders)
	}
	if eg.gotBody != `{"subject":"disk full"}` {
		t.Fatalf("body = %q; want the rendered body", eg.gotBody)
	}
	if !strings.Contains(string(res), `"status":201`) || !strings.Contains(string(res), `response_body`) || !strings.Contains(string(res), `42`) {
		t.Fatalf("result = %s; want the status and response body", res)
	}
}

func TestHTTPActionExecuteUnknownEndpoint(t *testing.T) {
	a := NewHTTPAction(&fakeStore{byName: map[string]*domain.HTTPEndpoint{}}, &fakeEgress{})
	_, err := a.Execute(context.Background(), httpParams(t, "nope", nil))
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Execute(unknown) = %v; want ErrInvalidInput", err)
	}
}

func TestHTTPActionExecuteMissingPlaceholderRefused(t *testing.T) {
	store := &fakeStore{byName: map[string]*domain.HTTPEndpoint{
		"get_order": endpoint("get_order", "GET", "https://api.acme.com/orders/{{.id}}", "", ""),
	}}
	eg := &fakeEgress{}
	a := NewHTTPAction(store, eg)
	// The agent left out .id — a call with a literal "<no value>" in the path is
	// worse than no call, so missingkey=error refuses it and nothing is dialled.
	if _, err := a.Execute(context.Background(), httpParams(t, "get_order", map[string]any{})); err == nil {
		t.Fatal("Execute(missing placeholder) = nil; want error")
	}
	if eg.gotURL != "" {
		t.Fatalf("dialled %q despite a missing placeholder; want nothing", eg.gotURL)
	}
}

func TestHTTPActionExecuteRefusesTemplatedHost(t *testing.T) {
	// A host that is itself a placeholder is the host-swap this feature exists to
	// stop. Registration refuses it; Execute is the belt to that braces.
	store := &fakeStore{byName: map[string]*domain.HTTPEndpoint{
		"evil": endpoint("evil", "GET", "https://{{.host}}/x", "", ""),
	}}
	eg := &fakeEgress{}
	a := NewHTTPAction(store, eg)
	_, err := a.Execute(context.Background(), httpParams(t, "evil", map[string]any{"host": "attacker.example"}))
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Execute(templated host) = %v; want ErrInvalidInput", err)
	}
	if eg.gotURL != "" {
		t.Fatalf("dialled %q for a templated host; want nothing", eg.gotURL)
	}
}

func TestHTTPActionExecuteRefusesHostChange(t *testing.T) {
	// A path placeholder whose value smuggles a new authority — the rendered URL
	// parses to a different host than the registered one — is refused before egress.
	store := &fakeStore{byName: map[string]*domain.HTTPEndpoint{
		"proxy": endpoint("proxy", "GET", "https://api.acme.com{{.rest}}", "", ""),
	}}
	eg := &fakeEgress{}
	a := NewHTTPAction(store, eg)
	_, err := a.Execute(context.Background(),
		httpParams(t, "proxy", map[string]any{"rest": "@169.254.169.254/latest/meta-data"}))
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Execute(host change) = %v; want ErrInvalidInput", err)
	}
	if eg.gotURL != "" {
		t.Fatalf("dialled %q for a host change; want nothing", eg.gotURL)
	}
}

func TestHTTPActionExecutePropagatesEgressRefusal(t *testing.T) {
	// When the guard refuses the address (the 169.254 metadata endpoint reached via
	// a hostname), that refusal is the action's error and the call is a failure.
	store := &fakeStore{byName: map[string]*domain.HTTPEndpoint{
		"ping": endpoint("ping", "GET", "https://metadata.acme.com/ping", "", ""),
	}}
	eg := &fakeEgress{failWith: errors.New("egress blocked: 169.254.169.254 is a link-local address")}
	a := NewHTTPAction(store, eg)
	if _, err := a.Execute(context.Background(), httpParams(t, "ping", nil)); err == nil {
		t.Fatal("Execute(egress refused) = nil; want the guard's error")
	}
}

func TestHTTPActionExecuteNon2xxIsNotAnError(t *testing.T) {
	// A 404 from the far end is a recorded outcome, not an execution failure: the
	// call was made and the agent should see what came back.
	store := &fakeStore{byName: map[string]*domain.HTTPEndpoint{
		"get_order": endpoint("get_order", "GET", "https://api.acme.com/orders/{{.id}}", "", ""),
	}}
	eg := &fakeEgress{status: 404, body: `{"error":"not found"}`}
	a := NewHTTPAction(store, eg)
	res, err := a.Execute(context.Background(), httpParams(t, "get_order", map[string]any{"id": "9"}))
	if err != nil {
		t.Fatalf("Execute(404) = %v; want nil (a non-2xx is not an error)", err)
	}
	if !strings.Contains(string(res), `"status":404`) {
		t.Fatalf("result = %s; want status 404 recorded", res)
	}
}
