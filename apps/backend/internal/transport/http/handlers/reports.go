package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/pdf"
	"github.com/fauzanebd/argentum/internal/report/sample"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// ReportsHandler exposes tenant report branding and its live preview.
type ReportsHandler struct {
	svc       *branding.Service
	companies domain.CompanyRepository
}

func NewReportsHandler(svc *branding.Service, companies domain.CompanyRepository) *ReportsHandler {
	return &ReportsHandler{svc: svc, companies: companies}
}

// Register installs the routes. Every one of them is admin-only through
// cmd/api/policy.go — branding is what leaves the building on a document, so
// changing it is a company-level act rather than a personal preference.
func (h *ReportsHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/reports/branding", h.getBranding)
	rg.PUT("/reports/branding", h.updateBranding)
	rg.POST("/reports/branding/logo", h.uploadLogo)
	rg.POST("/reports/preview", h.preview)
}

// brandingResponse carries the record plus what the dashboard needs to render
// its form without hard-coding our constants: the defaults a blank field falls
// back to, and the limits it should enforce before a round trip.
type brandingResponse struct {
	Branding *domain.ReportBranding `json:"branding"`
	Defaults brandingDefaults       `json:"defaults"`
	Limits   brandingLimits         `json:"limits"`
}

type brandingDefaults struct {
	PrimaryColor string `json:"primary_color"`
	CompanyName  string `json:"company_name"`
	Locale       string `json:"locale"`
}

type brandingLimits struct {
	MinContrast  float64 `json:"min_contrast"`
	MaxLogoBytes int     `json:"max_logo_bytes"`
	MaxLogoEdge  int     `json:"max_logo_edge"`
}

func (h *ReportsHandler) respond(c *gin.Context, status int, b *domain.ReportBranding) {
	name := ""
	if h.companies != nil {
		if company, err := h.companies.GetByID(c.Request.Context(), companyID(c)); err == nil && company != nil {
			name = company.Name
		}
	}
	c.JSON(status, brandingResponse{
		Branding: b,
		Defaults: brandingDefaults{
			PrimaryColor: theme.ColorPrimary.Hex(),
			CompanyName:  name,
			Locale:       "en",
		},
		Limits: brandingLimits{
			MinContrast:  theme.MinBrandContrast,
			MaxLogoBytes: branding.MaxLogoBytes,
			MaxLogoEdge:  branding.MaxLogoEdge,
		},
	})
}

func (h *ReportsHandler) getBranding(c *gin.Context) {
	b, err := h.svc.Get(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.respond(c, http.StatusOK, b)
}

func (h *ReportsHandler) updateBranding(c *gin.Context) {
	var req domain.ReportBranding
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	saved, err := h.svc.Save(c.Request.Context(), companyID(c), &req)
	if err != nil {
		writeBrandingError(c, err)
		return
	}
	h.respond(c, http.StatusOK, saved)
}

// uploadLogo takes the file and returns its key. It deliberately does not save
// the branding record: the dashboard uploads while the rest of the form is
// still being edited, and writing the key here would mean an abandoned edit had
// already changed what the next report looks like.
func (h *ReportsHandler) uploadLogo(c *gin.Context) {
	file, err := c.FormFile("logo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected a file in the \"logo\" field"})
		return
	}
	if file.Size > branding.MaxLogoBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("the logo must be %d KB or smaller", branding.MaxLogoBytes>>10),
		})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the upload"})
		return
	}
	defer func() { _ = f.Close() }()

	key, err := h.svc.UploadLogo(c.Request.Context(), companyID(c), f)
	if err != nil {
		writeBrandingError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"logo_key": key})
}

// preview renders the sample document with the branding in the request body,
// which is what makes it a *live* preview: the customer sees the colour they
// are choosing before they save it, not after.
//
// It returns a PDF rather than JSON with a URL so the dashboard can point an
// <iframe> at it without a second rendering path or a PDF.js dependency.
func (h *ReportsHandler) preview(c *gin.Context) {
	var req domain.ReportBranding
	// An empty body means "preview what is saved", which is the shape the
	// Reports tab loads with.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else {
		saved, err := h.svc.Get(c.Request.Context(), companyID(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		req = *saved
	}

	req.Normalize()
	if err := branding.Validate(&req); err != nil {
		writeBrandingError(c, err)
		return
	}

	cfg := h.svc.Preview(c.Request.Context(), companyID(c), &req, func(err error) {
		logrus.WithError(err).WithField("company_id", companyID(c)).
			Warn("reports preview: branding partially unresolved")
	})

	currency := ""
	if h.companies != nil {
		if company, err := h.companies.GetByID(c.Request.Context(), companyID(c)); err == nil && company != nil {
			currency = company.DefaultCurrency
		}
	}
	if currency == "" {
		// Same default the company row is created with (postgres.CompanyRepo.Create).
		currency = "USD"
	}

	// A fixed timestamp, so re-previewing the same branding produces the same
	// bytes and the browser's own cache does the right thing.
	genAt := time.Date(2026, time.January, 31, 9, 0, 0, 0, time.UTC)
	doc := sample.Document(genAt, currency, cfg.Locale)

	out, err := pdf.Render(doc, pdf.Options{
		Brand:    cfg.PDF(),
		Currency: currency,
		Locale:   cfg.Locale,
		Now:      genAt,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Disposition", `inline; filename="branding-preview.pdf"`)
	c.Data(http.StatusOK, "application/pdf", out)
}

// writeBrandingError maps the service's errors to statuses. Validation failures
// carry their measured numbers in the message, so the message is passed through
// verbatim — it is written for the person looking at the colour picker.
func writeBrandingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
