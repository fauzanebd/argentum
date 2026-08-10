package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// EmbedSessionHandler is the public door of T-19: a tenant's page presents
// their backend's identity assertion and gets a short-lived session token.
//
// **These two routes are not behind middleware.Auth and must never be.** The
// caller has no Argentum account by definition — that is the entire point of
// embedding. What stands in for authentication is the three-part check in
// app.EmbedKeyService.MintSession: a known key, an allowlisted origin, and an
// HMAC only the tenant's backend can compute.
type EmbedSessionHandler struct {
	svc *app.EmbedKeyService
}

func NewEmbedSessionHandler(svc *app.EmbedKeyService) *EmbedSessionHandler {
	return &EmbedSessionHandler{svc: svc}
}

// Register installs the mint and the refresh on a group that has no auth
// middleware on it.
//
// Refresh is a separate route from mint even though the two do exactly the same
// work, and deliberately so: the host page's two calls mean different things
// ("start" and "keep going"), and the day one of them needs to diverge — a
// counter, a different rate bucket, a deprecation — a shared route would have
// to grow a mode flag to tell them apart.
func (h *EmbedSessionHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/session", h.mint)
	rg.POST("/session/refresh", h.mint)
}

type embedSessionReq struct {
	ClientKey string `json:"client_key" binding:"required"`
	UserRef   string `json:"user_ref" binding:"required"`
	// Exp is the unix timestamp the tenant's backend signed over.
	Exp int64 `json:"exp" binding:"required"`
	// Signature is hex `HMAC-SHA256(secret, "<user_ref>:<exp>")`.
	Signature string `json:"signature" binding:"required"`
}

type embedSessionResp struct {
	Token string `json:"token"`
	// ExpiresAt lets the host page schedule its own re-sign instead of waiting
	// for the first 401. A widget that only learns its session died by being
	// refused shows the user an error it could have avoided.
	ExpiresAt string `json:"expires_at"`
	// ExpiresInSeconds is the same fact for a client that would rather not
	// parse a timestamp, and it is immune to a host whose clock is wrong.
	ExpiresInSeconds int `json:"expires_in_seconds"`
}

// mint answers a session request. Three outcomes and three statuses:
//
//	401 — the key is unknown, revoked or disabled, or the identity material
//	      does not check out. One message for all of it; the server log has the
//	      distinction.
//	403 — the key is fine and the Origin is not on its allowlist. Distinct
//	      because the caller has already proved they hold a valid client key,
//	      and because "add this origin in Settings" is the entire fix.
//	200 — a token.
func (h *EmbedSessionHandler) mint(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding is not configured on this deployment"})
		return
	}

	var req embedSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_key, user_ref, exp and signature are all required"})
		return
	}

	sess, err := h.svc.MintSession(c.Request.Context(), app.SessionRequest{
		ClientKey: req.ClientKey,
		// The browser's own Origin header, never a field of the body. A page
		// that could name its own origin could name somebody else's.
		Origin:    c.GetHeader("Origin"),
		UserRef:   req.UserRef,
		Exp:       req.Exp,
		Signature: req.Signature,
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrEmbedOriginNotAllowed):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "This page's origin is not allowed for that embed key. Add it in Settings → Embed.",
			})
		case errors.Is(err, app.ErrEmbedKeyUnusable), errors.Is(err, app.ErrEmbedIdentityRejected):
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "That embed session request was rejected.",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not issue an embed session."})
		}
		return
	}

	c.JSON(http.StatusOK, embedSessionResp{
		Token:            sess.Token,
		ExpiresAt:        sess.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		ExpiresInSeconds: int(h.svc.SessionTTL().Seconds()),
	})
}
