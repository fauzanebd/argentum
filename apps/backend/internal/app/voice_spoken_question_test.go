package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// spokenFixture is u-1's two holds in th-1, beside two clips a browser could name
// and must not reach: another person's in the same conversation, and u-1's own in
// another conversation.
func spokenFixture() (*VoiceClips, *voiceFakeRepo) {
	repo := &voiceFakeRepo{clips: []*domain.VoiceClip{
		{ID: "c-1", CompanyID: "co-1", UserID: "u-1", ThreadID: "th-1", Transcript: "berapa stok gudang barat"},
		{ID: "c-2", CompanyID: "co-1", UserID: "u-1", ThreadID: "th-1", Transcript: "minggu ini"},
		{ID: "c-rina", CompanyID: "co-1", UserID: "u-rina", ThreadID: "th-1", Transcript: "gaji semua orang"},
		{ID: "c-other", CompanyID: "co-1", UserID: "u-1", ThreadID: "th-2", Transcript: "stok"},
	}}
	return NewVoiceClips(repo, nil), repo
}

// T-W9: two holds sent untouched are verbatim, whatever whitespace the composer
// put between them; the same holds with one word corrected are not.
func TestSpokenQuestionSaysWhetherTheMessageWasSentAsHeard(t *testing.T) {
	clips, _ := spokenFixture()
	ctx := context.Background()

	got, err := clips.SpokenQuestion(ctx, "co-1", "u-1", "th-1", []string{"c-1", "c-2"}, "berapa stok gudang barat\n  minggu ini ")
	if err != nil {
		t.Fatal(err)
	}
	if want := (&domain.SpokenQuestion{ClipIDs: []string{"c-1", "c-2"}, Verbatim: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("sent as heard: %+v, want %+v", got, want)
	}

	got, err = clips.SpokenQuestion(ctx, "co-1", "u-1", "th-1", []string{"c-1", "c-2"}, "berapa stok gudang timur minggu ini")
	if err != nil {
		t.Fatal(err)
	}
	if want := (&domain.SpokenQuestion{ClipIDs: []string{"c-1", "c-2"}, Verbatim: false}); !reflect.DeepEqual(got, want) {
		t.Errorf("corrected before sending: %+v, want %+v", got, want)
	}
}

// The ids come from a browser. Another person's clip, the caller's own from
// another conversation, a duplicate, a blank and one already swept are dropped,
// and what is left keeps the order it was dictated in.
func TestSpokenQuestionKeepsOnlyTheSendersClipsFromThisConversation(t *testing.T) {
	clips, repo := spokenFixture()
	ids := []string{"c-2", "c-rina", "c-other", "c-2", " ", "c-swept", "c-1"}

	got, err := clips.SpokenQuestion(context.Background(), "co-1", "u-1", "th-1", ids, "minggu ini berapa stok gudang barat")
	if err != nil {
		t.Fatal(err)
	}
	if want := (&domain.SpokenQuestion{ClipIDs: []string{"c-2", "c-1"}, Verbatim: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if want := []string{"c-2", "c-rina", "c-other", "c-swept", "c-1"}; !reflect.DeepEqual(repo.askedFor, want) {
		t.Errorf("asked the repository for %v, want %v: once each, no blank", repo.askedFor, want)
	}
}

// Naming nothing of the sender's is no record, not an empty one: a message whose
// every id was dropped must read exactly as a message that was typed.
func TestSpokenQuestionNamingNothingOfTheSendersIsNil(t *testing.T) {
	clips, _ := spokenFixture()
	for _, ids := range [][]string{{"c-rina"}, {"c-other"}, nil, {}} {
		got, err := clips.SpokenQuestion(context.Background(), "co-1", "u-1", "th-1", ids, "gaji semua orang")
		if err != nil || got != nil {
			t.Errorf("ids %v: got %+v, %v; want nil, nil", ids, got, err)
		}
	}
	var unwired *VoiceClips
	if got, err := unwired.SpokenQuestion(context.Background(), "co-1", "u-1", "th-1", []string{"c-1"}, "x"); err != nil || got != nil {
		t.Errorf("nil VoiceClips: got %+v, %v; want nil, nil", got, err)
	}
}

// A failed lookup is the caller's to decide about, so it comes back as an error
// rather than as "nothing was spoken".
func TestSpokenQuestionReturnsALookupFailure(t *testing.T) {
	clips, repo := spokenFixture()
	repo.forMessageErr = errors.New("connection refused")
	got, err := clips.SpokenQuestion(context.Background(), "co-1", "u-1", "th-1", []string{"c-1"}, "x")
	if err == nil || got != nil {
		t.Errorf("got %+v, %v; want nil and the error", got, err)
	}
}

// One send asks for at most maxClipsPerMessage clips, however many ids it names.
func TestSpokenQuestionAsksForABoundedNumberOfClips(t *testing.T) {
	clips, repo := spokenFixture()
	var ids []string
	for i := 0; i < 50; i++ {
		ids = append(ids, fmt.Sprintf("c-%d", i))
	}
	if _, err := clips.SpokenQuestion(context.Background(), "co-1", "u-1", "th-1", ids, "x"); err != nil {
		t.Fatal(err)
	}
	if len(repo.askedFor) != maxClipsPerMessage {
		t.Errorf("asked for %d clips, want %d", len(repo.askedFor), maxClipsPerMessage)
	}
}
