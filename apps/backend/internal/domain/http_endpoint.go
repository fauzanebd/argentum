package domain

import (
	"context"
	"time"
)

// HTTPEndpoint is one target an http_action may call (T-12b).
//
// It is the registered half of the http_action feature: an admin sets up a named
// endpoint here — a method, a URL whose host is fixed and whose path may carry
// placeholders, the headers that authenticate it, and an optional body template —
// and the agent proposes a call by *name*, never by URL. Everything that decides
// where a call goes and how it is authorized was set by a human; the model only
// fills the declared holes.
//
// The credential lives in Header, sealed at rest. A repository returns it
// encrypted (HeaderEncrypted) to every caller except the turn-time resolver, which
// decrypts it into Header for the one moment a request is built. HasHeader is the
// fact a list view is allowed to see — that a header template is set — without the
// bytes.
type HTTPEndpoint struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	// Name is the stable, company-scoped identifier the agent proposes against,
	// e.g. "create_ticket".
	Name string `json:"name"`
	// Method is the HTTP verb, upper-cased and validated to a known set at
	// registration.
	Method string `json:"method"`
	// URLTemplate has a literal scheme://host authority and optional
	// {{.placeholders}} in its path and query. The literal authority is what makes
	// the host un-forgeable from turn-time input.
	URLTemplate string `json:"url_template"`
	// HeaderEncrypted is the sealed header template as stored, JSON of header
	// name→value with optional placeholders. Nil when the endpoint sets no headers.
	// Never serialized to a client.
	HeaderEncrypted []byte `json:"-"`
	// Header is the decrypted header template, populated only by the turn-time
	// resolver and empty everywhere else. It is not serialized: a credential does
	// not leave this process in a response body.
	Header string `json:"-"`
	// HasHeader reports whether a header template is set, derived on read so no
	// second column can disagree with the bytes.
	HasHeader bool `json:"has_header"`
	// BodyTemplate is an optional request-body template, filled from the same
	// values as the URL. Empty for a call with no body.
	BodyTemplate string    `json:"body_template"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// HTTPEndpointRepository persists a company's registered endpoints (T-12b).
//
// Every method takes the company id beside any row id, like MCPServerRepo and for
// the same reason: an endpoint is an egress destination plus a credential, a
// DSN-class object, and a repository that will answer for any company is one
// forgotten check from a cross-tenant read. The repository stores and returns the
// header sealed; decryption is the resolver's job, not the store's.
type HTTPEndpointRepository interface {
	Create(ctx context.Context, e *HTTPEndpoint) error
	ListByCompany(ctx context.Context, companyID string) ([]*HTTPEndpoint, error)
	GetByID(ctx context.Context, companyID, id string) (*HTTPEndpoint, error)
	GetByName(ctx context.Context, companyID, name string) (*HTTPEndpoint, error)
	Delete(ctx context.Context, companyID, id string) error
}
