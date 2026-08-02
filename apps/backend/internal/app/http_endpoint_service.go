// Package app — registered HTTP endpoints, the admin side of http_action (T-12b).
//
// http_action lets an agent call one of a company's own systems. The safety model
// is that the agent never types a URL: it names an endpoint an admin registered
// here, and this service is where the method, the host and the credentials are
// fixed. Nothing in this file reaches a turn — it is CRUD plus validation, the
// same split MCPServerService made against its turn-time source.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"text/template"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

const (
	// httpEndpointNameMax bounds the name the agent proposes against and a list
	// shows.
	httpEndpointNameMax = 60
	// httpEndpointURLMax is generous for a URL with placeholders and finite for a
	// text column.
	httpEndpointURLMax = 2000
	// httpEndpointHeaderMax bounds the header JSON. Room for a signed JWT and a
	// couple of fixed headers, not a certificate chain.
	httpEndpointHeaderMax = 8000
	// httpEndpointBodyMax bounds the body template.
	httpEndpointBodyMax = 8000
)

// httpEndpointMethods is the verb set an endpoint may register. Mirrors the
// action's own set; validated here so a bad method is a rejected save, not a
// proposal that fails after approval.
var httpEndpointMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// HTTPEndpointCipherRW encrypts a header template at rest. Satisfied by
// *crypto.DSNCipher — the same envelope db_connections.dsn_encrypted uses, because
// a second scheme for a second secret is how a codebase ends up with one key it
// can rotate and one it cannot.
type HTTPEndpointCipherRW interface {
	Encrypt(plain string) ([]byte, error)
}

// HTTPEndpointURLChecker is the egress guard's URL check, narrowed to what
// registration asks of it: reject a URL that is not https (unless the deployment
// permits plaintext) or whose literal host is our own network, before the row is
// stored. Satisfied by mcp.Guard.
type HTTPEndpointURLChecker interface {
	CheckURL(raw string) error
}

// HTTPEndpointService is the admin-only CRUD behind Settings → HTTP endpoints, and
// the registry http_action resolves a name against at turn time (through the
// separate resolver, which decrypts; this service never does).
type HTTPEndpointService struct {
	repo    domain.HTTPEndpointRepository
	cipher  HTTPEndpointCipherRW
	checker HTTPEndpointURLChecker
}

func NewHTTPEndpointService(repo domain.HTTPEndpointRepository, cipher HTTPEndpointCipherRW, checker HTTPEndpointURLChecker) *HTTPEndpointService {
	return &HTTPEndpointService{repo: repo, cipher: cipher, checker: checker}
}

// HTTPEndpointInput is one submitted endpoint. Header is write-only and sealed on
// the way in — it carries the credential, so it is never read back. There is no
// update: an endpoint is a credential plus an egress destination, and editing one
// already named by in-flight proposals changes what they point at, so a change is
// delete-then-register, which is explicit about the discontinuity.
type HTTPEndpointInput struct {
	Name         string `json:"name"`
	Method       string `json:"method"`
	URLTemplate  string `json:"url_template"`
	Header       string `json:"header,omitempty"`
	BodyTemplate string `json:"body_template,omitempty"`
}

// Register validates and stores one endpoint. Everything that could make an
// http_action unsafe or un-runnable is checked here, at admin time, rather than
// discovered at execute time after a human approved a proposal: the method, the
// literal host, the templates' syntax, and the egress guard's verdict on the host.
func (s *HTTPEndpointService) Register(ctx context.Context, companyID, actorID string, in HTTPEndpointInput) (*domain.HTTPEndpoint, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: an endpoint name is required — it is what the agent proposes against", domain.ErrInvalidInput)
	}
	if len(name) > httpEndpointNameMax {
		return nil, fmt.Errorf("%w: the endpoint name is too long (max %d)", domain.ErrInvalidInput, httpEndpointNameMax)
	}

	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if !httpEndpointMethods[method] {
		return nil, fmt.Errorf("%w: %q is not a supported method; use one of GET, POST, PUT, PATCH, DELETE", domain.ErrInvalidInput, in.Method)
	}

	urlTemplate := strings.TrimSpace(in.URLTemplate)
	if err := s.validateURLTemplate(urlTemplate); err != nil {
		return nil, err
	}

	if len(in.BodyTemplate) > httpEndpointBodyMax {
		return nil, fmt.Errorf("%w: the body template is too long (max %d)", domain.ErrInvalidInput, httpEndpointBodyMax)
	}
	if err := checkTemplateSyntax("body", in.BodyTemplate); err != nil {
		return nil, err
	}

	var sealed []byte
	if strings.TrimSpace(in.Header) != "" {
		if err := s.validateHeader(in.Header); err != nil {
			return nil, err
		}
		var encErr error
		sealed, encErr = s.cipher.Encrypt(in.Header)
		if encErr != nil {
			return nil, fmt.Errorf("seal endpoint headers: %w", encErr)
		}
	}

	// Case-insensitive collision, caught before the DB's case-sensitive unique
	// constraint so the message names the real problem — "create_ticket" and
	// "Create_Ticket" are the same target to a human and would be two rows to the
	// index.
	existing, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("check existing endpoints: %w", err)
	}
	for _, e := range existing {
		if strings.EqualFold(e.Name, name) {
			return nil, fmt.Errorf("%w: an endpoint named %q is already registered", domain.ErrAlreadyExists, e.Name)
		}
	}

	ep := &domain.HTTPEndpoint{
		CompanyID:       companyID,
		Name:            name,
		Method:          method,
		URLTemplate:     urlTemplate,
		HeaderEncrypted: sealed,
		HasHeader:       len(sealed) > 0,
		BodyTemplate:    in.BodyTemplate,
		CreatedBy:       actorID,
	}
	if err := s.repo.Create(ctx, ep); err != nil {
		return nil, fmt.Errorf("register endpoint: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "endpoint": name, "method": method,
	}).Info("http endpoint registered")
	return ep, nil
}

