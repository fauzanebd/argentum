package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeEndpointRepo is an in-memory HTTPEndpointRepository for the service tests.
type fakeEndpointRepo struct {
	rows []*domain.HTTPEndpoint
}

func (f *fakeEndpointRepo) Create(_ context.Context, e *domain.HTTPEndpoint) error {
	for _, r := range f.rows {
		if r.CompanyID == e.CompanyID && r.Name == e.Name {
			return domain.ErrAlreadyExists
		}
	}
	e.ID = "id-" + e.Name
	f.rows = append(f.rows, e)
	return nil
}
func (f *fakeEndpointRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.HTTPEndpoint, error) {
	out := []*domain.HTTPEndpoint{}
	for _, r := range f.rows {
		if r.CompanyID == companyID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeEndpointRepo) GetByID(_ context.Context, companyID, id string) (*domain.HTTPEndpoint, error) {
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (f *fakeEndpointRepo) GetByName(_ context.Context, companyID, name string) (*domain.HTTPEndpoint, error) {
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.Name == name {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (f *fakeEndpointRepo) Delete(_ context.Context, companyID, id string) error {
	for i, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// recordingCipher seals by prefixing, so a test can assert a header was encrypted
// (the stored bytes are not the plaintext) without a real key.
type recordingCipher struct{ calls int }

func (c *recordingCipher) Encrypt(plain string) ([]byte, error) {
	c.calls++
	return append([]byte("sealed:"), []byte(plain)...), nil
}

func newEndpointSvc() (*HTTPEndpointService, *fakeEndpointRepo, *recordingCipher) {
	repo := &fakeEndpointRepo{}
	cipher := &recordingCipher{}
	// A real guard as the URL checker: the service's private-host and scheme
	// refusals are the guard's, and a fake would not prove they run.
	svc := NewHTTPEndpointService(repo, cipher, mcp.Guard{})
	return svc, repo, cipher
}

func TestRegisterEndpointValidAndSealed(t *testing.T) {
	svc, repo, cipher := newEndpointSvc()
	ep, err := svc.Register(context.Background(), "co1", "admin1", HTTPEndpointInput{
		Name:         "create_ticket",
		Method:       "post",
		URLTemplate:  "https://example.com/v2/tickets/{{.id}}",
		Header:       `{"Authorization":"Bearer s3cr3t"}`,
		BodyTemplate: `{"subject":"{{.subject}}"}`,
	})
	if err != nil {
		t.Fatalf("Register = %v; want nil", err)
	}
	if ep.Method != "POST" {
		t.Fatalf("method = %q; want upper-cased POST", ep.Method)
	}
	if cipher.calls != 1 {
		t.Fatalf("cipher called %d times; want the header sealed once", cipher.calls)
	}
	stored := repo.rows[0]
	if string(stored.HeaderEncrypted) == `{"Authorization":"Bearer s3cr3t"}` {
		t.Fatal("header stored in plaintext; want it sealed")
	}
	if !ep.HasHeader {
		t.Fatal("HasHeader = false; want true when a header was set")
	}
}

func TestRegisterEndpointRejections(t *testing.T) {
	cases := []struct {
		name string
		in   HTTPEndpointInput
	}{
		{"templated host", HTTPEndpointInput{Name: "a", Method: "GET", URLTemplate: "https://{{.host}}/x"}},
		{"bad method", HTTPEndpointInput{Name: "a", Method: "FETCH", URLTemplate: "https://example.com/x"}},
		{"non-https", HTTPEndpointInput{Name: "a", Method: "GET", URLTemplate: "http://example.com/x"}},
		{"private host", HTTPEndpointInput{Name: "a", Method: "GET", URLTemplate: "https://127.0.0.1/x"}},
		// A public name that answers 127.0.0.1. It passes the string-only check and
		// is refused by the dialer, so registering it stored an endpoint that could
		// never work — observed live on 2026-08-02, where the approved invocation
		// came back "egress blocked: ::1 is a loopback address" after a human had
		// already approved it. The save has to ask the resolving question.
		{"host resolving to loopback", HTTPEndpointInput{Name: "a", Method: "GET", URLTemplate: "https://localtest.me/x"}},
		{"empty name", HTTPEndpointInput{Name: "", Method: "GET", URLTemplate: "https://example.com/x"}},
		{"header not object", HTTPEndpointInput{Name: "a", Method: "GET", URLTemplate: "https://example.com/x", Header: `["nope"]`}},
		{"broken template", HTTPEndpointInput{Name: "a", Method: "GET", URLTemplate: "https://example.com/{{.id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newEndpointSvc()
			_, err := svc.Register(context.Background(), "co1", "admin1", tc.in)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("Register(%s) = %v; want ErrInvalidInput", tc.name, err)
			}
		})
	}
}

func TestRegisterEndpointCaseInsensitiveCollision(t *testing.T) {
	svc, _, _ := newEndpointSvc()
	base := HTTPEndpointInput{Name: "Create_Ticket", Method: "GET", URLTemplate: "https://example.com/x"}
	if _, err := svc.Register(context.Background(), "co1", "a", base); err != nil {
		t.Fatalf("first Register = %v; want nil", err)
	}
	dup := HTTPEndpointInput{Name: "create_ticket", Method: "GET", URLTemplate: "https://example.com/y"}
	_, err := svc.Register(context.Background(), "co1", "a", dup)
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate Register = %v; want ErrAlreadyExists", err)
	}
}

func TestDeleteEndpoint(t *testing.T) {
	svc, _, _ := newEndpointSvc()
	ep, err := svc.Register(context.Background(), "co1", "a",
		HTTPEndpointInput{Name: "x", Method: "GET", URLTemplate: "https://example.com/x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), "co1", ep.ID); err != nil {
		t.Fatalf("Delete = %v; want nil", err)
	}
	// Another company cannot delete it — company-scoped.
	if err := svc.Delete(context.Background(), "co2", ep.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-company Delete = %v; want ErrNotFound", err)
	}
}
