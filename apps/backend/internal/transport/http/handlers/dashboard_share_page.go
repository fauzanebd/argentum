package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// DashboardSharePageHandler serves a shared dashboard to a visitor with no
// session (T-D13).
//
// **Mounted at `/share/dashboard/:token`, not `/share/:token`.** That path is
// taken by the report player, and gin panics on a conflicting wildcard at the
// same position. Sitting inside the existing `/share` group means this
// inherits its rate limiter for free, which is the right default for a route
// anyone on the internet may call.
//
// **No HTML is rendered here.** The page is the dashboard SPA's own route, the
// way the report player's is. An HTML template in Go would be a second frontend
// drawn with a second set of tokens, and it would drift.
type DashboardSharePageHandler struct{ svc *app.DashboardShareService }

func NewDashboardSharePageHandler(svc *app.DashboardShareService) *DashboardSharePageHandler {
	return &DashboardSharePageHandler{svc: svc}
}

func (h *DashboardSharePageHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/dashboard/:token", h.get)
}

func (h *DashboardSharePageHandler) get(c *gin.Context) {
	// Every header goes out before anything can return, including the
	// refusals: a cacheable 404 keeps being served after a share is fixed, and
	// an indexable one publishes the shape of the URL space. The ordering is
	// the property, not the presence.
	//
	// `private, no-store` is what makes revocation mean something — a CDN or a
	// corporate proxy holding a copy would serve a link that has been taken
	// back, from a machine we cannot reach.
	c.Header("Cache-Control", "private, no-store, max-age=0")
	c.Header("X-Robots-Tag", "noindex, nofollow, noarchive")
	// The frame policy is new to this product — a whole-tree search found no
	// X-Frame-Options or frame-ancestors anywhere in Go before T-D13. A shared
	// dashboard must not be framable, because a page that can be framed can be
	// clickjacked into a screenshot of somebody's revenue.
	//
	// Deliberately NOT generalised from the widget's iframe story
	// (internal/auth/embedkey.go): that surface is meant to be embedded and
	// carries its own origin allowlist. This one is meant to be opened.
	c.Header("X-Frame-Options", "DENY")
	c.Header("Content-Security-Policy", "frame-ancestors 'none'")

	if h.svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "This link is not available."})
		return
	}

	// Query parameters reach the service, which decides whether they matter.
	// They are ignored unless the share allows filters, and can never override
	// a pinned value — enforced in the domain, not here, so a second caller
	// cannot get it wrong.
	requested := map[string]string{}
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 && k != "password" {
			requested[k] = v[0]
		}
	}

	out, err := h.svc.Open(c.Request.Context(), c.Param("token"), c.Query("password"), requested)
	switch {
	case err == nil:
	case errors.Is(err, app.ErrSharePassword):
		// 401 with a marker the SPA can branch on to show a password box. The
		// visitor already holds a valid token, so saying "this needs a
		// password" tells them nothing they could not infer.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "This link needs a password.", "password_required": true})
		return
	case errors.Is(err, app.ErrShareBudget):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "This link has been refreshed too many times. Try again later."})
		return
	default:
		// One answer for unknown, expired, revoked and deleted, so the route
		// cannot be used as an oracle by somebody trying tokens.
		c.JSON(http.StatusNotFound, gin.H{"error": "This link is not available."})
		return
	}
	c.JSON(http.StatusOK, out)
}
