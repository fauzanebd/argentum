package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/openapi"
)

// V1OpenAPIHandler serves the published contract at `GET /v1/openapi.json`
// (T-A4).
//
// **Public and keyless**, which is the whole point: an integrator reads the
// spec before they have a credential to call it with, and a contract you have
// to authenticate to read is a contract you evaluate by asking us for a key
// first.
//
// It is still behind the `/v1` kill switch. A spec is a promise about a
// surface, and generating a client from one while every call it describes
// answers 503 produces a client nobody can use — the honest answer there is
// the 503 the rest of the API is already giving.
type V1OpenAPIHandler struct {
	body []byte
	err  error
}

// NewV1OpenAPIHandler converts the embedded YAML to JSON once, at construction.
//
// Not per request: it is the same bytes every time, and doing it in the
// handler would put a YAML parse and a JSON marshal on a route whose job is to
// be trivially cheap. A conversion failure is kept rather than fatal — the
// binary should still boot and serve the API it has, and this one route
// reports what went wrong.
func NewV1OpenAPIHandler() *V1OpenAPIHandler {
	body, err := openapi.JSON()
	if err != nil {
		logrus.WithError(err).Error("openapi spec could not be converted to JSON; /v1/openapi.json will answer 500")
	}
	return &V1OpenAPIHandler{body: body, err: err}
}

// Register installs the route.
//
// Call it on a `/v1` group that carries the request-id middleware and the kill
// switch but **not** APIKeyAuth — see the router, where that group is built
// separately for this one route.
func (h *V1OpenAPIHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/openapi.json", h.spec)
}

func (h *V1OpenAPIHandler) spec(c *gin.Context) {
	if h.err != nil || len(h.body) == 0 {
		apierr.Abort(c, apierr.TypeServer, "spec_unavailable",
			"The API specification could not be read on this deployment.")
		return
	}
	// The one place under `/v1` that sends a CORS header, and it is safe for
	// the reason the rest of the surface is not: there is no credential on this
	// route, so a page that fetches it gains nothing it could not have fetched
	// from a terminal. It is here because the readers are browser tools — a
	// documentation viewer, a schema explorer, an editor's "import from URL".
	c.Header("Access-Control-Allow-Origin", "*")
	// Five minutes. Long enough that a docs page rendering the spec on every
	// view is not a request per view, short enough that a deploy's changed
	// contract is visible while someone is still debugging against it.
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "application/json; charset=utf-8", h.body)
}
