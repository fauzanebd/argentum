package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/postimage"
)

// PostImagesHandler exposes the tenant's picture library (T-G12): the product
// photographs the agent draws onto a promotion card.
type PostImagesHandler struct {
	svc *postimage.Service
}

func NewPostImagesHandler(svc *postimage.Service) *PostImagesHandler {
	return &PostImagesHandler{svc: svc}
}

// Register installs the routes.
//
// Uploading and deleting are admin-only through cmd/api/policy.go, for
// branding's reason: a picture here leaves the building on a public post, so
// putting one in the library is a company-level act. **Listing and reading are
// not** — a member composing a post needs to see what is available, and a
// library nobody can look at is a library nobody uses.
func (h *PostImagesHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/post-images", h.list)
	rg.POST("/post-images", h.upload)
	rg.GET("/post-images/:id/content", h.content)
	rg.DELETE("/post-images/:id", h.remove)
}

func (h *PostImagesHandler) list(c *gin.Context) {
	imgs, err := h.svc.List(c.Request.Context(), companyID(c))
	if err != nil {
		writePostImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"images": imgs,
		"limits": gin.H{
			"max_bytes":      postimage.MaxBytes,
			"max_edge":       postimage.MaxEdge,
			"max_name_chars": domain.MaxPostImageNameChars,
			"max_alt_chars":  domain.MaxPostImageAltChars,
		},
	})
}

// upload takes the file, the name the model will ask for it by, and the alt
// text. Multipart rather than JSON with a base64 field, so a two-megabyte
// photograph is two megabytes on the wire rather than 2.7.
func (h *PostImagesHandler) upload(c *gin.Context) {
	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected a file in the \"image\" field"})
		return
	}
	if file.Size > postimage.MaxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": "the image must be 4 MB or smaller",
		})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the upload"})
		return
	}
	defer func() { _ = f.Close() }()

	// The filename is the fallback name, because a shop owner uploading
	// `jeruk-cara-cara.jpg` has already named it and should not have to type
	// it twice. It is only a fallback: a name in the form always wins.
	name := c.PostForm("name")
	if name == "" {
		name = trimExtension(file.Filename)
	}

	img, err := h.svc.Upload(c.Request.Context(), companyID(c), userID(c), name, c.PostForm("alt"), f)
	if err != nil {
		writePostImageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, img)
}

// content serves the picture itself, for the dashboard's picker.
//
// Authenticated and company-scoped, never presigned: this is the same rule
// the carousel's page route keeps (T-G6). A presigned URL in a picker is a
// link that works for an hour and then shows a broken image, and the picker
// is a screen somebody leaves open.
func (h *PostImagesHandler) content(c *gin.Context) {
	img, body, err := h.svc.Bytes(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		writePostImageError(c, err)
		return
	}
	// Private: the picture belongs to one tenant, and a shared cache that
	// keyed on the path alone would serve it to the next one.
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("Content-Disposition", `inline; filename="`+img.Name+`.png"`)
	c.Data(http.StatusOK, "image/png", body)
}

func (h *PostImagesHandler) remove(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), companyID(c), c.Param("id")); err != nil {
		writePostImageError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writePostImageError maps the domain errors onto status codes. A name
// collision is a 409 with the sentence that names the fix, because the name is
// the model's handle and "pick another" is the whole remedy.
func writePostImageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such image"})
	case errors.Is(err, domain.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{
			"error": "an image with that name already exists; give this one a different name, " +
				"because the name is how the agent asks for it",
		})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not complete the request"})
	}
}

// trimExtension drops a trailing file extension from an uploaded filename.
func trimExtension(name string) string {
	for i := len(name) - 1; i >= 0 && len(name)-i <= 6; i-- {
		if name[i] == '.' {
			return name[:i]
		}
	}
	return name
}
