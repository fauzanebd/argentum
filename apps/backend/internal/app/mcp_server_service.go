package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The tenant's MCP servers: register, discover, review (T-M1).
//
// Nothing in this file reaches a turn. It is the same split T-S1 made against
// T-S2 and for the same reason — a schema, a CRUD surface, an egress allowlist
// and a review screen do not belong in the ticket that rewires tool
// registration. Calling a tool is T-M2.
//
// Two rules shape every decision here:
//
//  1. **Nothing is callable until an admin approves it** (locked decision 2).
//     A discovered tool arrives unapproved and not-read-only, which is the
//     opposite of the roster's empty-means-everything rule and is meant to be:
//     there the failure is an agent that cannot answer, here it is a write
//     against a system we do not own.
//
//  2. **A probe failure is a saved row, not a rejected save.** A server that is
//     down at 4pm is not a configuration error, and an admin who cannot save
//     the row cannot fix the URL either.

const (
	// mcpNameMax bounds what shows in a list and, after T-M2, prefixes a tool
	// name the model sees.
	mcpNameMax = 60
	// mcpDescriptionMax bounds the line under the name.
	mcpDescriptionMax = 240
	// mcpURLMax is generous for a URL and finite for a text column.
	mcpURLMax = 2000
	// mcpTokenMax bounds the bearer token. Long enough for a signed JWT, short
	// enough that nobody pastes a private key into it.
	mcpTokenMax = 4000
)

// MCPProber is discovery, narrowed to what this service asks of it: check a URL
// without touching the network, and list a server's tools.
//
// An interface rather than *mcp.Client so the service is testable without a
// server to point at — and so the egress guard is something the service is
// *given* rather than something it could forget to build.
type MCPProber interface {
	CheckURL(raw string) error
	// AllowsInsecureHTTP is what the form's hint is written from, so the rule
	// on screen is the rule the save applies.
	AllowsInsecureHTTP() bool
	Probe(ctx context.Context, url string, transport domain.MCPTransport, token string) ([]mcp.DiscoveredTool, error)
}

// MCPServerStore is the persistence this service needs. It is
// domain.MCPServerRepository plus ClearAuth, which is deliberately not on the
// domain interface: clearing a credential is a UI affordance, not a fact about
// the entity, and nothing outside this service has any business calling it.
type MCPServerStore interface {
	domain.MCPServerRepository
	ClearAuth(ctx context.Context, companyID, id string) error
}

// MCPCipher encrypts the tenant's token at rest. Satisfied by *crypto.DSNCipher
// — the same envelope db_connections.dsn_encrypted uses, because inventing a
// second scheme for the second secret is how a codebase ends up with one key it
// can rotate and one it cannot.
type MCPCipher interface {
	Encrypt(plain string) ([]byte, error)
	Decrypt(blob []byte) (string, error)
}

// MCPServerService is the admin-only CRUD surface behind Settings → MCP servers.
type MCPServerService struct {
	repo   MCPServerStore
	cipher MCPCipher
	prober MCPProber
}

func NewMCPServerService(repo MCPServerStore, cipher MCPCipher, prober MCPProber) *MCPServerService {
	return &MCPServerService{repo: repo, cipher: cipher, prober: prober}
}

// MCPServerInput is one submitted server.
//
// AuthToken is write-only and three-valued, which is the only fiddly thing in
// this file: nil means "leave the stored token alone" (the form cannot show it
// back, so an empty field must not delete it), a non-empty string replaces it,
// and ClearAuth removes it. A two-valued field would make every edit of the
// description a coin flip on the credential.
type MCPServerInput struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	URL         string              `json:"url"`
	Transport   domain.MCPTransport `json:"transport"`
	AuthToken   *string             `json:"auth_token,omitempty"`
	ClearAuth   bool                `json:"clear_auth,omitempty"`
	Enabled     *bool               `json:"enabled,omitempty"`
}

