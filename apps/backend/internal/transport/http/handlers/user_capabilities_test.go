package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// memCapabilities is user_capabilities with the users join the postgres
// repository starts every statement from: each user id belongs to one company,
// and an id asked about under any other company is not found.
type memCapabilities struct {
	members map[string]string                 // user id → company id
	rows    map[string]domain.CapabilityGrant // "user/capability"
	grants  int
}

func newMemCapabilities() *memCapabilities {
	return &memCapabilities{
		members: map[string]string{"admin-1": "co-1", "u-1": "co-1", "u-9": "co-2"},
		rows:    map[string]domain.CapabilityGrant{},
	}
}

func (m *memCapabilities) ListForUser(_ context.Context, companyID, userID string) ([]domain.CapabilityGrant, error) {
	if m.members[userID] != companyID {
		return nil, domain.ErrNotFound
	}
	// nil rather than an empty slice on purpose: the handler must still answer
	// `[]`, whatever an implementation of the repository hands it.
	var out []domain.CapabilityGrant
	for _, g := range m.rows {
		if g.UserID == userID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *memCapabilities) Grant(_ context.Context, companyID, userID string, c domain.Capability, grantedBy string) error {
	m.grants++
	if m.members[userID] != companyID {
		return domain.ErrNotFound
	}
	key := userID + "/" + string(c)
	if _, held := m.rows[key]; !held {
		m.rows[key] = domain.CapabilityGrant{UserID: userID, Capability: c, GrantedBy: grantedBy, GrantedAt: time.Now()}
	}
	return nil
}

func (m *memCapabilities) Revoke(_ context.Context, companyID, userID string, c domain.Capability) error {
	if m.members[userID] != companyID {
		return domain.ErrNotFound
	}
	delete(m.rows, userID+"/"+string(c))
	return nil
}

// capabilityRoutes registers UserHandler under /users, as cmd/api does, behind a
// stand-in for Auth that puts admin-1 of co-1 on the context. The role table is
// not under test here — cmd/api's policy tests own it — so this is what the
// handler does once a request has been let through.
func capabilityRoutes(repo domain.CapabilityRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/users")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "admin-1")
		c.Set("role", "admin")
	})
	NewUserHandler(nil, nil, nil).WithCapabilities(app.NewCapabilityService(repo, nil)).Register(g)
	return r
}

func serve(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestGrantingAnUnknownCapabilityIsRefusedAndNotStored(t *testing.T) {
	repo := newMemCapabilities()
	r := capabilityRoutes(repo)
	w := serve(r, http.MethodPut, "/users/u-1/capabilities/telepathy")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if repo.grants != 0 || len(repo.rows) != 0 {
		t.Errorf("an unknown capability reached the store: %d calls, %d rows", repo.grants, len(repo.rows))
	}
}

func TestGrantingTwiceIsIdempotent(t *testing.T) {
	repo := newMemCapabilities()
	r := capabilityRoutes(repo)
	for i := 1; i <= 2; i++ {
		if w := serve(r, http.MethodPut, "/users/u-1/capabilities/voice"); w.Code != http.StatusNoContent {
			t.Fatalf("grant #%d: status = %d, want 204: %s", i, w.Code, w.Body.String())
		}
	}
	if len(repo.rows) != 1 {
		t.Errorf("%d rows after granting one capability twice, want 1", len(repo.rows))
	}
	if got := repo.rows["u-1/voice"].GrantedBy; got != "admin-1" {
		t.Errorf("granted_by = %q, want the caller from the session, admin-1", got)
	}
}

func TestRevokingWhatIsNotHeldIsIdempotent(t *testing.T) {
	r := capabilityRoutes(newMemCapabilities())
	if w := serve(r, http.MethodDelete, "/users/u-1/capabilities/export_data"); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
}

// u-9 is co-2's. Holding its id gets an admin of co-1 a 404 on every route, and
// writes nothing.
func TestCapabilityRoutesDoNotReachAnotherCompanysUser(t *testing.T) {
	repo := newMemCapabilities()
	r := capabilityRoutes(repo)
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/users/u-9/capabilities"},
		{http.MethodPut, "/users/u-9/capabilities/voice"},
		{http.MethodDelete, "/users/u-9/capabilities/voice"},
	} {
		if w := serve(r, req.method, req.path); w.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404: %s", req.method, req.path, w.Code, w.Body.String())
		}
	}
	if len(repo.rows) != 0 {
		t.Errorf("a cross-tenant request wrote %d rows", len(repo.rows))
	}
}

// A member reads their own and only their own: `me` resolves from the session,
// never from a path segment a caller controls.
func TestMyCapabilitiesAreTheCallersOnly(t *testing.T) {
	repo := newMemCapabilities()
	r := capabilityRoutes(repo)
	serve(r, http.MethodPut, "/users/admin-1/capabilities/voice")
	serve(r, http.MethodPut, "/users/u-1/capabilities/export_data")

	w := serve(r, http.MethodGet, "/users/me/capabilities")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var body struct {
		Capabilities []domain.CapabilityGrant `json:"capabilities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Capabilities) != 1 || body.Capabilities[0].Capability != domain.CapabilityVoice {
		t.Fatalf("me/capabilities = %+v, want exactly admin-1's voice", body.Capabilities)
	}
}

// Holding nothing is `[]`, not `null`. The dashboard iterates this list, and
// `null.map` is the kind of crash that ships because every tester had a grant.
func TestHoldingNothingIsAnEmptyList(t *testing.T) {
	r := capabilityRoutes(newMemCapabilities())
	w := serve(r, http.MethodGet, "/users/u-1/capabilities")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got, want := w.Body.String(), `{"capabilities":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// The caller's own read says what this deployment can do with voice (T-W9), and
// the admin's read of somebody else's does not. A wiring that never called
// WithVoice reports nothing available, which is the truth about it.
func TestMyCapabilitiesSayWhetherThisDeploymentHasVoice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		voice *VoiceAvailability
		want  VoiceAvailability
	}{
		{"a deployment that hears and does not read aloud",
			&VoiceAvailability{Transcribe: true, MaxClipSeconds: 60},
			VoiceAvailability{Transcribe: true, MaxClipSeconds: 60}},
		{"a wiring that said nothing", nil, VoiceAvailability{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			g := r.Group("/users")
			g.Use(func(c *gin.Context) {
				c.Set("company_id", "co-1")
				c.Set("user_id", "u-1")
			})
			h := NewUserHandler(nil, nil, nil).WithCapabilities(app.NewCapabilityService(newMemCapabilities(), nil))
			if tc.voice != nil {
				h = h.WithVoice(*tc.voice)
			}
			h.Register(g)

			w := serve(r, http.MethodGet, "/users/me/capabilities")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
			var body MyCapabilitiesResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Voice != tc.want {
				t.Errorf("voice = %+v, want %+v", body.Voice, tc.want)
			}
			if body.Capabilities == nil {
				t.Errorf("capabilities decoded as null: %s", w.Body.String())
			}

			other := serve(r, http.MethodGet, "/users/u-1/capabilities")
			if strings.Contains(other.Body.String(), `"voice"`) {
				t.Errorf("the per-person read carries voice: %s", other.Body.String())
			}
		})
	}
}

// Without the service the routes still exist and say why they cannot answer.
func TestCapabilityRoutesWithoutTheServiceAnswer503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/users")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "admin-1")
	})
	NewUserHandler(nil, nil, nil).Register(g)
	if w := serve(r, http.MethodPut, "/users/u-1/capabilities/voice"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
