package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"text/template"

	"github.com/fauzanebd/argentum/internal/domain"
)

// EndpointStore is the registered-endpoint lookup http_action depends on (T-12b).
// Declared here, where it is consumed, and implemented in internal/app where the
// repository and the DSN cipher both live. FindByName returns the endpoint with
// its header template already decrypted — the one moment a credential is in the
// clear is the moment a request is built from it.
//
// The company is resolved from ctx (tenantctx), exactly as every other tenant
// operation resolves it; the action never restates what the turn already set.
type EndpointStore interface {
	FindByName(ctx context.Context, name string) (*domain.HTTPEndpoint, error)
}

// EndpointLister is the read a *catalog* needs rather than a call: the names,
// with no URL, no method and above all no header template, since nothing about
// this read is building a request. Kept separate from EndpointStore and probed
// for at runtime so a store that predates it still satisfies the action.
type EndpointLister interface {
	ListNames(ctx context.Context) ([]string, error)
}

// Egress is the guarded outbound call. Its whole job is the SSRF property: it
// pins the resolved address against the same allowlist the MCP client uses,
// refuses redirects, and bounds the call by a timeout. Declared here so
// http_action is testable without a network and cannot forget to build the guard.
type Egress interface {
	// Do performs method against rawURL with the given headers and body and
	// returns the response status and a size-capped body. An address the guard
	// refuses — our metadata endpoint, loopback, an RFC1918 host a name resolved
	// to — is an error, not a response.
	Do(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) (status int, respBody []byte, err error)
}

// httpMethods are the verbs an endpoint may register. A method outside this set is
// refused at registration; the action re-checks at execute time because the row
// could predate a tightening of the set.
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// httpActionParams is what the agent proposes: a registered endpoint's name and
// the values that fill its templates. The agent never supplies a URL, a method or
// a header — those are the admin's, on the endpoint row.
type httpActionParams struct {
	Endpoint string         `json:"endpoint"`
	Params   map[string]any `json:"params"`
}

// HTTPAction calls one of a company's registered endpoints (T-12b): the generic
// authenticated outbound call that wires Argentum into a ticket queue, an ERP, an
// internal service. The safety property is that the agent picks a *name*. The
// method, the host and the credentials were set by an admin and are not on the
// surface the model can reach; a placeholder can fill a path segment or a query
// value, never the host it is sent to.
type HTTPAction struct {
	store  EndpointStore
	egress Egress
}

// NewHTTPAction wires the action to its endpoint store and egress guard. A nil
// dependency is a wiring error the constructor does not hide: an action that
// cannot look up an endpoint or cannot make a guarded call is not an action, so it
// panics at boot rather than accepting proposals it can never carry out.
func NewHTTPAction(store EndpointStore, egress Egress) *HTTPAction {
	if store == nil || egress == nil {
		panic("actions: http_action requires an endpoint store and an egress guard")
	}
	return &HTTPAction{store: store, egress: egress}
}

func (a *HTTPAction) Kind() string { return "http_action" }

func (a *HTTPAction) parse(params json.RawMessage) (httpActionParams, error) {
	var p httpActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return p, fmt.Errorf("invalid parameters: %w", err)
	}
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	if p.Endpoint == "" {
		return p, fmt.Errorf("endpoint is required — the name of a registered endpoint to call")
	}
	if p.Params == nil {
		p.Params = map[string]any{}
	}
	return p, nil
}

// Validate runs at propose time, before a proposal is stored, and can only see
// the parameters — the tenant is not on the context here (the Action interface is
// ctx-free by design so a malformed proposal never reaches a human). So it checks
// the shape: a named endpoint and a values object. Whether that endpoint exists,
// and whether its host survives the SSRF guard, is enforced in Execute where the
// company is known — a proposal for an unknown endpoint fails there, after
// approval, with a plain reason.
func (a *HTTPAction) Validate(params json.RawMessage) error {
	_, err := a.parse(params)
	return err
}

// Usage says what the agent supplies and — as importantly — what it does not.
// A model that thinks it may pass a URL will pass one, get a refusal, and try a
// different URL; the safety property is that only a *name* is on its surface, so
// the line says that outright.
func (a *HTTPAction) Usage() string {
	return `call one of the endpoints an admin registered for this workspace. ` +
		`params: {"endpoint": "<a registered endpoint name>", "params": {"<placeholder>": "<value>"}}. ` +
		`You choose the name only — the method, the host and the credentials are the admin's and cannot be set from here.`
}

// TurnOptions lists the endpoint names this company has registered, so a turn is
// told which names exist instead of guessing one. A store that cannot list is not
// an error the turn should fail on: the catalog degrades to the Usage line above,
// which is what a deployment on an older store also gets.
func (a *HTTPAction) TurnOptions(ctx context.Context) ([]string, error) {
	lister, ok := a.store.(EndpointLister)
	if !ok {
		return nil, nil
	}
	return lister.ListNames(ctx)
}

// Describe renders the approval sentence. It names the endpoint the admin
// registered — which the approver knows the meaning of, having registered it —
// and the values the agent supplied, so a human sees what will be filled in
// without the action having to resolve a URL it cannot yet see.
func (a *HTTPAction) Describe(params json.RawMessage) (string, error) {
	p, err := a.parse(params)
	if err != nil {
		return "", err
	}
	if len(p.Params) == 0 {
		return fmt.Sprintf("Call the registered HTTP endpoint %q", p.Endpoint), nil
	}
	keys := make([]string, 0, len(p.Params))
	for k := range p.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, preview(fmt.Sprint(p.Params[k]))))
	}
	return fmt.Sprintf("Call the registered HTTP endpoint %q with %s", p.Endpoint, strings.Join(pairs, ", ")), nil
}

