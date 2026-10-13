package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

type userTable map[string]*domain.User

func (u userTable) GetByID(_ context.Context, id string) (*domain.User, error) {
	if id == "broken" {
		return nil, errors.New("connection refused")
	}
	if user, ok := u[id]; ok {
		return user, nil
	}
	return nil, domain.ErrNotFound
}

// RIZ-83 R9 / AC7: an allow-listed email, case-insensitively, on an active
// account — and nobody when the list is empty.
func TestOperatorOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now()
	users := userTable{
		"admin":       {ID: "admin", Email: "dina@tokosegar.example", Role: domain.RoleAdmin, ActivatedAt: &now},
		"operator":    {ID: "operator", Email: "owner@example.com", Role: domain.RoleMember, ActivatedAt: &now},
		"deactivated": {ID: "deactivated", Email: "owner@example.com", ActivatedAt: &now, DeactivatedAt: &now},
	}
	for _, c := range []struct {
		name      string
		operators []string
		userID    string
		admit     bool
	}{
		{"operator, list in another case", []string{"Owner@Example.com"}, "operator", true},
		{"company admin who is not an operator", []string{"owner@example.com"}, "admin", false},
		{"empty list refuses the operator", nil, "operator", false},
		{"blank entries are not a list", []string{" ", ""}, "operator", false},
		{"deactivated operator", []string{"owner@example.com"}, "deactivated", false},
		{"unknown user", []string{"owner@example.com"}, "ghost", false},
		{"lookup failure", []string{"owner@example.com"}, "broken", false},
		{"no user on the context", []string{"owner@example.com"}, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/op", func(ctx *gin.Context) {
				if c.userID != "" {
					ctx.Set("user_id", c.userID)
				}
				ctx.Next()
			}, OperatorOnly(users, c.operators), func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/op", nil))
			switch {
			case c.admit && w.Code != http.StatusNoContent:
				t.Errorf("refused: %d %s", w.Code, w.Body)
			case !c.admit && (w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"operators only"`)):
				t.Errorf("admitted or wrong refusal: %d %s", w.Code, w.Body)
			}
		})
	}
	// A nil lookup refuses rather than admitting on the token alone.
	r := gin.New()
	r.GET("/op", OperatorOnly(nil, []string{"owner@example.com"}), func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/op", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("nil lookup: %d", w.Code)
	}
}
