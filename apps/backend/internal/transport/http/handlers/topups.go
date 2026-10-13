package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// TopupsHandler is the manual credit top-up (RIZ-83): a workspace admin's
// request, proof and history, and the operator's review queue.
//
// **Dashboard-only.** These are `/api` routes with no `/v1` twin, so there is
// no openapi entry and no SDK change: an integration has no business paying
// for a workspace's credit, and a person has to transfer the money anyway.
//
// Registered unconditionally, with a 503 when there is no service, for the
// knowledge-documents handler's reason: a missing route reads as a wrong path,
// a 503 says why. The tenant routes are admin in apiPolicy; the operator
// routes are member there and narrowed by OperatorOnly, which is installed on
// their group here.
type TopupsHandler struct {
	svc      *app.TopupService
	operator gin.HandlerFunc
}

// NewTopupsHandler wires it. operator is the OperatorOnly middleware; nil
// refuses every operator route, which is what an unconfigured deployment
// should do.
func NewTopupsHandler(svc *app.TopupService, operator gin.HandlerFunc) *TopupsHandler {
	return &TopupsHandler{svc: svc, operator: operator}
}

// The sentences a customer and an operator read. Spelled once, here, so the
// copy the owner approved is in one place a reviewer can diff.
const (
	topupMsgUnavailable      = "Top-up is not available right now. Please try again later."
	topupMsgInProgress       = "You already have a top-up in progress."
	topupMsgClosed           = "This top-up is closed. Start a new one."
	topupMsgNotCancellable   = "Only a top-up waiting for payment can be cancelled."
	topupMsgTooLarge         = "That file is larger than 5 MB. Upload a smaller screenshot."
	topupMsgWrongType        = "Upload a PNG, JPG, WebP or PDF file."
	topupMsgUnknownPayee     = "Choose the account you paid to."
	topupMsgProofUnavailable = "Proof upload is not available right now. Try again later."
	topupMsgAlreadyReviewed  = "This top-up was already reviewed."
	topupMsgReasonRequired   = "Write a reason the customer will see."
	topupMsgReasonTooLong    = "Keep the reason to 300 characters or fewer."
	topupMsgNotFound         = "no such top-up"
)

// topupFormSlack is what a multipart envelope adds around a 5 MB file: the
// boundaries, the part headers and the payee field. The service measures the
// file itself; this only has to stop a body that cannot be a 5 MB proof from
// being read at all.
const topupFormSlack = 64 << 10

// Register installs the routes. Caller wraps rg with Auth and the role table.
func (h *TopupsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/topups/options", h.options)
	rg.GET("/topups", h.list)
	rg.POST("/topups", h.create)
	rg.POST("/topups/:id/proof", h.uploadProof)
	rg.POST("/topups/:id/cancel", h.cancel)

	op := h.operator
	if op == nil {
		op = func(c *gin.Context) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "operators only"})
		}
	}
	ops := rg.Group("/operator", op)
	ops.GET("/topups", h.operatorList)
	ops.GET("/topups/:id/proof", h.operatorProof)
	ops.POST("/topups/:id/approve", h.approve)
	ops.POST("/topups/:id/reject", h.reject)
}

func (h *TopupsHandler) options(c *gin.Context) {
	if h.svc == nil {
		// The page reads enabled:false as "not available", which is exactly
		// what a deployment without the service is.
		c.JSON(http.StatusOK, TopupOptionsResponse{PresetsIDR: []int64{}, Payees: []domain.TopupPayee{}})
		return
	}
	o := h.svc.Options(c.Request.Context())
	c.JSON(http.StatusOK, TopupOptionsResponse{
		Enabled:            o.Enabled,
		MinIDR:             o.MinIDR,
		MaxIDR:             o.MaxIDR,
		StepIDR:            o.StepIDR,
		PresetsIDR:         o.PresetsIDR,
		PreviewIDRPerUSDE4: o.PreviewIDRPerUSDE4,
		Payees:             o.Payees,
		ExpiresAfterHours:  o.ExpiresAfterHours,
	})
}

func (h *TopupsHandler) list(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	out, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		topupFail(c, err)
		return
	}
	c.JSON(http.StatusOK, TopupsResponse{Topups: out})
}

