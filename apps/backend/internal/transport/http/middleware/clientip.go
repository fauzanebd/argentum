package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// TrustProxies decides whose word the engine takes on who a request came
// from. Everything that calls c.ClientIP() reads the answer: the signup,
// /share and embed-mint limiters, the share page's view record, and the
// request log (RIZ-79 review F1).
//
// gin's own default believes every peer, which makes c.ClientIP() the
// leftmost X-Forwarded-For entry — the one the client writes. With a
// trusted list, gin reads the header from the right and stops at the first
// address that is not a trusted proxy, which is the one the proxy appended.
//
// Only X-Forwarded-For is read. gin also consults X-Real-IP by default, and
// neither shipped proxy sets it, so through the compose nginx it would arrive
// exactly as the client wrote it.
//
// An empty list trusts nobody: the socket's peer is the client. So does a
// list gin refuses — failing closed, so a broken setting costs one shared
// bucket rather than reopening the header. config.Validate rejects such a
// list before this is reached; the fallback is for the caller that skipped it.
func TrustProxies(r *gin.Engine, proxies []string) {
	r.ForwardedByClientIP = true
	r.RemoteIPHeaders = []string{"X-Forwarded-For"}
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		logrus.WithError(err).Error("TRUSTED_PROXIES rejected; trusting no proxy, so every client behind one is one address")
		_ = r.SetTrustedProxies(nil)
	}
}
