package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// CompanyHandler exposes connection + phone management endpoints.
type CompanyHandler struct{ svc *app.CompanyService }

func NewCompanyHandler(svc *app.CompanyService) *CompanyHandler {
	return &CompanyHandler{svc: svc}
}

// Register installs the routes. Caller is expected to wrap the group with the
// Auth middleware before calling.
func (h *CompanyHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/connections", h.listConnections)
	rg.POST("/connections", h.addConnection)
	rg.PATCH("/connections/:id", h.updateConnectionMeta)
	rg.PUT("/connections/:id/dsn", h.updateConnectionDSN)
	rg.POST("/connections/:id/default", h.setDefault)
	rg.POST("/connections/:id/regenerate-description", h.regenerateDescription)
	rg.DELETE("/connections/:id", h.deleteConnection)
	rg.POST("/connections/test", h.testConnection)

	rg.GET("/phones", h.listPhones)
	rg.POST("/phones", h.addPhone)
	rg.DELETE("/phones/:phone", h.deletePhone)

	rg.GET("/settings", h.getSettings)
	rg.PUT("/settings", h.updateSettings)
}

func companyID(c *gin.Context) string {
	v, _ := c.Get("company_id")
	s, _ := v.(string)
	return s
}

// buildDSN constructs a driver-specific DSN from discrete fields.
// If raw is non-empty it is returned as-is (advanced mode).
func buildDSN(dbType, raw, host, port, user, pass, dbname string) (string, error) {
	if raw != "" {
		return raw, nil
	}
	if host == "" || port == "" || dbname == "" {
		return "", fmt.Errorf("host, port and database name are required")
	}
	switch dbType {
	case "postgres":
		u := url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(user, pass),
			Host:   host + ":" + port,
			Path:   dbname,
		}
		q := u.Query()
		q.Set("sslmode", "require")
		u.RawQuery = q.Encode()
		return u.String(), nil
	case "mysql":
		cfg := mysql.Config{
			User:                 user,
			Passwd:               pass,
			Net:                  "tcp",
			Addr:                 host + ":" + port,
			DBName:               dbname,
			ParseTime:            true,
			Loc:                  time.UTC,
			AllowNativePasswords: true,
		}
		return cfg.FormatDSN(), nil
	case "sqlserver":
		u := url.URL{
			Scheme: "sqlserver",
			User:   url.UserPassword(user, pass),
			Host:   host + ":" + port,
		}
		q := u.Query()
		q.Set("database", dbname)
		q.Set("encrypt", "true")
		q.Set("TrustServerCertificate", "false")
		u.RawQuery = q.Encode()
		return u.String(), nil
	default:
		return "", fmt.Errorf("unsupported db_type %q", dbType)
	}
}

func (h *CompanyHandler) listConnections(c *gin.Context) {
	out, err := h.svc.ListConnections(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"connections": out})
}

type addConnReq struct {
	DBType      string `json:"db_type" binding:"required"`
	Label       string `json:"label"`
	Description string `json:"description"`
	DSN         string `json:"dsn"`
	Host        string `json:"host"`
	Port        string `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DBName      string `json:"dbname"`
	IsDefault   bool   `json:"is_default"`
}

func (h *CompanyHandler) addConnection(c *gin.Context) {
	var req addConnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dsn, err := buildDSN(req.DBType, req.DSN, req.Host, req.Port, req.Username, req.Password, req.DBName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	conn, err := h.svc.AddConnection(c.Request.Context(), companyID(c), req.DBType, req.Label, req.Description, dsn, req.IsDefault)
	if err != nil {
		if errors.Is(err, domain.ErrUnsupportedDB) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	conn.DSNEncrypted = nil
	c.JSON(http.StatusCreated, conn)
}

type updateMetaReq struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (h *CompanyHandler) updateConnectionMeta(c *gin.Context) {
	var req updateMetaReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.UpdateConnectionMeta(c.Request.Context(), companyID(c), c.Param("id"), req.Label, req.Description); err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

type updateDSNReq struct {
	DBType   string `json:"db_type" binding:"required"`
	DSN      string `json:"dsn"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
}

func (h *CompanyHandler) updateConnectionDSN(c *gin.Context) {
	var req updateDSNReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dsn, err := buildDSN(req.DBType, req.DSN, req.Host, req.Port, req.Username, req.Password, req.DBName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.UpdateConnectionDSN(c.Request.Context(), companyID(c), c.Param("id"), dsn); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *CompanyHandler) regenerateDescription(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	conn, err := h.svc.RegenerateDescription(ctx, companyID(c), c.Param("id"))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUnauthorized):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, context.DeadlineExceeded):
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "regeneration timed out; try again"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, conn)
}

func (h *CompanyHandler) setDefault(c *gin.Context) {
	if err := h.svc.SetDefaultConnection(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *CompanyHandler) deleteConnection(c *gin.Context) {
	if err := h.svc.DeleteConnection(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

type testConnReq struct {
	DBType   string `json:"db_type" binding:"required"`
	DSN      string `json:"dsn"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
}

func (h *CompanyHandler) testConnection(c *gin.Context) {
	var req testConnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dsn, err := buildDSN(req.DBType, req.DSN, req.Host, req.Port, req.Username, req.Password, req.DBName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.TestConnection(c.Request.Context(), req.DBType, dsn); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *CompanyHandler) listPhones(c *gin.Context) {
	out, err := h.svc.ListPhoneNumbers(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"phones": out})
}

type addPhoneReq struct {
	PhoneNumber string `json:"phone_number" binding:"required"`
	Label       string `json:"label"`
}

func (h *CompanyHandler) addPhone(c *gin.Context) {
	var req addPhoneReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.AddPhoneNumber(c.Request.Context(), companyID(c), req.PhoneNumber, req.Label); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "phone already registered"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (h *CompanyHandler) deletePhone(c *gin.Context) {
	if err := h.svc.RemovePhoneNumber(c.Request.Context(), companyID(c), c.Param("phone")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *CompanyHandler) getSettings(c *gin.Context) {
	company, err := h.svc.GetCompany(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"default_currency":    company.DefaultCurrency,
		"supported_currencies": app.SupportedCurrencies(),
	})
}

type updateSettingsReq struct {
	DefaultCurrency string `json:"default_currency" binding:"required"`
}

func (h *CompanyHandler) updateSettings(c *gin.Context) {
	var req updateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.UpdateCurrency(c.Request.Context(), companyID(c), req.DefaultCurrency); err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
