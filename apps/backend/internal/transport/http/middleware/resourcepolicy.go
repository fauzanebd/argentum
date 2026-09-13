package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ResourceRoute says which restrictable object a route serves and where its id
// is: the kind, and the name of the path parameter that carries the id.
type ResourceRoute struct {
	Kind  domain.ResourceKind
	Param string
}

// ResourcePolicy maps a route to the resource it serves (T-Z3). Keys are
// RolePolicy's — `METHOD path`, gin's registered pattern — so the three tables
// can be diffed against each other and against the router.
//
// It is the third question a route can ask, and the first one about an object
// rather than about the caller. RolePolicy asks whether their rank is high
// enough; CapabilityPolicy asks whether they were granted a power; this asks
// whether they may reach *this* agent, dashboard, source or document.
//
// One resource per route. A route whose path names two restrictable ids is
// declared on the one it serves, and the other is a handler's check — the
// classification test in cmd/api is what makes somebody decide which.
type ResourcePolicy map[string]ResourceRoute

// ResourceAuthorizer is the narrow contract RequireResource decides through.
// *authz.Authorizer is the production one; the interface is declared here, where
// it is consumed, for CapabilityChecker's reason.
type ResourceAuthorizer interface {
	Decide(ctx context.Context, s authz.Subject, kind domain.ResourceKind, id string) (authz.Decision, error)
}

// RequireResource enforces a resource policy across a router group. Apply after
// RequireCapability and before the rate limiter.
//
// Like RequireCapability it only ever adds a requirement: an unlisted route is
// admitted, because most routes serve no restrictable object, and what keeps
// that from being a hole is the classification test rather than a default. A
// request reaches this middleware only once the role table and the capability
// table have both said yes, so a grant can narrow that yes and never turn a no
// into one — and a request refused for a missing grant spends no rate-limit
// token, which is T-04's rule carried one layer down.
//
// The decision is internal/authz's, all of it (roadmap 12, decision 1). This
// middleware reads the id out of the path, asks, and turns the answer into a
// status; it composes nothing itself, so an admin is refused exactly like a
// member (decision 4) without a line here that could say otherwise.
//
// Three answers, three different responses, and the middle one is deliberate:
//
//   - Allowed: the handler runs.
//   - Not found — no such object of this kind in the caller's company, or an id
//     that is not an id at all: **the handler runs too.** Every handler behind a
//     listed route looks the object up scoped to the company, so it finds
//     nothing and answers with whatever not-found it answers today — its own
//     status and its own body. Refusing here instead would change a 404 into a
//     403 on every mistyped URL, and would give a caller probing another
//     company's ids a second, differently-shaped answer to compare against the
//     handler's. authz.ReasonNotFound's own comment asks for exactly this.
//   - Not granted: 403, naming the kind, so the dashboard can say what to ask an
//     admin for rather than rendering a refusal that reads like the role table's.
//
// An authorizer that fails refuses with a 503, for RequireCapability's reason: a
// caller who may well hold the grant should retry, not be told they do not.
func RequireResource(policy ResourcePolicy, authorizer ResourceAuthorizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		route, listed := policy[RouteKey(c.Request.Method, c.FullPath())]
		if !listed {
			c.Next()
			return
		}

		cv, _ := c.Get("company_id")
		uv, _ := c.Get("user_id")
		rv, _ := c.Get("role")
		companyID, _ := cv.(string)
		userID, _ := uv.(string)
		role, _ := rv.(string)
		id := c.Param(route.Param)
		// No identity means Auth did not run ahead of this; a nil authorizer is
		// a wiring that never built one; an empty id is a policy entry naming a
		// parameter its route does not declare, which the classification test
		// exists to stop and which must still fail closed if it ships. All three
		// are mistakes in the chain rather than answers about the caller, and
		// all three refuse.
		if companyID == "" || userID == "" || authorizer == nil || id == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		subject := authz.Subject{CompanyID: companyID, UserID: userID, Role: domain.Role(role)}
		decision, err := authorizer.Decide(c.Request.Context(), subject, route.Kind, id)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id":    companyID,
				"user_id":       userID,
				"resource_kind": route.Kind,
				"resource_id":   id,
			}).Warn("resource access check failed; refusing the request")
			AbortAccessCheckFailed(c)
			return
		}
		if decision.Allowed || decision.Reason == authz.ReasonNotFound {
			c.Next()
			return
		}
		AbortNotGranted(c, route.Kind)
	}
}

// AbortNotGranted refuses a request for a resource the person is not granted:
// 403, naming the kind, so the dashboard can say what to ask an admin for.
//
// Exported for the handlers that have to ask in their own body — a list, which
// has no id for a route entry to name, and an object served under a child's id,
// like a document's extracted table (T-Z6) — so a refusal reads the same whether
// the table or the handler decided it.
func AbortNotGranted(c *gin.Context, kind domain.ResourceKind) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error":         "this is restricted, and an admin has not granted it to you",
		"resource_kind": kind,
	})
}

// AbortAccessCheckFailed refuses a request whose access could not be read: 503,
// a retry, and never a refusal a person might go and ask a grant for.
func AbortAccessCheckFailed(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
		"error": "could not check access; try again",
	})
}
