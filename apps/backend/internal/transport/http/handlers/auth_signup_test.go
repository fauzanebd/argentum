package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-76 R8 / Q14: signup's 500 says nothing about why. The 400 and 409 bodies
// are what the signup page maps to field errors, so they must not move.

// signupUsers answers GetByEmail with a fixture. Every other method is the
// embedded nil interface, because signup reaches none of them before the
// failures under test.
type signupUsers struct {
	domain.UserRepository
	existing *domain.User
}

func (u signupUsers) GetByEmail(context.Context, string) (*domain.User, error) {
	if u.existing != nil {
		return u.existing, nil
	}
	return nil, domain.ErrNotFound
}

// signupCompanies refuses every insert with err.
type signupCompanies struct {
	domain.CompanyRepository
	err error
}

func (c signupCompanies) Create(context.Context, *domain.Company) error { return c.err }

func signupRouter(t *testing.T, users signupUsers, companies signupCompanies) *gin.Engine {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewAuthHandler(app.NewAuthService(companies, users, signer), nil, false, time.Hour).
		Register(r.Group("/auth"))
	return r
}

func TestSignupFailureBodies(t *testing.T) {
	driverErr := errors.New(`pq: duplicate key value violates unique constraint "companies_slug_key"`)
	cases := []struct {
		name      string
		users     signupUsers
		companies signupCompanies
		password  string
		wantCode  int
		wantBody  string
	}{
		{
			name:      "an unexpected error is a generic 500 (AC9)",
			companies: signupCompanies{err: driverErr},
			password:  "correct-horse",
			wantCode:  http.StatusInternalServerError,
			wantBody:  `{"error":"could not create the account, please try again"}`,
		},
		{
			name:      "an email in use is still 409 with its own message",
			users:     signupUsers{existing: &domain.User{ID: "u-1", Email: "dewi@acme.id"}},
			companies: signupCompanies{err: driverErr},
			password:  "correct-horse",
			wantCode:  http.StatusConflict,
			wantBody:  `{"error":"email already in use"}`,
		},
		{
			name:      "a short password is still 400 with the service's message",
			companies: signupCompanies{err: driverErr},
			password:  "short",
			wantCode:  http.StatusBadRequest,
			wantBody:  `{"error":"invalid input: password must be at least 8 characters"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"company_name":"Toko Segar","email":"dewi@acme.id","password":"` + tc.password + `"}`
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/auth/signup", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			signupRouter(t, tc.users, tc.companies).ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantCode, w.Body)
			}
			if got := w.Body.String(); got != tc.wantBody {
				t.Errorf("body = %s\nwant   %s", got, tc.wantBody)
			}
			for _, leak := range []string{"pq:", "companies_slug_key", "create company"} {
				if strings.Contains(w.Body.String(), leak) {
					t.Errorf("the response leaked %q: %s", leak, w.Body)
				}
			}
		})
	}
}