// AllowsInsecureHTTP reports whether this deployment accepts a plaintext http
// MCP URL, for the create form's hint. It is a deployment fact rather than a
// per-tenant one, which is why it rides on the list response beside the
// transports rather than being a second request.
func (s *MCPServerService) AllowsInsecureHTTP() bool {
	if s == nil || s.prober == nil {
		return false
	}
	return s.prober.AllowsInsecureHTTP()
}

// List returns the company's servers.
func (s *MCPServerService) List(ctx context.Context, companyID string) ([]*domain.MCPServer, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("mcp servers are not configured")
	}
	return s.repo.ListByCompany(ctx, companyID)
}

// Get returns one server and the tools discovery found on it.
func (s *MCPServerService) Get(ctx context.Context, companyID, id string) (*domain.MCPServer, []*domain.MCPServerTool, error) {
	srv, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, nil, err
	}
	tools, err := s.repo.ListTools(ctx, srv.ID)
	if err != nil {
		return nil, nil, err
	}
	return srv, tools, nil
}

// Create registers a server and immediately tries to discover its tools.
//
// The probe runs after the row is saved, so its failure is reportable rather
// than fatal — the returned server carries ProbeError, and the tenant can fix
// the URL from a row that exists. What is *not* forgiving is the URL check: a
// URL the egress guard refuses is a rejected save, because storing it would
// mean a row whose only possible outcome is a blocked request.
func (s *MCPServerService) Create(ctx context.Context, companyID string, in MCPServerInput) (*domain.MCPServer, []*domain.MCPServerTool, error) {
	srv, token, err := s.validated(companyID, in, nil)
	if err != nil {
		return nil, nil, err
	}
	if err := s.repo.Create(ctx, srv); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, nil, fmt.Errorf("%w: an MCP server called %q already exists", domain.ErrAlreadyExists, srv.Name)
		}
		return nil, nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "server_id": srv.ID, "name": srv.Name,
		"transport": srv.Transport, "has_auth": srv.HasAuth,
	}).Info("mcp server registered")

	tools := s.discover(ctx, companyID, srv, token)
	return srv, tools, nil
}

// Update rewrites a server. A changed URL or transport re-probes, because the
// tool list belongs to the endpoint rather than to the row — leaving the old
// server's tools under the new URL would show an admin capabilities the new
// endpoint may not have.
func (s *MCPServerService) Update(ctx context.Context, companyID, id string, in MCPServerInput) (*domain.MCPServer, []*domain.MCPServerTool, error) {
	current, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, nil, err
	}
	srv, token, err := s.validated(companyID, in, current)
	if err != nil {
		return nil, nil, err
	}
	srv.ID = current.ID
	if err := s.repo.Update(ctx, srv); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, nil, fmt.Errorf("%w: an MCP server called %q already exists", domain.ErrAlreadyExists, srv.Name)
		}
		return nil, nil, err
	}
	if in.ClearAuth {
		if err := s.repo.ClearAuth(ctx, companyID, id); err != nil {
			return nil, nil, err
		}
		srv.HasAuth = false
		token = ""
	}

	if srv.URL != current.URL || srv.Transport != current.Transport || in.AuthToken != nil || in.ClearAuth {
		tools := s.discover(ctx, companyID, srv, token)
		return srv, tools, nil
	}
	tools, err := s.repo.ListTools(ctx, srv.ID)
	return srv, tools, err
}

// Delete removes a server and, by cascade, its discovered tools.
func (s *MCPServerService) Delete(ctx context.Context, companyID, id string) error {
	if err := s.repo.Delete(ctx, companyID, id); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "server_id": id}).Info("mcp server deleted")
	return nil
}

// Refresh re-runs discovery on an admin's request.
//
// This is the whole of "discovery is explicit, not per turn" (locked decision
// 6): a tenant's server changing what our agent can do is a thing somebody
// presses a button to accept, not something that happens between two questions.
func (s *MCPServerService) Refresh(ctx context.Context, companyID, id string) (*domain.MCPServer, []*domain.MCPServerTool, error) {
	srv, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, nil, err
	}
	token, err := s.token(srv)
	if err != nil {
		return nil, nil, err
	}
	tools := s.discover(ctx, companyID, srv, token)
	return srv, tools, nil
}

