package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/speech"
)

type voiceFakeTranscriber struct {
	text    string
	seconds float64
	// cost is a charge the provider reports, as OpenRouter does.
	cost  float64
	err   error
	calls int
	lang  string
	mt    string
	audio []byte
}

func (f *voiceFakeTranscriber) Transcribe(_ context.Context, r io.Reader, mt, lang string) (speech.Transcript, error) {
	f.calls++
	f.lang, f.mt = lang, mt
	f.audio, _ = io.ReadAll(r)
	if f.err != nil {
		return speech.Transcript{}, f.err
	}
	return speech.Transcript{Text: f.text, Seconds: f.seconds, CostUSD: f.cost}, nil
}
func (f *voiceFakeTranscriber) Enabled() bool { return true }
func (f *voiceFakeTranscriber) Model() string { return "whisper-large-v3-turbo" }

type voiceFakeRepo struct {
	created   []*domain.VoiceClip
	createErr error
	due       []*domain.VoiceClip
	deleted   []string
	deleteErr error
	erasedFor []string
	eraseN    int
	// clips is what ForMessage searches, and askedFor what it was last asked.
	clips         []*domain.VoiceClip
	askedFor      []string
	forMessageErr error
}

// ForMessage filters on all three predicates the real statement has, so a test
// that hands it another person's clip is testing the caller's trust, not the
// fake's.
func (r *voiceFakeRepo) ForMessage(_ context.Context, companyID, userID, threadID string, ids []string) ([]*domain.VoiceClip, error) {
	r.askedFor = ids
	if r.forMessageErr != nil {
		return nil, r.forMessageErr
	}
	var out []*domain.VoiceClip
	for _, c := range r.clips {
		for _, id := range ids {
			if c.ID == id && c.CompanyID == companyID && c.UserID == userID && c.ThreadID == threadID {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func (r *voiceFakeRepo) Create(_ context.Context, c *domain.VoiceClip) error {
	if r.createErr != nil {
		return r.createErr
	}
	cp := *c
	r.created = append(r.created, &cp)
	return nil
}

// Due answers what is due and not yet deleted, so a sweep that deletes makes
// progress and one that does not reads the same list again — as the real
// statement would.
func (r *voiceFakeRepo) Due(_ context.Context, _ time.Time, limit int) ([]*domain.VoiceClip, error) {
	var out []*domain.VoiceClip
	for _, c := range r.due {
		gone := false
		for _, id := range r.deleted {
			gone = gone || id == c.ID
		}
		if !gone && len(out) < limit {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *voiceFakeRepo) Delete(_ context.Context, _, id string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *voiceFakeRepo) DeleteForCompany(_ context.Context, companyID string) (int, error) {
	r.erasedFor = append(r.erasedFor, companyID)
	return r.eraseN, nil
}

type voiceFakeStore struct {
	uploads   map[string][]byte
	uploadErr error
	removed   []string
	removeErr map[string]error
	prefixes  []string
	prefixErr error
}

func (s *voiceFakeStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	if s.uploadErr != nil {
		return "", s.uploadErr
	}
	if s.uploads == nil {
		s.uploads = map[string][]byte{}
	}
	s.uploads[key], _ = io.ReadAll(r)
	return key, nil
}

func (s *voiceFakeStore) RemoveKey(_ context.Context, key string) error {
	if err := s.removeErr[key]; err != nil {
		return err
	}
	s.removed = append(s.removed, key)
	return nil
}

func (s *voiceFakeStore) RemovePrefix(_ context.Context, prefix string) error {
	s.prefixes = append(s.prefixes, prefix)
	return s.prefixErr
}

type voiceUsageCall struct {
	company, thread, model string
	seconds, cost          float64
}

type voiceFakeUsage struct{ calls []voiceUsageCall }

func (u *voiceFakeUsage) RecordTranscription(_ context.Context, companyID, threadID, model string, seconds, cost float64) {
	u.calls = append(u.calls, voiceUsageCall{companyID, threadID, model, seconds, cost})
}

type voiceFakeBudget struct{ st BudgetState }

func (b voiceFakeBudget) CheckBudget(context.Context, string) (BudgetState, error) { return b.st, nil }

type voiceFakeCompanies struct{ currency string }

func (c voiceFakeCompanies) GetByID(context.Context, string) (*domain.Company, error) {
	return &domain.Company{DefaultCurrency: c.currency}, nil
}

type voiceFakeBranding struct{ locale string }

func (b voiceFakeBranding) Get(context.Context, string) (*domain.ReportBranding, error) {
	return &domain.ReportBranding{Locale: b.locale}, nil
}

const voiceSecretAudio = "SECRETAUDIOFRAMES"

var voiceNow = time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)

func voiceInput() VoiceInput {
	return VoiceInput{
		CompanyID: "co-1", UserID: "u-1", ThreadID: "th-1",
		Audio:     append([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte(voiceSecretAudio)...),
		MediaType: "audio/webm", Ext: "webm", DeclaredSeconds: 11,
	}
}

type voiceRig struct {
	tr    *voiceFakeTranscriber
	repo  *voiceFakeRepo
	store *voiceFakeStore
	usage *voiceFakeUsage
	svc   *VoiceService
}

func newVoiceRig(currency string) *voiceRig {
	r := &voiceRig{
		tr:    &voiceFakeTranscriber{text: "berapa stok gudang barat", seconds: 12.5},
		repo:  &voiceFakeRepo{},
		store: &voiceFakeStore{},
		usage: &voiceFakeUsage{},
	}
	clips := NewVoiceClips(r.repo, r.store).WithClock(fixedClock(voiceNow))
	r.svc = NewVoiceService(r.tr, clips, r.usage, voiceFakeCompanies{currency: currency}, 60, 7).
		WithClock(fixedClock(voiceNow))
	return r
}

func TestVoiceTranscribesKeepsAndBillsOnTheMeasuredLength(t *testing.T) {
	rig := newVoiceRig("IDR")
	in := voiceInput()
	out, err := rig.svc.Transcribe(context.Background(), in)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if out.Text != "berapa stok gudang barat" || out.Language != "id" || out.Seconds != 12.5 {
		t.Errorf("result = %+v", out)
	}
	if string(rig.tr.audio) != string(in.Audio) || rig.tr.mt != "audio/webm" {
		t.Errorf("the provider was sent %d bytes as %q", len(rig.tr.audio), rig.tr.mt)
	}
	// Billed on what the provider measured, not on the 11 seconds the client said.
	if len(rig.usage.calls) != 1 || rig.usage.calls[0] != (voiceUsageCall{"co-1", "th-1", "whisper-large-v3-turbo", 12.5, 0}) {
		t.Errorf("usage = %+v", rig.usage.calls)
	}
	if out.Clip == nil || len(rig.repo.created) != 1 {
		t.Fatalf("clip = %+v, rows = %d", out.Clip, len(rig.repo.created))
	}
	row := rig.repo.created[0]
	wantKey := "voice/co-1/" + row.ID + ".webm"
	if row.ObjectKey != wantKey || string(rig.store.uploads[wantKey]) != string(in.Audio) {
		t.Errorf("audio stored under %q (%d keys), row names %q", wantKey, len(rig.store.uploads), row.ObjectKey)
	}
	if !row.ExpiresAt.Equal(voiceNow.Add(7*24*time.Hour)) || row.Transcript != out.Text || row.UserID != "u-1" {
		t.Errorf("row = %+v", row)
	}
}

func TestVoiceProviderFailureKeepsNothingAndBillsNothing(t *testing.T) {
	rig := newVoiceRig("IDR")
	rig.tr.err = &speech.ProviderError{Status: 503, Message: "over capacity"}
	_, err := rig.svc.Transcribe(context.Background(), voiceInput())
	if !errors.Is(err, ErrTranscriptionFailed) {
		t.Fatalf("error = %v, want ErrTranscriptionFailed", err)
	}
	if len(rig.store.uploads) != 0 || len(rig.repo.created) != 0 || len(rig.usage.calls) != 0 {
		t.Errorf("after a failure: %d objects, %d rows, %d usage events — want none",
			len(rig.store.uploads), len(rig.repo.created), len(rig.usage.calls))
	}
}

func TestVoiceRefusesATenantWithoutCreditsBeforeTheProvider(t *testing.T) {
	rig := newVoiceRig("IDR")
	rig.svc.WithBudget(voiceFakeBudget{st: BudgetState{Verdict: BudgetExhausted}})
	_, err := rig.svc.Transcribe(context.Background(), voiceInput())
	if !errors.Is(err, domain.ErrInsufficientCredits) {
		t.Fatalf("error = %v, want ErrInsufficientCredits", err)
	}
	if rig.tr.calls != 0 {
		t.Errorf("the provider was called %d times for a tenant out of credits", rig.tr.calls)
	}
}

// "Language hint defaults to the tenant's, not to English."
func TestVoiceLanguageIsTheTenantsAndNeverEnglishByDefault(t *testing.T) {
	cases := []struct {
		name, currency, locale, override, want string
	}{
		{"the person's own choice", "IDR", "id", "en", "en"},
		{"the tenant's stated locale", "IDR", "en", "", "en"},
		{"a rupiah tenant with no locale", "IDR", "", "", "id"},
		// Not "en": a tenant that stated nothing has not said it speaks English,
		// and an empty hint lets the provider detect what was said.
		{"a tenant that stated nothing", "THB", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newVoiceRig(c.currency)
			rig.svc.WithBranding(voiceFakeBranding{locale: c.locale})
			in := voiceInput()
			in.Language = c.override
			out, err := rig.svc.Transcribe(context.Background(), in)
			if err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if rig.tr.lang != c.want || out.Language != c.want {
				t.Errorf("hint sent %q, reported %q, want %q", rig.tr.lang, out.Language, c.want)
			}
		})
	}
}

// A charge the provider reported — OpenRouter's `usage.cost` — reaches the
// ledger as it was reported, beside the seconds, instead of being priced again
// here (research 08 §2e).
func TestVoicePassesTheProvidersChargeToTheLedger(t *testing.T) {
	rig := newVoiceRig("IDR")
	rig.tr.cost = 0.0000075
	if _, err := rig.svc.Transcribe(context.Background(), voiceInput()); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(rig.usage.calls) != 1 || rig.usage.calls[0] != (voiceUsageCall{"co-1", "th-1", "whisper-large-v3-turbo", 12.5, 0.0000075}) {
		t.Errorf("usage = %+v, want the provider's charge carried through", rig.usage.calls)
	}
}

func TestVoiceBillsTheDeclaredLengthWhenTheProviderReportsNone(t *testing.T) {
	rig := newVoiceRig("IDR")
	rig.tr.seconds = 0
	out, err := rig.svc.Transcribe(context.Background(), voiceInput())
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if out.Seconds != 11 || rig.usage.calls[0].seconds != 11 {
		t.Errorf("billed %v seconds, want the declared 11", rig.usage.calls[0].seconds)
	}
}

// Decision 15 at a smaller scale: a record that could not be kept is not a
// reason to withhold the transcript.
func TestVoiceStorageFailureStillReturnsTheTranscript(t *testing.T) {
	rig := newVoiceRig("IDR")
	rig.store.uploadErr = errors.New("bucket unreachable")
	out, err := rig.svc.Transcribe(context.Background(), voiceInput())
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if out.Text == "" || out.Clip == nil || out.Clip.ObjectKey != "" {
		t.Errorf("result = %+v; want the transcript, recorded with no audio", out)
	}
}

// "A provider failure … leaves no orphan clip" has a second half: an insert
// that fails after the upload took the audio back out.
func TestVoiceRowFailureTakesTheAudioBackOut(t *testing.T) {
	rig := newVoiceRig("IDR")
	rig.repo.createErr = errors.New("connection reset")
	out, err := rig.svc.Transcribe(context.Background(), voiceInput())
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if out.Clip != nil || out.Text == "" {
		t.Errorf("result = %+v; want the transcript and no clip", out)
	}
	if len(rig.store.uploads) != 1 || len(rig.store.removed) != 1 {
		t.Fatalf("uploads %d, removed %d; want the one upload removed", len(rig.store.uploads), len(rig.store.removed))
	}
	for key := range rig.store.uploads {
		if rig.store.removed[0] != key {
			t.Errorf("removed %q, uploaded %q", rig.store.removed[0], key)
		}
	}
}

// "No audio and no transcript appears in any log line at Info" — held at every
// level, across the success path and each failure path that logs.
func TestVoiceNothingSaidReachesTheLog(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()
	prev := logrus.GetLevel()
	logrus.SetLevel(logrus.DebugLevel)
	defer logrus.SetLevel(prev)

	said := "tiga ratus juta rupiah untuk gudang barat"
	for _, arrange := range []func(*voiceRig){
		func(*voiceRig) {},
		func(r *voiceRig) { r.tr.err = &speech.ProviderError{Status: 500, Message: "internal error"} },
		func(r *voiceRig) { r.store.uploadErr = errors.New("bucket unreachable") },
		func(r *voiceRig) { r.repo.createErr = errors.New("connection reset") },
		func(r *voiceRig) { r.tr.seconds = 900 }, // longer than declared: the warning path
	} {
		rig := newVoiceRig("IDR")
		rig.tr.text = said
		arrange(rig)
		_, _ = rig.svc.Transcribe(context.Background(), voiceInput())
	}

	entries := hook.AllEntries()
	if len(entries) < 5 {
		t.Fatalf("%d log entries; this test is not reading what it thinks it is", len(entries))
	}
	for _, e := range entries {
		line, _ := e.String()
		if strings.Contains(line, "tiga ratus") || strings.Contains(line, voiceSecretAudio) {
			t.Errorf("a %s line carries what was said: %s", e.Level, line)
		}
	}
}

func TestVoiceWithoutAProviderIsDisabled(t *testing.T) {
	for name, svc := range map[string]*VoiceService{
		"nil service":         nil,
		"nil transcriber":     NewVoiceService(nil, nil, nil, nil, 0, 0),
		"the nop transcriber": NewVoiceService(speech.New(speech.Config{}), nil, nil, nil, 0, 0),
	} {
		if svc.Enabled() {
			t.Errorf("%s: Enabled() = true", name)
		}
	}
	_, err := NewVoiceService(nil, nil, nil, nil, 0, 0).Transcribe(context.Background(), voiceInput())
	if !errors.Is(err, speech.ErrDisabled) {
		t.Errorf("error = %v, want ErrDisabled", err)
	}
}

func TestVoiceLimitsAreNormalised(t *testing.T) {
	cases := []struct {
		seconds, days int
		wantSeconds   int
		wantBytes     int64
		wantRetention time.Duration
	}{
		{0, 0, 60, 60*48_000 + 64<<10, 7 * 24 * time.Hour},
		{120, 30, 120, 120*48_000 + 64<<10, 30 * 24 * time.Hour},
		// Capped where the byte cap would cross a provider's 25 MB.
		{100_000, 100_000, 500, 500*48_000 + 64<<10, 90 * 24 * time.Hour},
	}
	for _, c := range cases {
		svc := NewVoiceService(&voiceFakeTranscriber{}, nil, nil, nil, c.seconds, c.days)
		if svc.MaxClipSeconds() != c.wantSeconds || svc.MaxClipBytes() != c.wantBytes || svc.retention != c.wantRetention {
			t.Errorf("(%d s, %d d) → %d s, %d bytes, %v", c.seconds, c.days, svc.MaxClipSeconds(), svc.MaxClipBytes(), svc.retention)
		}
		if svc.MaxClipBytes() > voiceProviderMaxBytes {
			t.Errorf("byte cap %d is above the provider's %d", svc.MaxClipBytes(), voiceProviderMaxBytes)
		}
	}
}

func TestVoiceSweepRemovesTheAudioBeforeTheRow(t *testing.T) {
	repo := &voiceFakeRepo{due: []*domain.VoiceClip{
		{ID: "a", CompanyID: "co-1", ObjectKey: "voice/co-1/a.webm"},
		{ID: "b", CompanyID: "co-2"}, // kept no audio
		{ID: "c", CompanyID: "co-1", ObjectKey: "voice/co-1/c.webm"},
	}}
	store := &voiceFakeStore{removeErr: map[string]error{"voice/co-1/c.webm": errors.New("timeout")}}
	res, err := NewVoiceClips(repo, store).Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Deleted != 2 || res.Kept != 1 {
		t.Errorf("result = %+v, want 2 deleted and 1 kept", res)
	}
	// c's audio would not delete, so its row stays for the next tick to find it.
	if strings.Join(repo.deleted, ",") != "a,b" || strings.Join(store.removed, ",") != "voice/co-1/a.webm" {
		t.Errorf("rows deleted %v, audio removed %v", repo.deleted, store.removed)
	}
}

func TestVoiceSweepWithoutStorageKeepsClipsWithAudio(t *testing.T) {
	repo := &voiceFakeRepo{due: []*domain.VoiceClip{
		{ID: "a", CompanyID: "co-1", ObjectKey: "voice/co-1/a.webm"},
		{ID: "b", CompanyID: "co-1"},
	}}
	res, err := NewVoiceClips(repo, nil).Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Deleted != 1 || res.Kept != 1 || strings.Join(repo.deleted, ",") != "b" {
		t.Errorf("result = %+v, deleted %v; a row whose audio this worker cannot reach must stay", res, repo.deleted)
	}
}

func TestVoiceEraseCompanyRemovesTheRowsAndThePrefix(t *testing.T) {
	repo := &voiceFakeRepo{eraseN: 3}
	store := &voiceFakeStore{}
	n, err := NewVoiceClips(repo, store).EraseCompany(context.Background(), "co-1")
	if err != nil || n != 3 {
		t.Fatalf("EraseCompany = %d, %v", n, err)
	}
	if strings.Join(repo.erasedFor, ",") != "co-1" || strings.Join(store.prefixes, ",") != "voice/co-1/" {
		t.Errorf("rows erased for %v, prefixes removed %v", repo.erasedFor, store.prefixes)
	}
	// An empty company would be the prefix "voice//", and the one before that
	// would be every company's.
	if _, err := NewVoiceClips(repo, store).EraseCompany(context.Background(), " "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("an empty company: error = %v, want ErrInvalidInput", err)
	}
	if len(store.prefixes) != 1 {
		t.Errorf("an empty company removed a prefix: %v", store.prefixes)
	}
}
