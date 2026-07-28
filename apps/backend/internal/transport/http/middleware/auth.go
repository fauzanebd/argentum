// Package middleware contains Gin middleware for the Argentum API.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// Auth returns a Gin middleware that enforces a valid JWT access token. On
// success it sets userID/companyID/role on the Gin context AND on the
// underlying request context (so downstream tools see them via tenantctx).
func Auth(signer *auth.TokenSigner) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := signer.Verify(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if claims.TokenType != "access" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "wrong token type"})
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("company_id", claims.CompanyID)
		c.Set("role", claims.Role)

		ctx := c.Request.Context()
		ctx = tenantctx.WithCompanyID(ctx, claims.CompanyID)
		ctx = tenantctx.WithUserID(ctx, claims.UserID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// AdminOnly is a middleware factory that rejects non-admin requests. Apply
// after Auth.
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if role, _ := c.Get("role"); role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		c.Next()
	}
}

// extractToken reads the access token from, in order:
//  1. The Authorization "Bearer …" header (default for fetch/axios calls).
//  2. The `?at=` query parameter (used by the WebSocket endpoint where the
//     browser cannot set a custom header on the upgrade request).
//  3. The `at` cookie (legacy fallback; not currently set by the dashboard).
func extractToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if q := c.Query("at"); q != "" {
		return q
	}
	if cookie, err := c.Cookie("at"); err == nil && cookie != "" {
		return cookie
	}
	return ""
}
