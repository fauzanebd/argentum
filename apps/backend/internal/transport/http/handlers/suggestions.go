package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// SuggestionsHandler exposes "somebody pressed one of the chips" (T-U13).
type SuggestionsHandler struct {
	svc *app.SuggestionService
}

func NewSuggestionsHandler(svc *app.SuggestionService) *SuggestionsHandler {
	return &SuggestionsHandler{svc: svc}
}

// Register installs the routes. Caller wraps with Auth middleware.
//
// The pick route is not restricted by role, for the same reason the rating
// route beside it is not: whoever read the answer is the person doing the
// clicking, and a member is the majority of them. The summary is aggregate
// tenant data and sits where the rest of that lives.
func (h *SuggestionsHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/messages/:id/suggestion-picked", h.pick)
	rg.GET("/suggestions/summary", h.summary)
}

// pickReq is the body of POST /messages/:id/suggestion-picked.
//
// One field. The label and the recommended flag are read off the stored message
// rather than accepted from the browser — see SuggestionService.Pick for why.
type pickReq struct {
	Index int `json:"index"`
}

func (h *SuggestionsHandler) pick(c *gin.Context) {
	var req pickReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.svc.Pick(c.Request.Context(), app.PickInput{
		CompanyID: companyID(c),
		MessageID: c.Param("id"),
		Index:     req.Index,
	})
	if err != nil {
		suggestionFail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *SuggestionsHandler) summary(c *gin.Context) {
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
		suggestionFail(c, err)
		return
	}
	// pick_rate is computed rather than stored so no two consumers can disagree
	// about the denominator — it is over answers that OFFERED a suggestion, not
	// over turns. See domain.SuggestionPickSummary.
	c.JSON(http.StatusOK, gin.H{
		"offered":           out.Offered,
		"picked":            out.Picked,
		"picks":             out.Picks,
		"recommended_picks": out.RecommendedPicks,
		"pick_rate":         out.PickRate(),
	})
}

// suggestionFail maps the service's errors onto status codes. A message that
// belongs to another tenant is a 404 for the reason feedbackFail gives: a 403
// would confirm the row is real to a caller holding a bare uuid.
func suggestionFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such message"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
