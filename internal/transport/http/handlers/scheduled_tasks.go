package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ScheduledTasksHandler exposes /scheduled-tasks endpoints. Auth + tenant
// isolation are handled by the middleware/Auth chain that wraps the
// router group; the handler reads company_id from the gin context.
type ScheduledTasksHandler struct {
	svc *app.ScheduledTaskService
}

func NewScheduledTasksHandler(svc *app.ScheduledTaskService) *ScheduledTasksHandler {
	return &ScheduledTasksHandler{svc: svc}
}

func (h *ScheduledTasksHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/scheduled-tasks", h.list)
	rg.POST("/scheduled-tasks", h.create)
	rg.GET("/scheduled-tasks/:id", h.get)
	rg.PATCH("/scheduled-tasks/:id", h.update)
	rg.DELETE("/scheduled-tasks/:id", h.delete)
	rg.GET("/scheduled-tasks/:id/runs", h.listRuns)
	rg.GET("/scheduled-tasks/:id/runs/:runID", h.getRun)
}

func (h *ScheduledTasksHandler) list(c *gin.Context) {
	tasks, err := h.svc.ListByCompany(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

type createScheduledReq struct {
	Name           string `json:"name" binding:"required"`
	Prompt         string `json:"prompt" binding:"required"`
	CronExpression string `json:"cron_expression" binding:"required"`
	Timezone       string `json:"timezone"`
}

func (h *ScheduledTasksHandler) create(c *gin.Context) {
	var req createScheduledReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	uid, _ := c.Get("user_id")
	userID, _ := uid.(string)
	task, err := h.svc.Create(c.Request.Context(), app.CreateInput{
		CompanyID:      companyID(c),
		UserID:         userID,
		Name:           req.Name,
		Prompt:         req.Prompt,
		CronExpression: req.CronExpression,
		Timezone:       req.Timezone,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, task)
}

func (h *ScheduledTasksHandler) get(c *gin.Context) {
	task, err := h.svc.Get(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		writeScheduledErr(c, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

type updateScheduledReq struct {
	Name           *string `json:"name"`
	Prompt         *string `json:"prompt"`
	CronExpression *string `json:"cron_expression"`
	Timezone       *string `json:"timezone"`
	Enabled        *bool   `json:"enabled"`
}

func (h *ScheduledTasksHandler) update(c *gin.Context) {
	var req updateScheduledReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	task, err := h.svc.Update(c.Request.Context(), companyID(c), c.Param("id"), app.UpdateInput{
		Name:           req.Name,
		Prompt:         req.Prompt,
		CronExpression: req.CronExpression,
		Timezone:       req.Timezone,
		Enabled:        req.Enabled,
	})
	if err != nil {
		writeScheduledErr(c, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *ScheduledTasksHandler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		writeScheduledErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *ScheduledTasksHandler) listRuns(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	runs, err := h.svc.ListRuns(c.Request.Context(), companyID(c), c.Param("id"), limit, offset)
	if err != nil {
		writeScheduledErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

func (h *ScheduledTasksHandler) getRun(c *gin.Context) {
	run, err := h.svc.GetRun(c.Request.Context(), companyID(c), c.Param("id"), c.Param("runID"))
	if err != nil {
		writeScheduledErr(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

func writeScheduledErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}
