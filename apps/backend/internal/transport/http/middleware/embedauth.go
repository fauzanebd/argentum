package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// Gin context keys set by EmbedAuth (T-19).
//
// **`user_id` and `role` are deliberately absent.** They are absent from
// APIKeyAuth for the same reason and it matters more here: an embed session
// belongs to somebody who has no account with us at all. If this middleware set
// a role, RequireRole would start admitting visitors of a tenant's website to
// routes the policy table believes are staff-only; if it set a user id, every
// handler that reads one would attribute a stranger's turn to whichever real
// user that id happened to name.
const (
	CtxEmbedUserRef = "embed_user_ref"
	CtxEmbedKeyID   = "embed_key_id"
)

// EmbedVerifier is the narrow half of auth.TokenSigner this middleware needs,
// declared here so a test can drive the chain without a signing secret.
type EmbedVerifier interface {
	VerifyEmbed(raw string) (*auth.EmbedClaims, error)
}

// EmbedAuth enforces a valid embed session token on every route in the group.
//
// The token arrives as `Authorization: Bearer <jwt>` and, for the WebSocket
// route T-20 registers, as `?et=` — a browser cannot set a header on an upgrade
// request, which is the same exemption the dashboard's own stream route has.
// The query parameter is deliberately named differently from the dashboard's
// `at`: two token families that cannot be substituted for each other should not
// share a parameter name either, or a copy-paste between the two client
// codebases silently produces a request that authenticates as the wrong thing.
//
// On success it annotates the request context with the company and with
// `actor_kind=embed` / `actor_ref=<embed_user_ref>`, which is what makes T-05's
// audit rows attribute a widget turn to the visitor the tenant asserted rather
// than to nobody.
func EmbedAuth(v EmbedVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "embedding is not configured on this deployment",
			})
			return
		}
		raw := embedToken(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing embed session"})
			return
		}

		claims, err := v.VerifyEmbed(raw)
		if err != nil {
			// One message for expired, malformed, wrong-typ and wrong-signature.
			// The widget's only correct reaction to any of them is the same:
			// ask the host page to re-sign.
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid embed session"})
			return
		}

		c.Set("company_id", claims.CompanyID)
		c.Set(CtxEmbedUserRef, claims.EmbedUserRef)
		c.Set(CtxEmbedKeyID, claims.KeyID)

		ctx := tenantctx.WithCompanyID(c.Request.Context(), claims.CompanyID)
		ctx = tenantctx.WithActor(ctx, string(domain.ActorKindEmbed), claims.EmbedUserRef)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// EmbedUserRef returns the visitor the session asserts, or "". Handlers use it
// to scope every read to one person — the check that stops a widget user
// reading a colleague's thread by id.
func EmbedUserRef(c *gin.Context) string {
	v, ok := c.Get(CtxEmbedUserRef)
	if !ok {
		return ""
	}
	ref, _ := v.(string)
	return ref
}

// embedToken reads the session token from the Authorization header, then from
// `?et=`. No cookie: a cookie on a third-party origin is either blocked by the
// browser or a CSRF surface, and the widget has a token in memory either way.
func embedToken(c *gin.Context) string {
	if t := bearerToken(c); t != "" {
		return t
	}
	return c.Query("et")
}
