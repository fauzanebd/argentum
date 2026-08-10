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
type CompanyHandler struct {
	svc       *app.CompanyService
	embedding *app.EmbeddingService
}

// NewCompanyHandler wires the company service. embeddingSvc is optional;
// pass nil to disable the reindex-embeddings endpoint (it will return 503).
func NewCompanyHandler(svc *app.CompanyService, embeddingSvc *app.EmbeddingService) *CompanyHandler {
	return &CompanyHandler{svc: svc, embedding: embeddingSvc}
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
	rg.POST("/connections/:id/rescan", h.rescanSource)
	rg.POST("/connections/:id/reindex-embeddings", h.reindexEmbeddings)
	rg.POST("/connections/:id/test-rag", h.testRAG)
	rg.DELETE("/connections/:id", h.deleteConnection)
	rg.POST("/connections/test", h.testConnection)
	rg.POST("/connections/:id/test", h.testConnectionByID)

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

// userID is companyID's counterpart. Both read values that middleware.Auth
// sets, and both use the comma-ok form: a bare `v.(string)` panics on any
// route reachable without Auth in front of it, turning a routing mistake into
// a 500 and a stack trace in the log rather than an empty tenant the handler
// can reject.
func userID(c *gin.Context) string {
	v, _ := c.Get("user_id")
	s, _ := v.(string)
	return s
}

// sslModes are the transport-security settings the host/port form may choose
// between, per driver. They exist because the form used to pin postgres to
// `sslmode=require` with no way to say otherwise, so a database that does not
// speak TLS — every local one, and plenty of internal ones behind a VPN — could
// be registered through the UI and could never be reached, which the tenant
// discovered a turn later.
//
// `require` stays the default everywhere. Choosing less is a decision an admin
// makes explicitly, and it is one they could already make by pasting a raw DSN;
// what changes is that they no longer have to know the DSN grammar to make it.
//
// The drivers do not agree on what `require` means, which is the second thing
// this table exists to absorb. libpq's `require` encrypts without checking who
// answered; go-sql-driver's `tls=true` verifies the chain and the address, so a
// mysql server holding the certificate mysqld generates for itself is refused
// with `x509: … doesn't contain any IP SANs` — correctly, but for a reason no
// control on the form could speak to. `skip-verify` names the middle ground
// explicitly for both: encrypted, unverified, and never a silent fall back to
// plaintext the way `prefer` is.
//
// mysql has no `verify-ca`: go-sql-driver reaches it only through a registered
// tls.Config carrying the root pool, so accepting the word here would have to
// mean skip-verify — a promise to check the CA, kept by checking nothing. It is
// refused instead, and `verify-full` is the mode that verifies.
var sslModes = map[string]map[string]string{
	"postgres": {
		"require": "require", "prefer": "prefer", "disable": "disable",
		"skip-verify": "require",
		"verify-ca":   "verify-ca", "verify-full": "verify-full",
	},
	"mysql": {
		"require": "true", "prefer": "preferred", "disable": "false",
		"skip-verify": "skip-verify", "verify-full": "true",
	},
}

// resolveSSLMode maps a requested mode onto the driver's own spelling. An empty
// or unknown request is `require`: the safe end, and the behaviour every
// connection registered before this shipped already has.
func resolveSSLMode(dbType, requested string) (string, error) {
	modes, ok := sslModes[dbType]
	if !ok {
		return "", nil
	}
	if requested == "" {
		requested = "require"
	}
	driverValue, ok := modes[requested]
	if !ok {
		return "", fmt.Errorf("unsupported ssl_mode %q for %s", requested, dbType)
	}
	return driverValue, nil
}

// buildDSN constructs a driver-specific DSN from discrete fields.
// If raw is non-empty it is returned as-is (advanced mode).
func buildDSN(dbType, raw, host, port, user, pass, dbname, sslMode string) (string, error) {
	if raw != "" {
		return raw, nil
	}
	if host == "" || port == "" || dbname == "" {
		return "", fmt.Errorf("host, port and database name are required")
	}
	driverSSL, err := resolveSSLMode(dbType, sslMode)
	if err != nil {
		return "", err
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
		q.Set("sslmode", driverSSL)
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
			TLSConfig:            driverSSL,
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
		q.Set("TrustServerCertificate", "true")
		q.Set("tlsmin", "1.0")
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
	// SSLMode is the transport security the host/port form chose. Empty is
	// `require`, which is what every connection registered before this shipped
	// already has.
	SSLMode string `json:"ssl_mode"`
	// SkipTest stores a source that could not be reached. The default is to
	// refuse: a source added through the form and never opened fails a turn
	// later, after an agent has spent its budget discovering it, and the tenant
	// reads that as the agent being broken. But a database behind a VPN that is
	// down at 4pm is not a configuration error — so the refusal is a 400 an
	// admin can override, not a wall.
	SkipTest bool `json:"skip_test"`
}

func (h *CompanyHandler) addConnection(c *gin.Context) {
	var req addConnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dsn, err := buildDSN(req.DBType, req.DSN, req.Host, req.Port, req.Username, req.Password, req.DBName, req.SSLMode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Open it before storing it. The failure this prevents is not a bad
	// password — it is a source that looks registered, is listed in the agent's
	// own catalog, and cannot be read: the agent picks it, spends its budget,
	// and answers that it has no access to the data.
	if !req.SkipTest {
		if err := h.svc.TestConnection(c.Request.Context(), req.DBType, dsn); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":            err.Error(),
				"connection_error": true,
			})
			return
		}
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
	Label                string `json:"label"`
	Description          string `json:"description"`
	EnableTableEmbedding *bool  `json:"enable_table_embedding,omitempty"`
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
	if req.EnableTableEmbedding != nil {
		if err := h.svc.SetConnectionEmbeddingToggle(c.Request.Context(), companyID(c), c.Param("id"), *req.EnableTableEmbedding); err != nil {
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
	// SSLMode travels with the fields it belongs to. A rotation through the
	// host/port form that dropped it would silently re-pin the connection to
	// `require` — which is how a source that worked yesterday stops working
	// after an unrelated password change.
	SSLMode string `json:"ssl_mode"`
}

func (h *CompanyHandler) updateConnectionDSN(c *gin.Context) {
	var req updateDSNReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dsn, err := buildDSN(req.DBType, req.DSN, req.Host, req.Port, req.Username, req.Password, req.DBName, req.SSLMode)
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

func (h *CompanyHandler) reindexEmbeddings(c *gin.Context) {
	if h.embedding == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding service is not configured"})
		return
	}
	// Reindex hits an external embedding API once per ~96 tables. Big
	// schemas (hundreds of tables) can run a couple of minutes, so we cap
	// the request context generously rather than pacing batches.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	res, err := h.embedding.ReindexSource(ctx, companyID(c), c.Param("id"))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUnauthorized):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, context.DeadlineExceeded):
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "reindex timed out; try again or shrink the source"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tables":        res.Tables,
		"skipped_noise": res.Skipped,
		"duration_ms":   res.Duration.Milliseconds(),
		"indexed_at":    res.IndexedAt,
	})
}

// testRAG exercises the table-picker path for an ad-hoc query without a chat
// turn: returns top-K hits, the filtered schema the agent would see, and
// per-step timings. Useful for verifying a fresh reindex produced useful
// shortlists before sending real user traffic.
func (h *CompanyHandler) testRAG(c *gin.Context) {
	if h.embedding == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding service is not configured"})
		return
	}
	var body struct {
		Query string `json:"query" binding:"required"`
		TopK  int    `json:"top_k"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	res, err := h.embedding.TestRetrieval(ctx, companyID(c), c.Param("id"), body.Query, body.TopK)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUnauthorized):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, context.DeadlineExceeded):
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "test-rag timed out"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, res)
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

// rescanSource re-reads what this source says the business is (T-B2).
//
// 202, not 200: the answer is written by the worker, and a tenant who pressed a
// button that returned "done" while nothing had happened yet would reload the
// form and find the old draft. The pass is a no-op when the schema has not
// changed, so the button is safe to press twice.
func (h *CompanyHandler) rescanSource(c *gin.Context) {
	err := h.svc.RescanSource(c.Request.Context(), companyID(c), c.Param("id"))
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusAccepted, gin.H{"queued": true})
	}
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
	// SSLMode so the Test button tests what Save will store. Without it the
	// test passes on `require` and the save stores `disable`, or the reverse —
	// a green test for a connection that is not the one being registered.
	SSLMode string `json:"ssl_mode"`
}

func (h *CompanyHandler) testConnection(c *gin.Context) {
	var req testConnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dsn, err := buildDSN(req.DBType, req.DSN, req.Host, req.Port, req.Username, req.Password, req.DBName, req.SSLMode)
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

func (h *CompanyHandler) testConnectionByID(c *gin.Context) {
	if err := h.svc.TestConnectionByID(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		switch {
		case errors.Is(err, domain.ErrUnauthorized):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		}
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
	mode := company.PIIRedactionMode
	if !mode.Valid() {
		// A row written before migration 045, or one whose column was dropped by
		// the down migration. The turn reads it as strict; the form has to show
		// the same thing, or an admin is looking at a setting that is not the one
		// in force.
		mode = domain.PIIRedactionStrict
	}
	c.JSON(http.StatusOK, gin.H{
		"default_currency":     company.DefaultCurrency,
		"supported_currencies": app.SupportedCurrencies(),
		"pii_redaction_mode":   mode,
		"pii_redaction_modes":  domain.PIIRedactionModes(),
	})
}

// updateSettingsReq carries whichever settings the caller is changing. Both
// fields are optional so a client that only knows about one of them — the
// dashboard before this ticket, any integrator's script — keeps working; a body
// with neither is a bad request rather than a silent no-op.
type updateSettingsReq struct {
	DefaultCurrency  string                  `json:"default_currency"`
	PIIRedactionMode domain.PIIRedactionMode `json:"pii_redaction_mode"`
}

func (h *CompanyHandler) updateSettings(c *gin.Context) {
	var req updateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.DefaultCurrency == "" && req.PIIRedactionMode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no settings in request: send default_currency, pii_redaction_mode, or both"})
		return
	}
	if req.DefaultCurrency != "" {
		if err := h.svc.UpdateCurrency(c.Request.Context(), companyID(c), req.DefaultCurrency); err != nil {
			writeSettingsErr(c, err)
			return
		}
	}
	if req.PIIRedactionMode != "" {
		if err := h.svc.UpdatePIIRedactionMode(c.Request.Context(), companyID(c), req.PIIRedactionMode); err != nil {
			writeSettingsErr(c, err)
			return
		}
	}
	c.Status(http.StatusNoContent)
}

func writeSettingsErr(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrInvalidInput) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
