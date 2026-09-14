package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

type fakeVoiceEraser struct {
	companies []string
	err       error
}

func (f *fakeVoiceEraser) EraseCompany(_ context.Context, companyID string) (int, error) {
	f.companies = append(f.companies, companyID)
	return 2, f.err
}

// "T-H6 erasure removes a user's clips and their objects" — built as a
// company's, because T-H6 erases companies; see coverage/voice.md.
func TestEraseCompanyDataTakesTheVoiceClipsWithIt(t *testing.T) {
	voice := &fakeVoiceEraser{}
	records := newFakeRecords()
	svc := NewRetentionService(&fakeRetentionRepo{}, records, &fakeRetentionCompanies{}).WithVoiceClips(voice)

	rec, err := svc.EraseCompanyData(context.Background(), "co-1", "user-9")
	if err != nil {
		t.Fatalf("EraseCompanyData: %v", err)
	}
	if rec.Status != domain.ErasureStatusCompleted {
		t.Errorf("status = %q", rec.Status)
	}
	if len(voice.companies) != 1 || voice.companies[0] != "co-1" {
		t.Errorf("voice clips erased for %v, want [co-1]", voice.companies)
	}
}

func TestEraseCompanyDataFailsWhenTheRecordingsWouldNotDelete(t *testing.T) {
	voice := &fakeVoiceEraser{err: errors.New("bucket unreachable")}
	records := newFakeRecords()
	svc := NewRetentionService(&fakeRetentionRepo{}, records, &fakeRetentionCompanies{}).WithVoiceClips(voice)

	if _, err := svc.EraseCompanyData(context.Background(), "co-1", "user-9"); err == nil {
		t.Fatal("EraseCompanyData returned nil while the recordings were still in the bucket")
	}
	if len(records.failed) != 1 {
		t.Errorf("recorded %d failures, want 1: an erasure that left the audio is not a completed one", len(records.failed))
	}
}
