package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RolePolicy maps a route to the minimum role allowed to call it. Keys are
// `METHOD path`, where path is gin's registered pattern — the same string
// c.FullPath() returns, parameters and all: "DELETE /api/connections/:id".
type RolePolicy map[string]domain.Role

// RouteKey builds the policy key for a method and registered path.
func RouteKey(method, path string) string { return method + " " + path }

// RequireRole enforces a policy across a whole router group.
//
// The alternative — sprinkling AdminOnly() through each handler's Register —
// puts the answer to "which routes are privileged?" in a dozen files and makes
// it unenumerable: gin's RouteInfo exposes the final handler, not the chain,
// so no test can read the gating back out of a built router. A table can be
// diffed against the real route list, which is what
// TestEveryAuthedRouteIsClassified in cmd/api does.
//
// Unlisted routes are denied. A route added without a decision fails loudly on
// its first request instead of shipping open, and the classification test turns
// that runtime failure into a build-time one.
func RequireRole(policy RolePolicy) gin.HandlerFunc {
	return func(c *gin.Context) {
		want, listed := policy[RouteKey(c.Request.Method, c.FullPath())]
		if !listed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "this route has no access policy",
			})
			return
		}

		v, _ := c.Get("role")
		s, _ := v.(string)
		have := domain.Role(s)
		// An unrecognised role — including the empty one left when Auth did
		// not run ahead of this middleware — is refused even on a member
		// route. The failure mode of a misordered chain has to be closed, and
		// checking only on admin routes would leave the rest open.
		if !have.Valid() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if want == domain.RoleAdmin && have != domain.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		c.Next()
	}
}

// AdminOnly is a middleware factory that rejects non-admin requests. Apply
// after Auth. RequireRole is what the API uses; this stays for one-off routes
// registered outside a policed group.
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if role, _ := c.Get("role"); role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		c.Next()
	}
}
