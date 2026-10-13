package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// FreeCreditHandler is the operator's free-credit queue (RIZ-89): every new
// workspace waiting for its free credit, and approve or reject on each.
//
// **Dashboard-only**, for TopupsHandler's reason: `/api` with no `/v1` twin,
// so no openapi entry and no SDK change. The routes are member in apiPolicy
// and narrowed by OperatorOnly, installed on their group here, exactly as the
// top-up queue's are — the operator allowlist is not a role, and every
// sign-up is the admin of its own workspace, so admin here would let anyone
// approve their own free credit.
//
// Registered unconditionally, with a 503 when there is no service, so the role
// table's classification test sees every route on every deployment.
type FreeCreditHandler struct {
	svc      *app.FreeCreditService
	operator gin.HandlerFunc
}

// NewFreeCreditHandler wires it. operator is the OperatorOnly middleware; nil
// refuses every route, which is what an unconfigured deployment should do.
func NewFreeCreditHandler(svc *app.FreeCreditService, operator gin.HandlerFunc) *FreeCreditHandler {
	return &FreeCreditHandler{svc: svc, operator: operator}
}

// The sentences the operator reads. Spelled once, here, for topupMsg*'s
// reason.
const (
	freeCreditMsgAlreadyReviewed = "This request was already reviewed."
	freeCreditMsgNotFound        = "no such request"
	freeCreditMsgUnavailable     = "Free credit review is not available right now."
)

// Register installs the routes. Caller wraps rg with Auth and the role table.
func (h *FreeCreditHandler) Register(rg *gin.RouterGroup) {
	op := h.operator
	if op == nil {
		op = refuseEveryOperator
	}
	ops := rg.Group("/operator", op)
	ops.GET("/free-credit", h.list)
	ops.POST("/free-credit/:id/approve", h.approve)
	ops.POST("/free-credit/:id/reject", h.reject)
}

func refuseEveryOperator(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "operators only"})
}

func (h *FreeCreditHandler) list(c *gin.Context) {
	if h.svc == nil {
		freeCreditUnavailable(c)
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
		freeCreditFail(c, err)
		return
	}
	c.JSON(http.StatusOK, OperatorFreeCreditResponse{Requests: out})
}

func (h *FreeCreditHandler) approve(c *gin.Context) {
	if h.svc == nil {
		freeCreditUnavailable(c)
		return
	}
	res, err := h.svc.Approve(c.Request.Context(), c.Param("id"), userID(c))
	if err != nil {
		freeCreditFail(c, err)
		return
	}
	c.JSON(http.StatusOK, FreeCreditApproveResponse{
		Request:         res.Request,
		AddedMicroUSD:   res.AddedMicroUSD,
		BalanceMicroUSD: res.BalanceMicroUSD,
	})
}

// reject reads no body: a rejection carries no reason in v1.
func (h *FreeCreditHandler) reject(c *gin.Context) {
	if h.svc == nil {
		freeCreditUnavailable(c)
		return
	}
	g, err := h.svc.Reject(c.Request.Context(), c.Param("id"), userID(c))
	if err != nil {
		freeCreditFail(c, err)
		return
	}
	c.JSON(http.StatusOK, FreeCreditRejectResponse{Request: g})
}

func freeCreditUnavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": freeCreditMsgUnavailable})
}

func freeCreditFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": freeCreditMsgNotFound})
	case errors.Is(err, domain.ErrCreditGrantAlreadyReviewed):
		c.JSON(http.StatusConflict, gin.H{"error": freeCreditMsgAlreadyReviewed})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the free-credit request could not be updated"})
	}
}
