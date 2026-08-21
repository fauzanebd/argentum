package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// CompanyDataHandler is retention, erasure and export (T-H6).
//
// **Why these three sit on one handler.** Under UU PDP 27/2022 the tenant is
// the *pengendali data* and owes their own users an erasure they cannot
// perform without a route from us. The obligation is one thing; the three
// endpoints are the read, the exit and the delete of it, and separating them
// across handlers is how one of them ends up with a different access policy
// from the other two.
type CompanyDataHandler struct {
	svc *app.RetentionService
}

// NewCompanyDataHandler wires the handler.
func NewCompanyDataHandler(svc *app.RetentionService) *CompanyDataHandler {
	return &CompanyDataHandler{svc: svc}
}

// Register installs the routes. Caller wraps the group with Auth; the role
// policy in cmd/api/policy.go is what gates them, and every one of these is
// admin.
func (h *CompanyDataHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/company/data/export", h.export)
	rg.GET("/company/data/erasures", h.history)
	rg.DELETE("/company/data", h.erase)
}

// erase deletes every conversation the company has.
//
// **It requires the company's own id in the body**, which is the one piece of
// ceremony on this route and the reason it is not a mistake somebody can make
// with a stray `curl`. There is no undo, no soft-delete and no recycle bin:
// the rows are gone when this returns 200, which is what the tenant is asking
// for and what makes a misfire unrecoverable.
func (h *CompanyDataHandler) erase(c *gin.Context) {
	var req struct {
		// ConfirmCompanyID must equal the caller's own company. Not a boolean
		// "confirm": true — a flag can be set by a client that does not know
		// what it is confirming, and copying the id back is a thing only a
		// caller looking at the right tenant can do.
		ConfirmCompanyID string `json:"confirm_company_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	company := companyID(c)
	if req.ConfirmCompanyID != company {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "confirm_company_id must match the authenticated company; this operation cannot be undone",
		})
		return
	}

	rec, err := h.svc.EraseCompanyData(c.Request.Context(), company, userID(c))
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The record, not a 204. The completion record is the deliverable of this
	// endpoint as much as the delete is — a tenant discharging their own
	// erasure obligation needs something to file.
	c.JSON(http.StatusOK, rec)
}

// history serves the written record: every purge tick and every erasure.
func (h *CompanyDataHandler) history(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	records, err := h.svc.History(c.Request.Context(), companyID(c), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"erasures": records})
}

// export streams the company's transcripts as newline-delimited JSON.
//
// **NDJSON rather than one JSON array**, and it is not a style choice. The
// route exists so erasure is not the only exit, so it is called by the tenants
// with the most history; a single array cannot be written without either
// buffering the whole result or hand-rolling the brackets around a stream. One
// object per line is readable by `jq`, resumable by eye, and costs one row of
// memory at a time.
//
// A failure partway through cannot become a 500 — the status line is long
// gone. It ends the stream with an `{"export_error": …}` line instead, so a
// truncated file says why it is truncated rather than looking complete.
func (h *CompanyDataHandler) export(c *gin.Context) {
	company := companyID(c)
	filename := fmt.Sprintf("argentum-export-%s-%s.ndjson", company, time.Now().UTC().Format("20060102"))

	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Status(http.StatusOK)

	enc := json.NewEncoder(c.Writer)
	err := h.svc.ExportCompanyData(c.Request.Context(), company, func(m domain.ExportedMessage) error {
		return enc.Encode(m)
	})
	if err != nil {
		// Best-effort: if the client has already gone this write fails too, and
		// there is nothing further to do about it.
		_ = enc.Encode(map[string]string{"export_error": err.Error()})
	}
}
