package middleware

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

// Gin context keys set by APIKeyAuth. They are distinct from the ones Auth
// sets on purpose: `role` is never among them, so RequireRole cannot admit an
// API key by accident, and any handler that reads `user_id` gets an empty
// string rather than a plausible-looking identity for a caller that is not a
// person.
const (
	CtxAPIKeyID     = "api_key_id"
	CtxAPIKeyName   = "api_key_name"
	CtxAPIKeyScopes = "api_key_scopes"
)

// APIKeyAuthenticator is the narrow half of app.APIKeyService this middleware
// needs. Declared here so the middleware package does not import internal/app,
// and so a test can drive it without a database.
type APIKeyAuthenticator interface {
	Authenticate(ctx context.Context, token string) (*domain.APIKey, error)
}

// APIKeyAuth enforces a valid `Authorization: Bearer arg_<prefix>_<secret>`
// on every route in the group.
//
// **Header only.** Auth accepts the dashboard's token from a query parameter
// and a cookie as well, because a browser cannot set a header on a WebSocket
// upgrade. Neither applies here, and both are how a credential ends up in an
// access log, a proxy trace or a referer. A machine caller can always set a
// header.
//
// On success it annotates two things:
//
//   - the Gin context, for handlers and the per-key rate limiter;
//   - the request context, with the company and with
//     `actor_kind=api_key` / `actor_ref=<key id>` — which is what makes T-05's
//     audit rows attribute a tool call to an integration rather than to a
//     person who was not there.
func APIKeyAuth(a APIKeyAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		if a == nil {
			apierr.Abort(c, apierr.TypeServer, "api_keys_unavailable",
				"API key authentication is not configured on this deployment.")
			return
		}
		raw := bearerToken(c)
		if raw == "" {
			apierr.Abort(c, apierr.TypeAuthentication, "missing_api_key",
				"Send your key as `Authorization: Bearer arg_…`.")
			return
		}

		key, err := a.Authenticate(c.Request.Context(), raw)
		if err != nil {
			if errors.Is(err, domain.ErrUnauthorized) {
				// One message for malformed, unknown, wrong-secret, revoked
				// and expired. A caller holding a broken credential has no
				// business learning which of the five it is; the server log
				// records the difference.
				apierr.Abort(c, apierr.TypeAuthentication, "invalid_api_key",
					"That API key is not valid, or it has been revoked or expired.")
				return
			}
			apierr.Abort(c, apierr.TypeServer, "auth_unavailable",
				"Could not verify that API key. Try again.")
			return
		}

		c.Set("company_id", key.CompanyID)
		c.Set(CtxAPIKeyID, key.ID)
		c.Set(CtxAPIKeyName, key.Name)
		c.Set(CtxAPIKeyScopes, key.Scopes)

		ctx := tenantctx.WithCompanyID(c.Request.Context(), key.CompanyID)
		ctx = tenantctx.WithActor(ctx, string(domain.ActorKindAPIKey), key.ID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// RequireScope gates one route on one capability. Deny by default is a
// property of how it is applied: a `/v1` route with no RequireScope reaches
// every key the tenant has ever minted, so the review rule is that every
// route names its scope — the same rule T-04's policy table enforces for
// roles, at the one place a table cannot reach because scopes are per-key
// rather than per-role.
func RequireScope(want domain.Scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(CtxAPIKeyScopes)
		if !ok {
			// APIKeyAuth did not run ahead of this middleware. That is a
			// wiring bug, and the failure mode of a misordered chain has to be
			// closed rather than open.
			apierr.Abort(c, apierr.TypeAuthentication, "missing_api_key",
				"Send your key as `Authorization: Bearer arg_…`.")
			return
		}
		scopes, _ := v.([]domain.Scope)
		if slices.Contains(scopes, want) {
			c.Next()
			return
		}
		apierr.Abort(c, apierr.TypePermission, "insufficient_scope",
			"This key does not have the `"+string(want)+"` scope. Create a new key with it — scopes cannot be changed after a key is minted.")
	}
}

// bearerToken reads the Authorization header and nothing else.
func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// APIKeyScopes returns the scopes carried by the authenticated key, or nil.
// Handlers use it to make a scope-dependent decision inside one route;
// gating a whole route is RequireScope's job.
func APIKeyScopes(c *gin.Context) []domain.Scope {
	v, ok := c.Get(CtxAPIKeyScopes)
	if !ok {
		return nil
	}
	scopes, _ := v.([]domain.Scope)
	return scopes
}