// ReviewTool records an admin's decision about one discovered tool.
//
// The digest is computed from the row as it stands, which is the text the admin
// was looking at when they approved it. That is what makes drift detectable: a
// server that rewrites the description afterwards no longer matches the hash of
// what was reviewed, and MCPServerTool.Drifted says so on the next read.
//
// Un-approving clears the digest rather than keeping it. A tool nobody approves
// has nothing to have drifted from, and a stale hash would light the drift
// warning on a row that is already switched off.
func (s *MCPServerService) ReviewTool(
	ctx context.Context, companyID, serverID, toolID string, approved, readOnly bool,
) ([]*domain.MCPServerTool, error) {
	if _, err := s.repo.GetByID(ctx, companyID, serverID); err != nil {
		return nil, err
	}
	tools, err := s.repo.ListTools(ctx, serverID)
	if err != nil {
		return nil, err
	}
	var target *domain.MCPServerTool
	for _, t := range tools {
		if t.ID == toolID {
			target = t
			break
		}
	}
	if target == nil {
		return nil, domain.ErrNotFound
	}

	digest := ""
	if approved {
		digest = domain.MCPToolDigest(target.Description, target.InputSchema)
	}
	if err := s.repo.SetToolReview(ctx, serverID, toolID, approved, readOnly, digest); err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "server_id": serverID, "tool": target.ToolName,
		"approved": approved, "read_only": readOnly,
	}).Info("mcp tool reviewed")
	return s.repo.ListTools(ctx, serverID)
}

// discover probes and stores, and never returns an error.
//
// Every failure path ends in a row with probe_error set and the previously
// discovered tools left alone: an admin looking at a server that stopped
// answering should still see what it offered yesterday, with the reason it is
// not answering today beside it.
func (s *MCPServerService) discover(
	ctx context.Context, companyID string, srv *domain.MCPServer, token string,
) []*domain.MCPServerTool {
	now := time.Now().UTC()
	found, err := s.prober.Probe(ctx, srv.URL, srv.Transport, token)
	if err != nil {
		msg := err.Error()
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "server_id": srv.ID, "url": srv.URL,
		}).Warn("mcp discovery failed; the server is saved with the error on it")
		if recErr := s.repo.RecordProbe(ctx, companyID, srv.ID, now, msg); recErr != nil {
			logrus.WithError(recErr).Warn("could not record the mcp probe failure")
		}
		srv.LastProbedAt = &now
		srv.ProbeError = msg
		tools, _ := s.repo.ListTools(ctx, srv.ID)
		return tools
	}

	rows := make([]*domain.MCPServerTool, 0, len(found))
	for _, t := range found {
		schema := t.InputSchema
		if len(schema) == 0 || !json.Valid(schema) {
			// A schema we cannot parse is stored as an empty object rather than
			// as invalid JSON: the column is jsonb, and one malformed tool must
			// not fail the discovery of the rest.
			schema = json.RawMessage(`{}`)
		}
		rows = append(rows, &domain.MCPServerTool{
			ToolName: strings.TrimSpace(t.Name),
			// Their text, stored as they wrote it. It is untrusted — this is
			// the string that enters our agent's context once approved — and
			// sanitising it here would mean an admin approving one thing and
			// the model reading another.
			Description: t.Description,
			InputSchema: schema,
		})
	}
	if err := s.repo.ReplaceTools(ctx, srv.ID, rows); err != nil {
		logrus.WithError(err).WithField("server_id", srv.ID).Warn("could not store discovered mcp tools")
	}
	if err := s.repo.RecordProbe(ctx, companyID, srv.ID, now, ""); err != nil {
		logrus.WithError(err).Warn("could not record the mcp probe")
	}
	srv.LastProbedAt = &now
	srv.ProbeError = ""
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "server_id": srv.ID, "tools": len(rows),
	}).Info("mcp discovery completed")

	tools, _ := s.repo.ListTools(ctx, srv.ID)
	return tools
}