// Execute performs the call, post-approval. Every rule the ticket names is here:
// the endpoint must be one the company registered, the rendered host must match
// the registered host (so a placeholder cannot redirect the call), the method must
// be a known verb, and the request goes out through the egress guard that pins the
// address and refuses redirects.
func (a *HTTPAction) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	p, err := a.parse(params)
	if err != nil {
		return nil, err
	}

	ep, err := a.store.FindByName(ctx, p.Endpoint)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, fmt.Errorf("%w: no HTTP endpoint named %q is registered for this workspace; an admin must register it first",
				domain.ErrInvalidInput, p.Endpoint)
		}
		return nil, fmt.Errorf("look up endpoint: %w", err)
	}

	method := strings.ToUpper(strings.TrimSpace(ep.Method))
	if !httpMethods[method] {
		return nil, fmt.Errorf("%w: endpoint %q has an unsupported method %q", domain.ErrInvalidInput, ep.Name, ep.Method)
	}

	rendered, err := renderTemplate("url", ep.URLTemplate, p.Params)
	if err != nil {
		return nil, fmt.Errorf("build request URL: %w", err)
	}
	// The host allowlist: the rendered URL must land on the exact scheme and host
	// the admin registered. A placeholder may fill a path segment or a query value,
	// but a value that changes the authority — an injected `@evil.com`, a scheme
	// downgrade — is refused here, before the egress guard is even asked. This is
	// the check that makes "the agent picks a name, never a URL" true even when the
	// name carries free-form values.
	if err := sameAuthority(ep.URLTemplate, rendered); err != nil {
		return nil, err
	}

	headers, err := renderHeaders(ep.Header, p.Params)
	if err != nil {
		return nil, err
	}

	var body []byte
	if strings.TrimSpace(ep.BodyTemplate) != "" {
		b, err := renderTemplate("body", ep.BodyTemplate, p.Params)
		if err != nil {
			return nil, fmt.Errorf("build request body: %w", err)
		}
		body = []byte(b)
	}

	status, respBody, err := a.egress.Do(ctx, method, rendered, headers, body)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", ep.Name, err)
	}

	out, _ := json.Marshal(map[string]any{
		"endpoint":      ep.Name,
		"status":        status,
		"response_body": string(respBody),
	})
	// A non-2xx is not an execution error: the call was made and the ledger should
	// record what came back. The agent, and the human reading the result, can see
	// a 4xx and its body — a failed action is one the guard refused or the network
	// dropped, not one the far end answered unhappily.
	return out, nil
}

// renderTemplate executes a text/template with missingkey=error, so a placeholder
// the agent did not fill is a refused call rather than a request with the literal
// string "<no value>" in it. The dot is the values map.
func renderTemplate(name, text string, values map[string]any) (string, error) {
	tpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("%q template is not valid: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, values); err != nil {
		return "", fmt.Errorf("%q template: %w", name, err)
	}
	return buf.String(), nil
}

// renderHeaders renders the (decrypted) header template — a JSON object of header
// name→value, values themselves templated — into a flat map. An empty template is
// no headers, not an error.
func renderHeaders(headerTemplate string, values map[string]any) (map[string]string, error) {
	if strings.TrimSpace(headerTemplate) == "" {
		return map[string]string{}, nil
	}
	rendered, err := renderTemplate("header", headerTemplate, values)
	if err != nil {
		return nil, fmt.Errorf("build request headers: %w", err)
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(rendered), &raw); err != nil {
		return nil, fmt.Errorf("%w: the endpoint's header template is not a JSON object of header name to value", domain.ErrInvalidInput)
	}
	return raw, nil
}

// sameAuthority refuses a rendered URL whose scheme or host differs from the
// template's. The template's authority is literal by registration, so parsing it
// yields the host the admin fixed; the rendered URL is parsed and compared. A
// template whose own authority is not literal (a `{{` before the path) is refused
// too — belt to the registration check's braces, in case a row predates it.
func sameAuthority(urlTemplate, rendered string) error {
	tu, err := url.Parse(strings.TrimSpace(urlTemplate))
	if err != nil {
		return fmt.Errorf("%w: the endpoint URL template is not a URL", domain.ErrInvalidInput)
	}
	if strings.Contains(tu.Scheme, "{{") || strings.Contains(tu.Host, "{{") {
		return fmt.Errorf("%w: the endpoint URL host must be fixed, not templated", domain.ErrInvalidInput)
	}
	ru, err := url.Parse(strings.TrimSpace(rendered))
	if err != nil {
		return fmt.Errorf("%w: the filled-in URL is not valid", domain.ErrInvalidInput)
	}
	if !strings.EqualFold(ru.Scheme, tu.Scheme) || !strings.EqualFold(ru.Host, tu.Host) {
		return fmt.Errorf("%w: this call would go to %s://%s, but the endpoint is registered for %s://%s; a parameter cannot change the host",
			domain.ErrInvalidInput, ru.Scheme, ru.Host, tu.Scheme, tu.Host)
	}
	return nil
}
