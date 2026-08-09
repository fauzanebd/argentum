package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// ShareHandler serves a shared report to somebody with no account.
//
// **It is not under `/api` and not under `/v1`, and that is the design rather
// than a routing preference.** Both of those groups mean "authenticated" —
// `/api` by a session, `/v1` by a key — and every middleware on them, every
// policy table and every test that walks them assumes a tenant. A keyless
// route inside either would be an exemption in somebody else's chain, which is
// the shape a mistake hides in. `/share` is its own group with its own
// middleware: a rate limit, `noindex`, and nothing else.
//
// The token in the path is the entire credential. That is what a bearer link
// is, and the properties that make it safe are elsewhere: 256 bits of entropy,
// a hash in the database, a default expiry, an explicit revoke, and a counted,
// audited view.
type ShareHandler struct{ svc *app.ReportShareService }

func NewShareHandler(svc *app.ReportShareService) *ShareHandler {
	return &ShareHandler{svc: svc}
}

// Register installs the two routes a player page needs: the metadata and plan
// it draws from, and nothing else.
//
// There is no HTML here. The page itself is the dashboard's SPA route — one
// build, one design system, one place the player component lives — and this
// serves it the data. An HTML template in Go would be a second frontend, drawn
// with a second set of tokens, drifting from the first.
func (h *ShareHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/:token", h.get)
}

// shareViewResponse is what the player fetches. It carries the plan itself
// rather than a URL to one: a presigned URL for the plan would outlive the
// revocation, be cacheable by anything in between, and be shareable onward
// without ever passing this handler again.
type shareViewResponse struct {
	Title    string `json:"title"`
	Filename string `json:"filename"`
	Format   string `json:"format"`
	// Plan is the videoplan document, verbatim. `json.RawMessage` so it is not
	// unmarshalled and re-marshalled on the way through — the renderer and the
	// player must be looking at the same bytes, and a round trip through Go
	// structs is a place for them to stop being.
	Plan any `json:"plan"`
	// DownloadURL is present only when the shared document is a video. A
	// visitor who can watch it can keep it; a PDF is not offered, because the
	// share is a player and a file handed out from a revocable page is a file
	// that outlives the revocation.
	DownloadURL string `json:"download_url,omitempty"`
	ExpiresAt   string `json:"expires_at"`
}

func (h *ShareHandler) get(c *gin.Context) {
	// Both headers go out before anything can return, including the refusals.
	// A cacheable 404 keeps being served after a share is fixed, and an
	// indexable one publishes the shape of the URL space — so the ordering
	// here is the property, not the presence.
	//
	// `private, no-store` is what makes revocation mean something: a CDN or a
	// corporate proxy holding a copy would serve a link that has been taken
	// back, from a machine we cannot reach. `noindex` is because a link
	// somebody emailed is a link a crawler eventually follows.
	c.Header("Cache-Control", "private, no-store, max-age=0")
	c.Header("X-Robots-Tag", "noindex, nofollow, noarchive")

	if h.svc == nil {
		// No object storage means no plan was ever written, so there is
		// nothing any token could open. A 404 rather than a 503: the visitor
		// is a stranger holding a link, and our deployment's configuration is
		// not something to tell them about.
		c.JSON(http.StatusNotFound, gin.H{"error": "This link is not available."})
		return
	}

	res, err := h.svc.Resolve(c.Request.Context(), c.Param("token"), c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		// One answer for unknown, expired, revoked and deleted. A visitor
		// cannot tell them apart, which is what stops the route being an
		// oracle for somebody trying tokens; our own logs and the share list
		// in the dashboard say which it was.
		if errors.Is(err, app.ErrShareGone) {
			c.JSON(http.StatusNotFound, gin.H{"error": "This link is not available."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "This link could not be opened."})
		return
	}

	c.JSON(http.StatusOK, shareViewResponse{
		Title:       res.Document.Filename,
		Filename:    res.Document.Filename,
		Format:      string(res.Document.Format),
		Plan:        rawJSON(res.Plan),
		DownloadURL: res.DownloadURL,
		ExpiresAt:   res.Share.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// rawJSON hands the stored plan through untouched.
type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}
