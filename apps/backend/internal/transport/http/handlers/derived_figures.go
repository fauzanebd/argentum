package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// DerivedFiguresHandler serves T-W3's measurement: how often an answer stated a
// figure no tool returned, and how often `compute` was called and was not
// enough.
//
// Admin, beside T-F4's panel and on its line: it is the workspace's aggregate
// quality data. It quotes nobody's question, which is the half of T-F4's
// argument that does not carry over — and not a reason to open it to members,
// who have nothing on it to act on.
type DerivedFiguresHandler struct {
	svc *app.DerivedFiguresService
}

func NewDerivedFiguresHandler(svc *app.DerivedFiguresService) *DerivedFiguresHandler {
	return &DerivedFiguresHandler{svc: svc}
}

func (h *DerivedFiguresHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/quality/derived-figures", h.figures)
}

func (h *DerivedFiguresHandler) figures(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "derived-figure measurement is not configured on this deployment"})
		return
	}
	days, _ := strconv.Atoi(c.Query("days"))
	rep, err := h.svc.ForCompany(c.Request.Context(), companyID(c), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	f := rep.Figures
	c.JSON(http.StatusOK, DerivedFiguresResponse{
		WindowDays:         rep.Days,
		From:               f.From,
		Answered:           f.Answered,
		Checked:            f.Checked,
		Unchecked:          f.Unchecked(),
		Composed:           f.Composed,
		Computed:           f.Computed,
		Residue:            f.Residue,
		CrossSource:        f.CrossSource,
		UnaccountedPercent: f.UnaccountedPercent(),
		ResiduePercent:     f.ResiduePercent(),
	})
}
