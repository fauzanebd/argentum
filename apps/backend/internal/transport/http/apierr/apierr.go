// Package apierr is the typed error envelope every `/v1` response uses.
//
// **Scope note.** The envelope is `T-A1`'s design and `T-A1` owns the rest of
// it — `param`, the request-id middleware that fills `request_id`, and the
// handler-side helpers. What is here is the half `T-13`'s authentication
// middleware cannot ship without: the first thing a `/v1` route ever answers
// is an auth failure, and `T-A1`'s acceptance says in as many words that a
// bare `{"error":"…"}` anywhere under `/v1` is a defect. Writing one now and
// replacing it later would mean shipping the exact shape that ticket forbids.
//
// The status code lives in one table rather than at each call site, because
// two handlers disagreeing about whether a missing scope is 401 or 403 is a
// contract bug that no test would catch.
package apierr

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Type is the coarse class of an error. A client switches on this; `Code` is
// the specific reason, and only `Message` is meant for a human.
type Type string

const (
	// TypeInvalidRequest — the caller sent something malformed.
	TypeInvalidRequest Type = "invalid_request"
	// TypeAuthentication — no credential, or one that is not usable.
	TypeAuthentication Type = "authentication"
	// TypePermission — a valid credential without the capability.
	TypePermission Type = "permission"
	// TypeNotFound — no such resource for this company.
	TypeNotFound Type = "not_found"
	// TypeRateLimit — too many requests on this key.
	TypeRateLimit Type = "rate_limit"
	// TypeBudgetExhausted — T-03's refusal, typed so a programmatic caller
	// does not retry it the way it would retry a 500.
	TypeBudgetExhausted Type = "budget_exhausted"
	// TypeServer — our fault.
	TypeServer Type = "server"
)

// statusByType maps a class to its HTTP status. An unknown type is a 500:
// a response with no mapping is a bug on our side, and saying so is more
// honest than guessing 400.
var statusByType = map[Type]int{
	TypeInvalidRequest:  http.StatusBadRequest,
	TypeAuthentication:  http.StatusUnauthorized,
	TypePermission:      http.StatusForbidden,
	TypeNotFound:        http.StatusNotFound,
	TypeRateLimit:       http.StatusTooManyRequests,
	TypeBudgetExhausted: http.StatusPaymentRequired,
	TypeServer:          http.StatusInternalServerError,
}

// Status returns the HTTP status for a type.
func Status(t Type) int {
	if code, ok := statusByType[t]; ok {
		return code
	}
	return http.StatusInternalServerError
}

// Detail is the error object. Empty fields are omitted so a client reading
// `param` can trust that its presence means something.
type Detail struct {
	Type      Type   `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Param     string `json:"param,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// Body is the whole response. The nesting under "error" is what lets a client
// tell a failure from a success without consulting the status code twice.
type Body struct {
	Error Detail `json:"error"`
}

// requestIDKey is where the request-id middleware (T-A1) leaves its value.
// Reading it through a constant means this package does not have to change
// when that middleware lands.
const requestIDKey = "request_id"

// NewDetail builds the error object without writing it, for the one response
// that carries something alongside the error — the idempotency middleware's
// `409 request_in_flight`, which returns the ids the caller is already
// waiting on. Composing that from this type rather than from a hand-built
// map is what stops the envelope's field names from having a second
// definition that quietly drifts.
func NewDetail(c *gin.Context, t Type, code, message string) Detail {
	return Detail{
		Type:      t,
		Code:      code,
		Message:   message,
		RequestID: c.GetString(requestIDKey),
	}
}

// Abort writes the envelope and stops the handler chain. Middleware uses this;
// so should any handler that has nothing further to do.
func Abort(c *gin.Context, t Type, code, message string) {
	AbortParam(c, t, code, message, "")
}

// AbortParam is Abort with the offending field named. Use it whenever the
// caller can act on which input was wrong — an integrator reading
// `"param":"rows"` fixes their request without reading prose.
func AbortParam(c *gin.Context, t Type, code, message, param string) {
	AbortStatus(c, Status(t), t, code, message, param)
}

// AbortStatus writes the envelope with an explicit status code.
//
// It exists for the two responses whose status is not implied by their class.
// A `/v1` kill switch is a 503 and a body over the size cap is a 413, and
// neither has a `type` of its own: the vocabulary in the ticket is closed, and
// widening it so two operational responses can each have a private class
// would hand clients seven more branches to write. So the class stays honest
// (`server` for "we turned it off", `invalid_request` for "you sent too
// much") and only the status carries the extra precision.
func AbortStatus(c *gin.Context, status int, t Type, code, message, param string) {
	c.AbortWithStatusJSON(status, Body{Error: Detail{
		Type:      t,
		Code:      code,
		Message:   message,
		Param:     param,
		RequestID: c.GetString(requestIDKey),
	}})
}
