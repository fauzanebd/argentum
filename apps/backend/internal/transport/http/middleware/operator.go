package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// OperatorUserLookup is the one question OperatorOnly asks of the user
// repository.
type OperatorUserLookup interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// OperatorOnly admits the platform's operators — the people who review every
// workspace's top-ups (RIZ-83 R9) — and refuses everyone else with 403
// "operators only".
//
// **Not a role.** Roles are company-level, and every signup is the admin of
// its own company: gating approval on admin would let anyone approve their own
// payment. An operator is an email on PLATFORM_OPERATOR_EMAILS, compared
// case-insensitively, and an empty list admits nobody.
//
// **The email is read from the database on every request, not from the JWT.**
// The token carries the user id, company and role, and nothing else; the
// account is loaded so that a deactivated operator is refused at once rather
// than when their token expires. The operator routes are a handful of requests
// a day, so the read costs nothing worth caching.
//
// The routes behind this sit in the role table as RoleMember: the role table
// still runs first (unlisted is refused), and this narrows from there.
func OperatorOnly(users OperatorUserLookup, operators []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(operators))
	for _, e := range operators {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			allowed[e] = true
		}
	}
	return func(c *gin.Context) {
		if len(allowed) == 0 || users == nil {
			refuseOperator(c)
			return
		}
		id := c.GetString("user_id")
		if id == "" {
			refuseOperator(c)
			return
		}
		u, err := users.GetByID(c.Request.Context(), id)
		if err != nil || u == nil {
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				logrus.WithError(err).Warn("operator check could not load the user; refusing")
			}
			refuseOperator(c)
			return
		}
		if !u.Active() || !allowed[strings.ToLower(strings.TrimSpace(u.Email))] {
			refuseOperator(c)
			return
		}
		c.Next()
	}
}

func refuseOperator(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "operators only"})
}
