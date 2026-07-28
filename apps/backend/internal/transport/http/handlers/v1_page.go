package handlers

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
)

// Query-parameter parsing shared by every cursor-paginated `/v1` listing
// (T-A2's documents, T-A3's threads and messages).
//
// One copy because the failure messages are part of the contract: two listings
// answering differently to the same malformed `limit` is a difference an
// integrator has to discover by hitting both.

// parseLimitParam reads `?limit=`. It returns false when it has already
// written the envelope.
//
// An over-large limit is *not* rejected here — the repositories clamp it —
// because a caller asking for 10 000 rows wants as many as they can get, and
// the cursor is what makes the rest reachable. Only a limit that is not a
// positive integer is a mistake worth stopping for.
func parseLimitParam(c *gin.Context) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_limit",
			"`limit` must be a positive integer.", "limit")
		return 0, false
	}
	return n, true
}

// parseCursorParam reads `?cursor=`. It returns false when it has already
// written the envelope.
//
// A cursor that will not decode is refused rather than treated as absent. The
// alternative hands the caller page one, which reads as the walk silently
// restarting — and a walk that restarts forever is a loop nobody notices until
// the bill arrives.
func parseCursorParam(c *gin.Context) (time.Time, string, bool) {
	raw := c.Query("cursor")
	if raw == "" {
		return time.Time{}, "", true
	}
	t, id, err := decodeCursorOrAbort(c, raw, "cursor")
	return t, id, err
}

// decodeCursorOrAbort is the shared decode-and-explain step. param names the
// input in the error, so the same helper serves `?cursor=` and the
// `Last-Event-ID` header a resumed stream sends.
func decodeCursorOrAbort(c *gin.Context, raw, param string) (time.Time, string, bool) {
	t, id, err := apiv1.DecodeCursor(raw)
	if err != nil {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_cursor",
			"That `"+param+"` is not one this API issued. Pass back the value this API gave you.", param)
		return time.Time{}, "", false
	}
	return t, id, true
}