// token decrypts a stored credential. A row with no token is not an error: an
// MCP server open to anyone who knows its URL is a supported configuration, and
// a common one for a server behind a VPN or a mTLS gateway.
func (s *MCPServerService) token(srv *domain.MCPServer) (string, error) {
	if len(srv.AuthEncrypted) == 0 {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("no cipher configured to read the stored token")
	}
	token, err := s.cipher.Decrypt(srv.AuthEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt mcp token: %w", err)
	}
	return token, nil
}

// validated turns submitted input into a server, or into the reason it is not
// one. current is nil on create and the stored row on update, which is what
// makes "leave the token alone" expressible.
func (s *MCPServerService) validated(
	companyID string, in MCPServerInput, current *domain.MCPServer,
) (*domain.MCPServer, string, error) {
	name := strings.TrimSpace(in.Name)
	description := strings.TrimSpace(in.Description)
	rawURL := strings.TrimSpace(in.URL)
	transport := domain.MCPTransport(strings.TrimSpace(string(in.Transport)))
	if transport == "" {
		transport = domain.MCPTransportHTTP
	}

	switch {
	case companyID == "":
		return nil, "", fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case name == "":
		return nil, "", fmt.Errorf("%w: an MCP server needs a name", domain.ErrInvalidInput)
	case len([]rune(name)) > mcpNameMax:
		return nil, "", fmt.Errorf("%w: name must be %d characters or fewer", domain.ErrInvalidInput, mcpNameMax)
	case len([]rune(description)) > mcpDescriptionMax:
		return nil, "", fmt.Errorf("%w: description must be %d characters or fewer", domain.ErrInvalidInput, mcpDescriptionMax)
	case rawURL == "":
		return nil, "", fmt.Errorf("%w: an MCP server needs a URL", domain.ErrInvalidInput)
	case len(rawURL) > mcpURLMax:
		return nil, "", fmt.Errorf("%w: that URL is too long", domain.ErrInvalidInput)
	case !transport.Valid():
		// Named, because the one somebody will try is "stdio", and the answer
		// to it is a decision rather than an omission.
		return nil, "", fmt.Errorf("%w: transport must be %q or %q — stdio is not supported",
			domain.ErrInvalidInput, domain.MCPTransportHTTP, domain.MCPTransportSSE)
	}

	// The egress guard decides the URL, here and again at dial time. A refusal
	// is ErrInvalidInput because the tenant typed it and the tenant can fix it.
	if err := s.prober.CheckURL(rawURL); err != nil {
		return nil, "", fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	srv := &domain.MCPServer{
		CompanyID:   companyID,
		Name:        name,
		Description: description,
		URL:         rawURL,
		Transport:   transport,
		Enabled:     in.Enabled == nil || *in.Enabled,
	}

	token := ""
	switch {
	case in.ClearAuth:
		// Nothing to encrypt; Update clears the column explicitly afterwards.
	case in.AuthToken != nil:
		token = strings.TrimSpace(*in.AuthToken)
		if len(token) > mcpTokenMax {
			return nil, "", fmt.Errorf("%w: that token is too long", domain.ErrInvalidInput)
		}
		if token != "" {
			if s.cipher == nil {
				return nil, "", fmt.Errorf("no cipher configured to store a token")
			}
			blob, err := s.cipher.Encrypt(token)
			if err != nil {
				return nil, "", fmt.Errorf("encrypt mcp token: %w", err)
			}
			srv.AuthEncrypted = blob
			srv.HasAuth = true
		}
	case current != nil:
		// Untouched: AuthEncrypted stays nil so the repository's keep-auth
		// statement runs, and the stored token is reused for this probe.
		srv.HasAuth = current.HasAuth
		stored, err := s.token(current)
		if err != nil {
			return nil, "", err
		}
		token = stored
	}
	return srv, token, nil
}
