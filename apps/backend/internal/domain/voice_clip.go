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
// decision 13). What they send is an ordinary message, which since T-W9 names the
// clips it was dictated from in its metadata — SpokenQuestion says why the link
// is on the message and not here.
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

// SpokenQuestion is what a user message records about the recordings it was
// dictated from (T-W9), under its metadata's MessageMetadataVoice key.
//
// **On the message, not as a column on voice_clips**, because the fact has to
// outlive the clip. A clip is deleted after SPEECH_RETENTION_DAYS; "this question
// was spoken, and sent as it was heard" is still true of the message a year later.
// It is also the read research 08 §6's unknown 6 needs — whether anybody talks to
// it — which a count of clips cannot answer: a clip says somebody pressed the
// button, and only a message says they sent what they said.
type SpokenQuestion struct {
	// ClipIDs are the recordings, in the order they were dictated. A clip may be
	// gone already; the id stays as the record that there was one.
	ClipIDs []string `json:"clip_ids"`
	// Verbatim says the message is exactly what was heard, whitespace aside.
	// False means the person changed it before sending — corrected a figure,
	// added to it, typed around it — which is decision 13's edit step in use.
	Verbatim bool `json:"verbatim"`
}

// MessageMetadataVoice is the metadata key a SpokenQuestion is stored under.
const MessageMetadataVoice = "voice"

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
	// ForMessage lists the clips among ids that userID recorded in threadID of
	// companyID: the only clips a message they send there may name. ID and
	// Transcript are filled. An id that is another person's, another
	// conversation's, malformed or already swept is absent, not an error.
	ForMessage(ctx context.Context, companyID, userID, threadID string, ids []string) ([]*VoiceClip, error)
}
