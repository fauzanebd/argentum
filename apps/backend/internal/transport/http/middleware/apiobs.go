package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

// APIRequestSink is the narrow half of apiobs.Recorder this middleware needs.
// Declared at the consumer, like APIKeyAuthenticator above it, so the
// middleware package keeps its short import list and a test can count samples
// without a database.
type APIRequestSink interface {
	Record(domain.APIRequestSample)
}

// RecordAPIRequests observes every `/v1` response and hands it to the recorder
// (T-A5).
//
// **Where it goes in the chain matters, and it is not first.** It sits below
// RequestID (so the sample carries the id the caller was given) and above
// APIKeyAuth (so a 401 is still counted), and it reads the key identity
// *after* `c.Next()` returns — by then the authenticator downstream has set it,
// or the request never authenticated and there is nothing to attribute. That
// ordering is what lets one middleware cover both the failures a tenant can be
// shown and the ones only an operator can.
//
// The kill switch (Enabled) stays above this: a 503 from a switched-off API is
// not a fact about anybody's integration.
//
// Nothing here can fail the request. A nil sink makes it a pass-through, which
// is what keeps the wiring in cmd/api free of a conditional install.
func RecordAPIRequests(sink APIRequestSink) gin.HandlerFunc {
	if sink == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()

		code, errType := apierr.Recorded(c)
		sink.Record(domain.APIRequestSample{
			// Empty for a request that never authenticated. The recorder keeps
			// those out of the database on purpose — see its Record doc.
			CompanyID: c.GetString("company_id"),
			APIKeyID:  c.GetString(CtxAPIKeyID),
			RequestID: c.GetString(CtxRequestID),
			Method:    c.Request.Method,
			// FullPath is the route pattern and is empty when nothing matched.
			// The concrete path is deliberately not the fallback: recording it
			// would make the label set a function of what a scanner guessed.
			Route:     c.FullPath(),
			Status:    c.Writer.Status(),
			ErrorCode: code,
			ErrorType: errType,
			Latency:   time.Since(started),
			At:        started,
		})
	}
}
