package domain

import (
	"context"
	"time"
)

// VoiceClip is one question somebody spoke (T-W7): what was heard, and — until
// ExpiresAt — the audio it was heard from.
//
// It is not a message and never becomes one by itself. The transcript goes back
// to the person who spoke, who sends it, edits it or discards it (roadmap 11,
// decision 13); what they send is an ordinary message with no link back here
// (migration 087 says why the link is not built yet).
type VoiceClip struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	// ThreadID is empty once the conversation has been deleted, which is also
	// what makes the sweep delete the clip.
	ThreadID string `json:"thread_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	// ObjectKey is empty when the audio was not kept.
	ObjectKey string `json:"-"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	// Seconds is the length the clip was billed on.
	Seconds    float64 `json:"seconds"`
	Transcript string  `json:"transcript"`
	// Language is the hint the provider was sent, not a detection.
	Language  string    `json:"language,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// VoiceClipKeyPrefix is where every clip a company has is stored. Erasure
// removes the prefix rather than walking rows, so a clip whose row was never
// written — an insert that failed after its upload succeeded — goes with the
// rest.
func VoiceClipKeyPrefix(companyID string) string { return "voice/" + companyID + "/" }

// VoiceClipRepository persists clips.
type VoiceClipRepository interface {
	// Create writes the clip if its conversation belongs to its company, and
	// answers ErrNotFound if it does not.
	Create(ctx context.Context, c *VoiceClip) error
	// Due lists clips the sweep should delete — past ExpiresAt, or whose
	// conversation is gone — across every company, oldest first. Only ID,
	// CompanyID and ObjectKey are filled: it is a delete list, not a read.
	Due(ctx context.Context, now time.Time, limit int) ([]*VoiceClip, error)
	// Delete removes one clip's row.
	Delete(ctx context.Context, companyID, id string) error
	// DeleteForCompany removes every clip row a company has, and returns how
	// many.
	DeleteForCompany(ctx context.Context, companyID string) (int, error)
}
