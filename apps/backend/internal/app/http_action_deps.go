package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// The turn-time dependencies http_action runs on (T-12b): where a registered
// endpoint is read from, and how the request actually leaves the process. Both
// are wired in bootstrap and handed to actions.NewHTTPAction; the action package
// declares the interfaces, this file satisfies them where the repository, the
// cipher and the egress guard all live.

// defaultHTTPActionMaxResponseBytes caps what a called endpoint's response can add
// to the invocation ledger and to the agent's context. A tenant's own system is
// not hostile, but a runaway export is a runaway prompt; 64 KiB is enough for a
// created-ticket id and an error body, and finite.
const defaultHTTPActionMaxResponseBytes int64 = 64 * 1024

// HTTPEndpointCipher opens the sealed header template at turn time. Satisfied by
// *crypto.DSNCipher — the same envelope every other tenant secret uses.
type HTTPEndpointCipher interface {
	Decrypt(blob []byte) (string, error)
}

// httpEndpointResolver reads a company's registered endpoint by name and decrypts
// its header template for the one moment a request is built from it. It resolves
// the company from ctx, exactly as the action's contract promises, so nothing
// upstream has to restate the tenant. Satisfies actions.EndpointStore.
type httpEndpointResolver struct {
	repo   domain.HTTPEndpointRepository
	cipher HTTPEndpointCipher
}

// NewHTTPEndpointResolver builds the turn-time endpoint store for http_action.
func NewHTTPEndpointResolver(repo domain.HTTPEndpointRepository, cipher HTTPEndpointCipher) actions.EndpointStore {
	if repo == nil || cipher == nil {
		panic("app: http endpoint resolver requires a repository and a cipher")
	}
	return &httpEndpointResolver{repo: repo, cipher: cipher}
}

func (r *httpEndpointResolver) FindByName(ctx context.Context, name string) (*domain.HTTPEndpoint, error) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return nil, fmt.Errorf("no tenant in context")
	}
	ep, err := r.repo.GetByName(ctx, companyID, name)
	if err != nil {
		return nil, err
	}
	if len(ep.HeaderEncrypted) > 0 {
		h, err := r.cipher.Decrypt(ep.HeaderEncrypted)
		if err != nil {
			// A header that cannot be opened is a call that cannot be authorized;
			// refuse it rather than send an unauthenticated request to the tenant's
			// system. The key rotated, or the row is corrupt — either way an admin
			// re-registers.
			return nil, fmt.Errorf("decrypt endpoint headers: %w", err)
		}
		ep.Header = h
	}
	return ep, nil
}

// guardEgress makes http_action's outbound call through the SSRF guard shared with
// the MCP client. The guard pins the resolved address and refuses redirects; this
// adapter adds the endpoint's own headers, bounds the response, and turns the
// exchange into the (status, body) the action records. Satisfies actions.Egress.
type guardEgress struct {
	guard            mcp.Guard
	client           *http.Client
	maxResponseBytes int64
}

// NewHTTPActionEgress builds the guarded egress for http_action. The client is
// built once — the guard's StrictClient carries the address-pinned transport — and
// reused, because a new transport per call would leak connections under load.
func NewHTTPActionEgress(guard mcp.Guard, maxResponseBytes int64) actions.Egress {
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultHTTPActionMaxResponseBytes
	}
	return &guardEgress{guard: guard, client: guard.StrictClient(), maxResponseBytes: maxResponseBytes}
}

func (e *guardEgress) Do(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) (int, []byte, error) {
	// The string check first — it can reject a literal 169.254.169.254 or a
	// non-https URL before any I/O. The dial-time Control on the transport is what
	// catches a hostname that resolves privately, so the two together leave no gap.
	if err := e.guard.CheckURL(rawURL); err != nil {
		return 0, nil, err
	}
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	capped, err := io.ReadAll(io.LimitReader(resp.Body, e.maxResponseBytes))
	if err != nil {
		return resp.StatusCode, capped, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, capped, nil
}
