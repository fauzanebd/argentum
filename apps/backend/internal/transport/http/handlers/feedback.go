package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// FeedbackHandler exposes the "was that answer any good?" routes (T-Q2).
type FeedbackHandler struct {
	svc *app.FeedbackService
}

func NewFeedbackHandler(svc *app.FeedbackService) *FeedbackHandler {
	return &FeedbackHandler{svc: svc}
}

// Register installs the routes. Caller wraps with Auth middleware.
//
// The rating route is not restricted by role, deliberately. Whoever read the
// answer is the person who knows whether it was right, and gating the button
// behind admin would collect verdicts from the people least likely to be
// reading the day's chat. The reads beside it are a different matter and stay
// where the rest of the tenant's aggregate data is.
func (h *FeedbackHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/messages/:id/feedback", h.rate)
	rg.GET("/messages/:id/feedback", h.forMessage)
	rg.GET("/feedback", h.recent)
	rg.GET("/feedback/summary", h.summary)
}

// rateReq is the body of POST /messages/:id/feedback.
type rateReq struct {
	// Rating is +1 or -1. Required, and there is no default: a request that
	// forgot the field means a client bug, and defaulting it either way would
	// write a verdict nobody gave.
	Rating int16 `json:"rating"`
	// Reason is free text, optional, capped by the service. It is the most
	// useful column in the table and the one a UI is most tempted to omit.
	Reason string `json:"reason,omitempty"`
}

func (h *FeedbackHandler) rate(c *gin.Context) {
	var req rateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.svc.Rate(c.Request.Context(), app.RateInput{
		CompanyID: companyID(c),
		MessageID: c.Param("id"),
		Rating:    domain.FeedbackRating(req.Rating),
		Reason:    req.Reason,
		// A dashboard session is a person we authenticated, which is exactly
		// what ActorKindUser means in the audit log's vocabulary.
		ActorKind: domain.ActorKindUser,
		ActorRef:  userID(c),
	})
	if err != nil {
		feedbackFail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FeedbackHandler) forMessage(c *gin.Context) {
	out, err := h.svc.ForMessage(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		feedbackFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"feedback": out})
}

func (h *FeedbackHandler) recent(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	// Default to the negatives. The positives are the majority and say nothing
	// actionable; somebody opening this list is looking for what went wrong.
	onlyNegative := c.DefaultQuery("only_negative", "true") != "false"

	out, err := h.svc.Recent(c.Request.Context(), companyID(c), onlyNegative, limit, offset)
	if err != nil {
		feedbackFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"feedback": out, "only_negative": onlyNegative})
}

func (h *FeedbackHandler) summary(c *gin.Context) {
	var from, to time.Time
	if s := c.Query("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "from must be RFC3339"})
			return
		}
		from = t
	}
	if s := c.Query("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "to must be RFC3339"})
			return
		}
		to = t
	}
	out, err := h.svc.Summary(c.Request.Context(), companyID(c), from, to)
	if err != nil {
		feedbackFail(c, err)
		return
	}
	// down_rate is computed rather than stored so the dashboard and any future
	// consumer cannot disagree about the denominator — it is over *rated*
	// answers, not over turns. See domain.FeedbackSummary.
	c.JSON(http.StatusOK, gin.H{
		"rated":     out.Rated,
		"up":        out.Up,
		"down":      out.Down,
		"down_rate": out.DownRate(),
	})
}

// feedbackFail maps the service's errors onto status codes. A message that
// belongs to another tenant is a 404 for the reason chatFail gives: a 403
// would confirm the row is real to a caller holding a bare uuid.
func feedbackFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such message"})
	case errors.Is(err, app.ErrNotAssistantMessage),
		errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
