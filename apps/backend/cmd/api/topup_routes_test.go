package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// The operator gate against the real router (RIZ-83 R9, AC7).

// operatorUsers is the user table OperatorOnly reads: a company admin who is
// not an operator, and an operator who is a plain member of their own
// workspace — the case that shows the gate is not a role.
type operatorUsers map[string]*domain.User

func (u operatorUsers) GetByID(_ context.Context, id string) (*domain.User, error) {
	if user, ok := u[id]; ok {
		return user, nil
	}
	return nil, domain.ErrNotFound
}

func activeUser(id, email string, role domain.Role) *domain.User {
	at := time.Now()
	return &domain.User{ID: id, CompanyID: "co-1", Email: email, Role: role, ActivatedAt: &at}
}

var gateUsers = operatorUsers{
	"user-1":  activeUser("user-1", "dina@tokosegar.example", domain.RoleAdmin),
	"user-op": activeUser("user-op", "owner@example.com", domain.RoleMember),
}

func operatorRouterWith(t *testing.T, operators []string) http.Handler {
	t.Helper()
	return routerWithDeps(t, func(d *apiDeps) {
		d.operatorOnly = middleware.OperatorOnly(gateUsers, operators)
	})
}

func TestOperatorRoutesRefuseEveryoneElse(t *testing.T) {
	// Mixed case on purpose: the comparison is case-insensitive (AC7).
	r := operatorRouterWith(t, []string{"Owner@Example.com"})
	admin, operator := tokenFor(t, "user-1", "admin"), tokenFor(t, "user-op", "member")

	for key := range operatorRoutes {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			code, body := serveWith(r, method, concreteURL(path), admin)
			if code != http.StatusForbidden || body["error"] != "operators only" {
				t.Errorf("company admin who is not an operator: %d %v, want 403 operators only", code, body)
			}
			// The handler has no service in this router, so getting past the
			// gate is a 503 — anything but the gate's 403.
			if code, body := serveWith(r, method, concreteURL(path), operator); code == http.StatusForbidden {
				t.Errorf("operator (a member of their own workspace) was refused: %d %v", code, body)
			}
		})
	}
}

func TestOperatorRoutesRefuseEveryoneWithAnEmptyList(t *testing.T) {
	r := operatorRouterWith(t, nil)
	operator := tokenFor(t, "user-op", "member")
	for key := range operatorRoutes {
		method, path, _ := strings.Cut(key, " ")
		if code, body := serveWith(r, method, concreteURL(path), operator); code != http.StatusForbidden || body["error"] != "operators only" {
			t.Errorf("%s with no operators configured: %d %v, want 403 operators only", key, code, body)
		}
	}
}

// Every route under /api/operator is in operatorRoutes and the other way
// round, so a new operator route cannot skip the test above.
func TestOperatorRoutesAreExactlyTheOperatorGroup(t *testing.T) {
	r := realRouter(t)
	registered := map[string]bool{}
	for _, ri := range r.Routes() {
		if strings.HasPrefix(ri.Path, "/api/operator/") {
			key := middleware.RouteKey(ri.Method, ri.Path)
			registered[key] = true
			if !operatorRoutes[key] {
				t.Errorf("%s is under /api/operator but not in operatorRoutes", key)
			}
		}
	}
	for key := range operatorRoutes {
		if !registered[key] {
			t.Errorf("operatorRoutes lists %s, which the router does not register", key)
		}
	}
}

// The tenant's routes are a company admin's: a member is refused by the role
// table with its own sentence (AC14).
func TestTopupRoutesRefuseMembers(t *testing.T) {
	r := realRouter(t)
	member := tokenFor(t, "user-2", "member")
	for _, key := range []string{
		"GET /api/topups/options", "GET /api/topups", "POST /api/topups",
		"POST /api/topups/:id/proof", "POST /api/topups/:id/cancel",
	} {
		method, path, _ := strings.Cut(key, " ")
		if code, body := serveWith(r, method, concreteURL(path), member); code != http.StatusForbidden || body["error"] != "admin only" {
			t.Errorf("%s as a member: %d %v, want 403 admin only", key, code, body)
		}
	}
}

func serveWith(r http.Handler, method, url, token string) (int, map[string]any) {
	req := httptest.NewRequest(method, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}