// create reads amount_idr and nothing else. The body is decoded as raw fields
// so a string "150000" can be told from the number 150000 — the first is
// refused (AC22) — and so a credit, a rate or a code someone added to the body
// is never even parsed.
func (h *TopupsHandler) create(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		body = nil
	}
	amount, err := h.svc.ParseTopupAmount(body["amount_idr"])
	if err != nil {
		topupFail(c, err)
		return
	}
	t, err := h.svc.Create(c.Request.Context(), companyID(c), userID(c), amount)
	if err != nil {
		topupFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (h *TopupsHandler) uploadProof(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	// Installed before the multipart reader touches the body, for the
	// knowledge upload's reason: an oversized proof is refused while it is
	// still bytes on a socket.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, app.TopupProofMaxBytes+topupFormSlack)
	file, err := c.FormFile("file")
	if err != nil {
		if isBodyTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": topupMsgTooLarge})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": topupMsgWrongType})
		return
	}
	if file.Size > app.TopupProofMaxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": topupMsgTooLarge})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": topupMsgWrongType})
		return
	}
	defer func() { _ = f.Close() }()

	t, err := h.svc.UploadProof(c.Request.Context(), app.ProofInput{
		CompanyID: companyID(c),
		UserID:    userID(c),
		TopupID:   c.Param("id"),
		PayeeID:   c.PostForm("payee_id"),
		Body:      f,
	})
	if err != nil {
		topupFail(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *TopupsHandler) cancel(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	t, err := h.svc.Cancel(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		topupFail(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *TopupsHandler) operatorList(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	var reviewed bool
	switch c.DefaultQuery("view", "review") {
	case "review":
	case "reviewed":
		reviewed = true
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "view must be review or reviewed"})
		return
	}
	out, err := h.svc.OperatorList(c.Request.Context(), reviewed)
	if err != nil {
		topupFail(c, err)
		return
	}
	c.JSON(http.StatusOK, OperatorTopupsResponse{Topups: out})
}

// operatorProof answers a presigned link and is never cached: a link that
// outlived its five minutes in somebody's browser cache would be a dead link
// served as a live one.
func (h *TopupsHandler) operatorProof(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	link, err := h.svc.ProofLink(c.Request.Context(), c.Param("id"))
	if err != nil {
		topupFail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, TopupProofLinkResponse{URL: link.URL, ContentType: link.ContentType, ExpiresIn: link.ExpiresIn})
}

func (h *TopupsHandler) approve(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	t, balance, err := h.svc.Approve(c.Request.Context(), c.Param("id"), userID(c))
	if err != nil {
		topupFail(c, err)
		return
	}
	c.JSON(http.StatusOK, TopupApproveResponse{Topup: t, BalanceMicroUSD: balance})
}

func (h *TopupsHandler) reject(c *gin.Context) {
	if h.svc == nil {
		topupUnavailable(c)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	t, err := h.svc.Reject(c.Request.Context(), c.Param("id"), userID(c), body.Reason)
	if err != nil {
		topupFail(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

func topupUnavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": topupMsgUnavailable})
}

// topupFail maps the service's errors onto the approved copy. Another
// company's id is a 404 for knowledgeDocumentFail's reason.
func topupFail(c *gin.Context, err error) {
	var amountErr *app.TopupAmountError
	switch {
	case errors.As(err, &amountErr):
		c.JSON(http.StatusBadRequest, gin.H{"error": amountErr.Message})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": topupMsgNotFound})
	case errors.Is(err, domain.ErrTopupInProgress):
		c.JSON(http.StatusConflict, gin.H{"error": topupMsgInProgress})
	case errors.Is(err, domain.ErrTopupClosed):
		c.JSON(http.StatusConflict, gin.H{"error": topupMsgClosed})
	case errors.Is(err, domain.ErrTopupNotCancellable):
		c.JSON(http.StatusConflict, gin.H{"error": topupMsgNotCancellable})
	case errors.Is(err, domain.ErrTopupAlreadyReviewed):
		c.JSON(http.StatusConflict, gin.H{"error": topupMsgAlreadyReviewed})
	case errors.Is(err, app.ErrTopupUnavailable):
		topupUnavailable(c)
	case errors.Is(err, app.ErrTopupProofUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": topupMsgProofUnavailable})
	case errors.Is(err, app.ErrTopupProofTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": topupMsgTooLarge})
	case errors.Is(err, app.ErrTopupProofType):
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": topupMsgWrongType})
	case errors.Is(err, app.ErrTopupUnknownPayee):
		c.JSON(http.StatusBadRequest, gin.H{"error": topupMsgUnknownPayee})
	case errors.Is(err, app.ErrTopupReasonRequired):
		c.JSON(http.StatusBadRequest, gin.H{"error": topupMsgReasonRequired})
	case errors.Is(err, app.ErrTopupReasonTooLong):
		c.JSON(http.StatusBadRequest, gin.H{"error": topupMsgReasonTooLong})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the top-up could not be completed"})
	}
}
