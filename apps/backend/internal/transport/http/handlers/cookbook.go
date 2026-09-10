package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// CookbookHandler exposes the per-tenant query cookbook (T-Q8).
//
// Four routes, and no CRUD: an example is *learned* from a turn that happened,
// never authored. A hand-written example would be a query nobody has run
// against data nobody has checked, presented to the agent with the same
// authority as one that demonstrably answered a real question.
//
// Two of the four write, and they are opposites — `harvest` learns, `sweep`
// forgets (T-Q15). Both are on a cron already; both are exposed here because
// an admin who has just renamed a table should not have to wait until 03:41 to
// find out what it cost them.
type CookbookHandler struct {
	svc *app.CookbookService
}

func NewCookbookHandler(svc *app.CookbookService) *CookbookHandler {
	return &CookbookHandler{svc: svc}
}

func (h *CookbookHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/cookbook", h.status)
	rg.POST("/cookbook/harvest", h.harvest)
	rg.POST("/cookbook/sweep", h.sweep)
	rg.DELETE("/cookbook", h.forget)
}

func (h *CookbookHandler) status(c *gin.Context) {
	n, err := h.svc.Count(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The archived count beside the live one, because "the cookbook shrank" is
	// a question somebody will ask and the answer is not visible from a single
	// number that went down.
	archived, err := h.svc.CountArchived(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"examples": n, "archived": archived})
}

// sweep retires what the cookbook should no longer teach, on demand (T-Q15).
//
// Synchronous like the harvest, and cheaper: no embedding calls at all. What it
// costs is one schema introspection per source, which is the same query
// `get_schema` makes on an ordinary turn and is usually already warm in that
// tool's cache.
func (h *CookbookHandler) sweep(c *gin.Context) {
	unusedAfter := app.SweepUnusedAfter
	if d := c.Query("unused_after_days"); d != "" {
		days, err := strconv.Atoi(d)
		if err != nil || days < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unused_after_days must be a non-negative integer"})
			return
		}
		unusedAfter = time.Duration(days) * 24 * time.Hour
	}
	res, err := h.svc.Sweep(c.Request.Context(), companyID(c), unusedAfter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// harvest runs the mining pass on demand.
//
// Synchronous, unlike most work that costs LLM calls in this codebase. The
// harvest is bounded by `limit` and does one embedding call per NEW example —
// on the second run that is usually zero, because ExistingOrigins stops it
// re-learning — so the expensive case is the first run for a busy tenant, and
// an admin who has just pressed the button is the right person to wait for it.
func (h *CookbookHandler) harvest(c *gin.Context) {
	var since time.Time
	if s := c.Query("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "since must be RFC3339"})
			return
		}
		since = t
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))

	res, err := h.svc.Harvest(c.Request.Context(), companyID(c), since, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// forget empties the cookbook. The escape hatch for a warehouse that changed
// underneath it, where every example is now wrong and re-harvesting from the
// same history would only relearn them.
func (h *CookbookHandler) forget(c *gin.Context) {
	n, err := h.svc.Forget(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}
