package domain

import (
	"context"
	"time"
)

// SpokenAnswer is an agent's answer as it was read aloud (T-W8): the words a
// model reduced it to, and the audio they became — or the reason they were
// refused.
//
// A cache and a record at once. The audio is what makes a second press of the
// play button free. The spoken text beside it is what makes the reduction
// checkable afterwards: roadmap 11's decision 14 makes a spoken figure the
// written answer does not state a defect, and a defect nobody can read back is a
// defect nobody can show. A refusal is kept for the first reason — it is what a
// second press answers without paying a model to be refused again.
type SpokenAnswer struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	// MessageID is empty once the message has been deleted, which is also what
	// makes the sweep delete the row and its audio.
	MessageID string `json:"message_id,omitempty"`
	// ObjectKey is empty on a refusal.
	ObjectKey string `json:"-"`
	MimeType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	// SpokenText is what was synthesised — or, on a refusal, what the model wrote
	// and was refused.
	SpokenText string `json:"spoken_text"`
	// Refusal is why the spoken text was not synthesised, naming both figures
	// when a figure was the reason. Empty when it was synthesised.
	Refusal string `json:"refusal,omitempty"`
	Voice   string `json:"voice,omitempty"`
	// Model is the synthesis model. The reduction's model is on its own
	// llm_call usage row.
	Model string `json:"model,omitempty"`
	// Chars is what the synthesis was billed on: the spoken text's length.
	Chars     int       `json:"chars"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SpokenAnswerKeyPrefix is where a company's spoken answers are stored: inside
// VoiceClipKeyPrefix, so the erasure that removes a company's recordings by
// prefix removes these with them, rows or no rows.
func SpokenAnswerKeyPrefix(companyID string) string {
	return VoiceClipKeyPrefix(companyID) + "answers/"
}

// SpokenAnswerRepository persists spoken answers.
type SpokenAnswerRepository interface {
	// ForMessage returns the spoken form of one message, or ErrNotFound.
	ForMessage(ctx context.Context, companyID, messageID string) (*SpokenAnswer, error)
	// Save writes the spoken form if the message belongs to the company, and
	// answers ErrNotFound if it does not. An earlier row for the same message is
	// replaced.
	Save(ctx context.Context, a *SpokenAnswer) error
	// Due lists rows the sweep should delete — past ExpiresAt, or whose message
	// is gone — across every company. Only ID, CompanyID and ObjectKey are
	// filled: it is a delete list, not a read.
	Due(ctx context.Context, now time.Time, limit int) ([]*SpokenAnswer, error)
	// Delete removes one row.
	Delete(ctx context.Context, companyID, id string) error
	// DeleteForCompany removes every row a company has, and returns how many.
	DeleteForCompany(ctx context.Context, companyID string) (int, error)
}
