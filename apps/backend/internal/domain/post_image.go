package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// PostImage is a picture a tenant uploaded for the agent to draw into a post
// (T-G12): a product photograph on a promotion card.
//
// It is not a `SourceDocument` and not a `Document`. Those two are files to be
// *read* and files that were *generated*; this is a file to be *drawn*, with
// no pipeline behind it and nothing to extract from it. What it has instead is
// a name, because the name is how the model asks for it.
type PostImage struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	// Name is what the tenant calls it and what the model asks for. Unique per
	// company, case-insensitively.
	Name string `json:"name"`
	// Alt describes the picture for somebody who cannot see it. It travels
	// onto the slide's alt text, which is what a screen reader and a
	// publishing tool read.
	Alt string `json:"alt,omitempty"`
	// StorageKey is the object, normalised to PNG at upload.
	StorageKey string    `json:"-"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	ByteSize   int64     `json:"byte_size"`
	UploadedBy string    `json:"uploaded_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Bounds on a post image's metadata. The bytes are bounded by the service.
const (
	// MaxPostImageNameChars is the name the model types to ask for it. Bounded
	// for the reason a skill's name is (T-K1): it is a string a model has to
	// reproduce, and a name nobody can retype is a picture nobody can use.
	MaxPostImageNameChars = 80
	// MaxPostImageAltChars is one or two sentences, the length alt text is
	// useful at.
	MaxPostImageAltChars = 300
)

// Aspect is width over height, or 1 when either dimension is missing. The
// layout reserves a box from this before the bytes are decoded.
func (p *PostImage) Aspect() float64 {
	if p == nil || p.Width <= 0 || p.Height <= 0 {
		return 1
	}
	return float64(p.Width) / float64(p.Height)
}

// Validate refuses a row that breaks a bound, naming the field and the limit
// and never truncating — the rule `Skill.Validate` states and for the same
// reason: a silently shortened name is a picture the model can no longer find.
func (p *PostImage) Validate() error {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return fmt.Errorf("%w: an image needs a name — it is how the agent asks for it", ErrInvalidInput)
	}
	if n := utf8.RuneCountInString(name); n > MaxPostImageNameChars {
		return fmt.Errorf("%w: name is %d characters and the limit is %d", ErrInvalidInput, n, MaxPostImageNameChars)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(p.Alt)); n > MaxPostImageAltChars {
		return fmt.Errorf("%w: alt is %d characters and the limit is %d", ErrInvalidInput, n, MaxPostImageAltChars)
	}
	return nil
}

// PostImageRepository stores the tenant's picture library.
//
// Every method takes the company id and puts it in the query rather than
// comparing after the fetch, for `DocumentRepository`'s reason: a handler that
// reads first and checks second is one forgotten check away from serving
// another tenant's photograph.
type PostImageRepository interface {
	Insert(ctx context.Context, img *PostImage) error
	GetForCompany(ctx context.Context, companyID, id string) (*PostImage, error)
	ListByCompany(ctx context.Context, companyID string) ([]*PostImage, error)
	// FindByName resolves the name a model wrote to one image. Exact on the
	// lower-cased name first; the caller decides what to do with a miss.
	FindByName(ctx context.Context, companyID, name string) (*PostImage, error)
	Delete(ctx context.Context, companyID, id string) error
}

// PostImageKey is where a company's picture lives.
func PostImageKey(companyID, id string) string {
	return "post-images/" + companyID + "/" + id + ".png"
}
