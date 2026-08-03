package app

import (
	"context"
	"errors"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	mcptools "github.com/fauzanebd/argentum/internal/tools/mcp"
)

// errNoMCPCipher is a wiring state, not a tenant error: a server with a stored
// token, in a process built without the cipher to read it. Surfaced as a failed
// execution with a reason rather than as a call made without the token.
var errNoMCPCipher = errors.New("no cipher configured to read the stored MCP token")

// MCPCallStore resolves the write tool a proposal names, for the company on the
// context (T-M4). It is what `actions.MCPCall` calls at approval time, and it
// lives here for the same reason http_action's endpoint store does: the
// repository and the cipher are both this layer's.
//
// It answers the same question `mcptools.Source` answers at turn time, with two
// differences that matter. There is no agent scope — a human is approving, not
// an agent proposing, and the binding that decided which servers that agent
// could see is not a fact about whether this call may run. And it resolves by
// the namespaced name rather than building a list, because the proposal recorded
// a name and that name has to mean the same tool a day later.
type MCPCallStore struct {
	repo   domain.MCPServerRepository
	cipher MCPCipher
}

// NewMCPCallStore wires the store. A nil cipher is legal and means a server with
// a stored token is unreachable — the same stance mcptools.Source takes, and for
// the same reason: calling a gated server unauthenticated is worse than not
// calling it.
func NewMCPCallStore(repo domain.MCPServerRepository, cipher MCPCipher) *MCPCallStore {
	return &MCPCallStore{repo: repo, cipher: cipher}
}

// FindWriteTool resolves a namespaced name to a callable target, re-checking
// every gate at the moment it is asked.
//
// The gates are the inverse of the read path's: enabled server, approved tool,
// **not** read-only, not drifted. A tool an admin has since re-classified as
// read-only is not runnable through here — it is an ordinary tool call again,
// and the ticket's own out-of-scope line says a misclassification is corrected
// by the admin, never by this action deciding for itself.
func (s *MCPCallStore) FindWriteTool(ctx context.Context, namespacedName string) (actions.MCPTarget, error) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return actions.MCPTarget{}, domain.ErrNotFound
	}
	servers, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return actions.MCPTarget{}, err
	}
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		toolRows, err := s.repo.ListTools(ctx, srv.ID)
		if err != nil {
			return actions.MCPTarget{}, err
		}
		for _, tr := range toolRows {
			if !tr.Approved || tr.ReadOnly || tr.Drifted() {
				continue
			}
			if mcptools.ToolName(srv.Name, tr.ToolName) != namespacedName {
				continue
			}
			token, err := s.token(srv)
			if err != nil {
				return actions.MCPTarget{}, err
			}
			return actions.MCPTarget{
				ServerID:   srv.ID,
				ServerName: srv.Name,
				ToolName:   tr.ToolName,
				URL:        srv.URL,
				Transport:  srv.Transport,
				Token:      token,
			}, nil
		}
	}
	return actions.MCPTarget{}, domain.ErrNotFound
}

// ListWriteToolNames is the catalog half: the names a turn may propose. Same
// gates, no token read — nothing about listing a name builds a request.
func (s *MCPCallStore) ListWriteToolNames(ctx context.Context) ([]string, error) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return nil, nil
	}
	servers, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		toolRows, err := s.repo.ListTools(ctx, srv.ID)
		if err != nil {
			return nil, err
		}
		for _, tr := range toolRows {
			if !tr.Approved || tr.ReadOnly || tr.Drifted() {
				continue
			}
			names = append(names, mcptools.ToolName(srv.Name, tr.ToolName))
		}
	}
	return names, nil
}

// token decrypts a server's bearer token. No token is not an error: a server
// open to anyone who knows its URL is a supported configuration.
func (s *MCPCallStore) token(srv *domain.MCPServer) (string, error) {
	if len(srv.AuthEncrypted) == 0 {
		return "", nil
	}
	if s.cipher == nil {
		return "", errNoMCPCipher
	}
	return s.cipher.Decrypt(srv.AuthEncrypted)
}