// List returns a company's registered endpoints. The header template is never on
// the wire — HasHeader is the only thing said about it — so a list is safe to show
// without decrypting anything.
func (s *HTTPEndpointService) List(ctx context.Context, companyID string) ([]*domain.HTTPEndpoint, error) {
	return s.repo.ListByCompany(ctx, companyID)
}

// Delete removes an endpoint. A proposal that named it fails at execute with a
// plain "no endpoint named X" rather than being silently retargeted, which is why
// there is no rename.
func (s *HTTPEndpointService) Delete(ctx context.Context, companyID, id string) error {
	return s.repo.Delete(ctx, companyID, id)
}

// validateURLTemplate enforces the property the whole feature rests on: the host
// is fixed. The authority must be literal (no placeholder before the path), the
// URL must pass the egress guard (https unless the deployment allows plaintext,
// and not our own network), and the template must parse.
func (s *HTTPEndpointService) validateURLTemplate(urlTemplate string) error {
	if urlTemplate == "" {
		return fmt.Errorf("%w: a URL is required", domain.ErrInvalidInput)
	}
	if len(urlTemplate) > httpEndpointURLMax {
		return fmt.Errorf("%w: the URL is too long (max %d)", domain.ErrInvalidInput, httpEndpointURLMax)
	}
	u, err := url.Parse(urlTemplate)
	if err != nil {
		return fmt.Errorf("%w: the URL is not valid: %v", domain.ErrInvalidInput, err)
	}
	if strings.Contains(u.Scheme, "{{") || strings.Contains(u.Host, "{{") || u.Host == "" {
		return fmt.Errorf("%w: the URL scheme and host must be fixed (no {{placeholders}} before the path); only the path and query may be templated", domain.ErrInvalidInput)
	}
	if err := checkTemplateSyntax("url", urlTemplate); err != nil {
		return err
	}
	// The guard decides https-vs-plaintext and rejects a literal host that is our
	// own network — the same verdict the turn-time dial will reach, surfaced now so
	// an unreachable endpoint is a rejected save rather than a proposal that fails
	// after a human approved it.
	if s.checker != nil {
		if err := s.checker.CheckURL(urlTemplate); err != nil {
			return fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
		}
	}
	return nil
}

// validateHeader requires the header template to be a JSON object of string to
// string and to parse as a template. It is validated but not rendered here: the
// values may reference placeholders that only exist at call time.
func (s *HTTPEndpointService) validateHeader(header string) error {
	if len(header) > httpEndpointHeaderMax {
		return fmt.Errorf("%w: the header template is too long (max %d)", domain.ErrInvalidInput, httpEndpointHeaderMax)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(header), &obj); err != nil {
		return fmt.Errorf("%w: the header template must be a JSON object of header name to value, e.g. {\"Authorization\":\"Bearer …\"}", domain.ErrInvalidInput)
	}
	return checkTemplateSyntax("header", header)
}

// checkTemplateSyntax rejects a template with a broken {{ at registration, so a
// malformed placeholder is an error the admin sees rather than one the agent hits
// after approval. An empty template is fine.
func checkTemplateSyntax(name, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if _, err := template.New(name).Option("missingkey=error").Parse(text); err != nil {
		return fmt.Errorf("%w: the %s template is not valid: %v", domain.ErrInvalidInput, name, err)
	}
	return nil
}

// AvailableMethods is the verb set the registration form offers, sorted for a
// stable list.
func (s *HTTPEndpointService) AvailableMethods() []string {
	return []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
}
