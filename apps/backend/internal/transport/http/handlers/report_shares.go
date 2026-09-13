package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ReportShareHandler is the dashboard's half of T-V4: minting a link for a
// document, listing the live ones, and taking one back.
//
// The half that serves the link is `ShareHandler`, and it is a different type
// on a different route group for a reason that is not organisational — see
// that file.
type ReportShareHandler struct {
	svc *app.ReportShareService
	// docs and conversations hide the links of a document the caller may not
	// see (T-Z11). Either nil leaves every route as it was.
	docs          domain.DocumentRepository
	conversations ConversationReader
}

func NewReportShareHandler(svc *app.ReportShareService) *ReportShareHandler {
	return &ReportShareHandler{svc: svc}
}

// WithConversationAccess applies DocumentsHandler's rule to a document's links
// (T-Z11). The document is read here, not in the service, because the service
// answers a company's question and this is a person's.
func (h *ReportShareHandler) WithConversationAccess(docs domain.DocumentRepository, r ConversationReader) *ReportShareHandler {
	h.docs = docs
	h.conversations = r
	return h
}

// Register installs the routes on the authenticated `/api` group. All three
// are admin in `cmd/api/policy.go`: a share is a credential that reaches a
// tenant's figures from outside every session, which is the same thing an API
// key is and is gated the same way.
// The routes are registered whether or not sharing is available on this
// deployment. A deployment with no object storage stored no plans and can
// share nothing, and the honest answer to that is a 503 saying so — an absent
// route answers 404, which reads as "you have the URL wrong". It is also what
// keeps `apiPolicy` diffable against the router: a route the table lists and
// the router registers only sometimes is a table nothing can check.
func (h *ReportShareHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/documents/:id/shares", h.available, h.visible, h.list)
	rg.POST("/documents/:id/shares", h.available, h.visible, h.create)
	// Revoking asks nothing about the document (T-Z11): it can only close a
	// door, and an admin the document is hidden from must still be able to take
	// back a link to it — resourceExempt's reason for the dashboard revoke.
	rg.DELETE("/documents/:id/shares/:shareID", h.available, h.revoke)
}

func (h *ReportShareHandler) available(c *gin.Context) {
	if h.svc == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": "Sharing a report as a player needs object storage, which is not configured on this deployment.",
		})
	}
}

// visible hides the links of a document the caller may not see (T-Z11), and
// answers as the route answers a document with none: listing is an empty list —
// what an id nobody shared gets — and minting is create's own not-found. A link
// is a door out of every session, so a person the product will not show the
// document to is not somebody who may open one.
//
// A document this lookup cannot find passes through untouched, so the route
// answers a missing one exactly as it did before this ticket.
func (h *ReportShareHandler) visible(c *gin.Context) {
	if h.conversations == nil || h.docs == nil {
		return
	}
	ctx := c.Request.Context()
	doc, err := h.docs.GetForCompany(ctx, companyID(c), c.Param("id"))
	if errors.Is(err, domain.ErrNotFound) {
		return
	}
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to that document; try again"})
		return
	}
	if doc.ThreadID == "" {
		return
	}
	ok, err := h.conversations.MayRead(ctx, companyID(c), userID(c), doc.ThreadID)
	switch {
	case err != nil:
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to that document; try again"})
	case ok:
	case c.Request.Method == http.MethodGet:
		c.AbortWithStatusJSON(http.StatusOK, gin.H{"shares": []shareResponse{}})
	default:
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "document not found"})
	}
}

type createShareReq struct {
	// ExpiresInDays is optional. Zero means the 30-day default; over the
	// 90-day ceiling is a 400 rather than a silent clamp.
	ExpiresInDays int `json:"expires_in_days"`
}

// shareResponse is a share as the dashboard sees it. The token is **not** on
// it: it appears in exactly one response body in a link's lifetime, on create,
// and `CreatedShare` is the only shape that carries it.
type shareResponse struct {
	ID           string     `json:"id"`
	DocumentID   string     `json:"document_id"`
	CreatedBy    string     `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	ViewCount    int        `json:"view_count"`
	LastViewedAt *time.Time `json:"last_viewed_at,omitempty"`
	// Live is computed rather than stored so the dashboard does not have to
	// re-derive "revoked or expired" and get it subtly different.
	Live bool `json:"live"`
	// Paused is a live link that will not open while an agent in its document's
	// conversation is restricted (T-Z13). It opens again when that agent does, so
	// it is not "revoked", and a list calling it live would be a list an admin
	// believes when a visitor is being told the link is gone.
	Paused bool `json:"paused,omitempty"`
}

func toShareResponse(s *domain.ReportShare) shareResponse {
	return shareResponse{
		ID: s.ID, DocumentID: s.DocumentID, CreatedBy: s.CreatedBy,
		CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, RevokedAt: s.RevokedAt,
		ViewCount: s.ViewCount, LastViewedAt: s.LastViewedAt,
		Live: s.Live(time.Now()),
	}
}

func (h *ReportShareHandler) list(c *gin.Context) {
	shares, err := h.svc.List(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	paused, err := h.svc.Paused(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to that document; try again"})
		return
	}
	out := make([]shareResponse, 0, len(shares))
	for _, s := range shares {
		r := toShareResponse(s)
		r.Paused = r.Live && paused
		out = append(out, r)
	}
	c.JSON(http.StatusOK, gin.H{"shares": out})
}

func (h *ReportShareHandler) create(c *gin.Context) {
	var req createShareReq
	// An absent body is a valid request for a default-length share, so a bind
	// failure on an empty body is not an error.
	_ = c.ShouldBindJSON(&req)

	created, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c), c.Param("id"),
		time.Duration(req.ExpiresInDays)*24*time.Hour)
	if err != nil {
		switch {
		// 409 with the reason, T-Z5's answer for a restricted dashboard: the
		// document is there and the caller may see it, and a link is the one thing
		// it cannot have.
		case errors.Is(err, app.ErrDocumentRestricted):
			c.JSON(http.StatusConflict, gin.H{"error": app.ErrDocumentRestricted.Error()})
		case errors.Is(err, app.ErrShareCheckFailed):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": app.ErrShareCheckFailed.Error()})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		case errors.Is(err, domain.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	// 201 with the token, exactly once. Nothing reads it back afterwards, and
	// the list route above cannot: the row holds a hash.
	c.JSON(http.StatusCreated, gin.H{
		"share": toShareResponse(created.Share),
		"token": created.Token,
	})
}

func (h *ReportShareHandler) revoke(c *gin.Context) {
	err := h.svc.Revoke(c.Request.Context(), companyID(c), c.Param("shareID"))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "share not found"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		c.Status(http.StatusNoContent)
	}
}
