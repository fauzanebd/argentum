package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// CapabilityPolicy maps a route to the capability a caller must hold to reach
// it (T-Z1). Keys are RolePolicy's — `METHOD path`, gin's registered pattern —
// so the two tables can be diffed against each other and against the router.
//
// One capability per route. A route that genuinely needed two would be two
// powers pretending to be one, and the vocabulary is the place to fix that.
type CapabilityPolicy map[string]domain.Capability

// CapabilityChecker is the narrow contract RequireCapability reads grants
// through. app.CapabilityService is the production one; the interface is
// declared here, where it is consumed, so this package does not import the
// service layer.
type CapabilityChecker interface {
	Has(ctx context.Context, companyID, userID string, c domain.Capability) (bool, error)
}

// RequireCapability enforces a capability policy across a router group. Apply
// after RequireRole.
//
// It only ever adds a requirement, which makes its default the opposite of
// RequireRole's, deliberately. An unlisted route is *denied* by RequireRole,
// because every route needs a role decision; it is *admitted* here, because
// most routes ask for no capability, and a capability table that had to list
// all of them would be the role table again. What keeps that from being a hole
// is the order: a request reaches this middleware only after the role table has
// said yes, so a capability can narrow that yes and can never turn a no into
// one.
//
// An admin is checked exactly like a member. A rank is not a grant (roadmap 12,
// decision 4); an admin who wants a capability grants it to themselves, which
// is allowed and is recorded.
//
// A checker that fails refuses the request rather than admitting it — with a
// 503 so the caller retries, not a 403 that would tell them they lack a grant
// they may well hold.
func RequireCapability(policy CapabilityPolicy, checker CapabilityChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		want, listed := policy[RouteKey(c.Request.Method, c.FullPath())]
		if !listed {
			c.Next()
			return
		}

		cv, _ := c.Get("company_id")
		uv, _ := c.Get("user_id")
		companyID, _ := cv.(string)
		userID, _ := uv.(string)
		// No user on the context means Auth did not run ahead of this — a
		// misordered chain — and a nil checker means a wiring that never built
		// the service. Both refuse, for RequireRole's reason: a mistake in the
		// chain has to fail closed.
		if companyID == "" || userID == "" || checker == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		held, err := checker.Has(c.Request.Context(), companyID, userID, want)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID,
				"user_id":    userID,
				"capability": want,
			}).Warn("capability check failed; refusing the request")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "could not check access; try again",
			})
			return
		}
		if !held {
			// The capability is named so the dashboard can say which grant to
			// ask an admin for (T-Z7), rather than a bare "forbidden" that
			// sends a member hunting for which of their permissions is wrong.
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":      "an admin has not granted you this",
				"capability": want,
			})
			return
		}
		c.Next()
	}
}
