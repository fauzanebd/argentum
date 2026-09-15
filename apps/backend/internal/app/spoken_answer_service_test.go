package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/speech"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

var spokenAudioBytes = append([]byte("ID3"), []byte("SPOKENFRAMES")...)

type spokenFakeSynth struct {
	mu    sync.Mutex
	calls int
	got   []string
	err   error
	// entered is sent on when Speak starts; gate, when set, holds it there.
	entered chan struct{}
	gate    chan struct{}
}

func (f *spokenFakeSynth) Speak(_ context.Context, text, _ string) (speech.Audio, error) {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.got = append(f.got, text)
	if f.err != nil {
		return speech.Audio{}, f.err
	}
	return speech.Audio{Data: spokenAudioBytes, MediaType: "audio/mpeg", Ext: "mp3"}, nil
}
func (f *spokenFakeSynth) Enabled() bool { return true }
func (f *spokenFakeSynth) Model() string { return "tts-1" }
func (f *spokenFakeSynth) Voice() string { return "alloy" }
func (f *spokenFakeSynth) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type spokenFakeLLM struct {
	mu              sync.Mutex
	reply           string
	err             error
	calls           int
	company, thread string
	prompt          string
}

func (f *spokenFakeLLM) Generate(ctx context.Context, prompt string, _ ...interfaces.GenerateOption) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.company, f.thread, f.prompt = tenantctx.CompanyID(ctx), tenantctx.ThreadID(ctx), prompt
	return f.reply, f.err
}

type spokenFakeRepo struct {
	mu        sync.Mutex
	rows      map[string]*domain.SpokenAnswer
	saveErr   error
	due       []*domain.SpokenAnswer
	deleted   []string
	erasedFor []string
}

func (r *spokenFakeRepo) ForMessage(_ context.Context, companyID, messageID string) (*domain.SpokenAnswer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.rows[messageID]
	if !ok || a.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (r *spokenFakeRepo) Save(_ context.Context, a *domain.SpokenAnswer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	if r.rows == nil {
		r.rows = map[string]*domain.SpokenAnswer{}
	}
	cp := *a
	r.rows[a.MessageID] = &cp
	return nil
}

func (r *spokenFakeRepo) Due(_ context.Context, _ time.Time, limit int) ([]*domain.SpokenAnswer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.SpokenAnswer
	for _, a := range r.due {
		if !slices.Contains(r.deleted, a.ID) && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *spokenFakeRepo) Delete(_ context.Context, _, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *spokenFakeRepo) DeleteForCompany(_ context.Context, companyID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.erasedFor = append(r.erasedFor, companyID)
	return 0, nil
}

type spokenFakeStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	uploads   int
	uploadErr error
	removed   []string
}

func (s *spokenFakeStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uploadErr != nil {
		return "", s.uploadErr
	}
	if s.objects == nil {
		s.objects = map[string][]byte{}
	}
	s.objects[key], _ = io.ReadAll(r)
	s.uploads++
	return key, nil
}

func (s *spokenFakeStore) StreamKey(_ context.Context, key string) (io.ReadCloser, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, 0, errors.New("no such key")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (s *spokenFakeStore) RemoveKey(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, key)
	delete(s.objects, key)
	return nil
}

type spokenUsageCall struct {
	company, thread, model string
	chars                  int
}

type spokenFakeUsage struct {
	mu    sync.Mutex
	calls []spokenUsageCall
}

func (u *spokenFakeUsage) RecordSynthesis(_ context.Context, companyID, threadID, model string, chars int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, spokenUsageCall{companyID, threadID, model, chars})
}

func (u *spokenFakeUsage) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

// The written answer: a figure in prose and a table.
const spokenWritten = "Penjualan Maret mencapai **Rp 1.234.567**.\n\n| Gudang | Stok |\n|---|---|\n| Barat | 1.480 |\n| Timur | 920 |"

// A reduction that rounds both figures and reads no table.
const spokenGood = "Penjualan Maret sekitar 1,2 juta rupiah. Gudang barat menyimpan sekitar 1.500 unit."

func spokenMessage() *domain.Message {
	return &domain.Message{ID: "msg-1", ThreadID: "th-1", Role: domain.MessageRoleAssistant, Content: spokenWritten}
}

type spokenRig struct {
	synth *spokenFakeSynth
	llm   *spokenFakeLLM
	repo  *spokenFakeRepo
	store *spokenFakeStore
	usage *spokenFakeUsage
	svc   *SpokenAnswerService
}

func newSpokenRig() *spokenRig {
	r := &spokenRig{
		synth: &spokenFakeSynth{},
		llm:   &spokenFakeLLM{reply: spokenGood},
		repo:  &spokenFakeRepo{},
		store: &spokenFakeStore{},
		usage: &spokenFakeUsage{},
	}
	r.svc = NewSpokenAnswerService(r.synth, r.llm, r.repo, r.store, r.usage, 7).WithClock(fixedClock(voiceNow))
	return r
}

func spokenBody(t *testing.T, a *SpokenAudio) []byte {
	t.Helper()
	defer func() { _ = a.Body.Close() }()
	b, err := io.ReadAll(a.Body)
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	return b
}

// "The same message synthesised twice hits the cache and bills once."
func TestSpokenAnswerSynthesisesOnceAndServesTheCacheAfter(t *testing.T) {
	rig := newSpokenRig()
	first, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage())
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if first.Cached || first.MediaType != "audio/mpeg" || !bytes.Equal(spokenBody(t, first), spokenAudioBytes) {
		t.Errorf("first press = %+v", first)
	}
	row := rig.repo.rows["msg-1"]
	if row == nil || row.ObjectKey != "voice/co-1/answers/msg-1.mp3" || row.SpokenText != spokenGood || row.Refusal != "" {
		t.Fatalf("row = %+v", row)
	}
	if !row.ExpiresAt.Equal(voiceNow.Add(7*24*time.Hour)) || row.Voice != "alloy" || row.Model != "tts-1" {
		t.Errorf("row = %+v", row)
	}
	want := spokenUsageCall{"co-1", "th-1", "tts-1", len([]rune(spokenGood))}
	if len(rig.usage.calls) != 1 || rig.usage.calls[0] != want {
		t.Errorf("usage = %+v, want %+v", rig.usage.calls, want)
	}

	second, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage())
	if err != nil {
		t.Fatalf("second Speak: %v", err)
	}
	if !second.Cached || !bytes.Equal(spokenBody(t, second), spokenAudioBytes) {
		t.Errorf("second press = %+v", second)
	}
	if rig.synth.count() != 1 || rig.llm.calls != 1 || rig.usage.count() != 1 {
		t.Errorf("two presses synthesised %d times, reduced %d times, billed %d times; want once each",
			rig.synth.count(), rig.llm.calls, rig.usage.count())
	}
}

// "A table in the written answer produces no pipes, no dashes and no column
// headers in the spoken text" — whatever the model wrote.
func TestSpokenAnswerATableIsNeverReadAloud(t *testing.T) {
	t.Run("a reduction that recites the table is refused", func(t *testing.T) {
		rig := newSpokenRig()
		rig.llm.reply = spokenWritten
		_, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage())
		if !errors.Is(err, ErrSpokenRefused) {
			t.Fatalf("error = %v, want ErrSpokenRefused", err)
		}
		if rig.synth.count() != 0 || rig.usage.count() != 0 {
			t.Errorf("a refused reduction reached the synthesiser %d times and billed %d", rig.synth.count(), rig.usage.count())
		}
		if row := rig.repo.rows["msg-1"]; row == nil || !strings.Contains(row.Refusal, "table") {
			t.Errorf("refusal row = %+v", row)
		}
	})
	t.Run("a reduction in markdown reaches the synthesiser as sentences", func(t *testing.T) {
		rig := newSpokenRig()
		rig.llm.reply = "- **Penjualan** Maret sekitar 1,2 juta.\n- Gudang barat sekitar 1.500 unit."
		if _, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage()); err != nil {
			t.Fatalf("Speak: %v", err)
		}
		got := rig.synth.got[0]
		if strings.ContainsAny(got, "|*#") || strings.HasPrefix(got, "-") || strings.Contains(got, "- ") || strings.Contains(got, "Stok") {
			t.Errorf("the synthesiser was sent %q", got)
		}
	})
}

// "A spoken text stating a figure absent from the written answer is refused" —
// and remembered, so the second press does not pay a model to be refused again.
func TestSpokenAnswerAFigureTheAnswerDoesNotStateIsRefusedAndRemembered(t *testing.T) {
	rig := newSpokenRig()
	rig.llm.reply = "Penjualan Maret sekitar 2 juta rupiah."
	_, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage())
	var refused *SpokenRefusalError
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want a SpokenRefusalError", err)
	}
	if !strings.Contains(refused.Reason, "2 juta") || !strings.Contains(refused.Reason, "1.234.567") {
		t.Errorf("the refusal does not name both figures: %s", refused.Reason)
	}
	if rig.synth.count() != 0 || rig.usage.count() != 0 {
		t.Errorf("synthesised %d, billed %d; want neither", rig.synth.count(), rig.usage.count())
	}
	if _, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage()); !errors.Is(err, ErrSpokenRefused) {
		t.Errorf("second press: error = %v, want the remembered refusal", err)
	}
	if rig.llm.calls != 1 {
		t.Errorf("the model was asked %d times; a refusal must be remembered", rig.llm.calls)
	}
}

// "A synthesiser failure yields the text answer and a disabled play button,
// never a failed turn." Nothing is kept or billed, and the next press tries
// again: a provider being down is not a property of the answer.
func TestSpokenAnswerSynthesiserFailureKeepsNothingAndBillsNothing(t *testing.T) {
	rig := newSpokenRig()
	rig.synth.err = &speech.ProviderError{Status: 503, Message: "over capacity"}
	_, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage())
	if !errors.Is(err, ErrSynthesisFailed) {
		t.Fatalf("error = %v, want ErrSynthesisFailed", err)
	}
	if rig.usage.count() != 0 || rig.store.uploads != 0 || len(rig.repo.rows) != 0 {
		t.Errorf("after a failure: %d usage, %d uploads, %d rows — want none", rig.usage.count(), rig.store.uploads, len(rig.repo.rows))
	}
	_, _ = rig.svc.Speak(context.Background(), "co-1", spokenMessage())
	if rig.llm.calls != 2 {
		t.Errorf("the second press reduced %d times in all, want 2: a failure is not a refusal", rig.llm.calls)
	}
}

func TestSpokenAnswerOnlyAnAnswerIsRead(t *testing.T) {
	for name, msg := range map[string]*domain.Message{
		"a person's question": {ID: "m", ThreadID: "th-1", Role: domain.MessageRoleUser, Content: "berapa stok?"},
		"an empty answer":     {ID: "m", ThreadID: "th-1", Role: domain.MessageRoleAssistant, Content: "  "},
		"a room's line": {ID: "m", ThreadID: "th-1", Role: domain.MessageRoleAssistant, Content: "→ Finance: ok",
			Metadata: map[string]interface{}{RoomEventKey: "nudge"}},
	} {
		rig := newSpokenRig()
		if _, err := rig.svc.Speak(context.Background(), "co-1", msg); !errors.Is(err, ErrNotAnAnswer) {
			t.Errorf("%s: error = %v, want ErrNotAnAnswer", name, err)
		}
		if rig.llm.calls != 0 {
			t.Errorf("%s: the model was asked", name)
		}
	}
}

func TestSpokenAnswerOutOfCreditsSpendsNothing(t *testing.T) {
	rig := newSpokenRig()
	rig.svc.WithBudget(voiceFakeBudget{st: BudgetState{Verdict: BudgetExhausted}})
	if _, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage()); !errors.Is(err, domain.ErrInsufficientCredits) {
		t.Fatalf("error = %v, want ErrInsufficientCredits", err)
	}
	if rig.llm.calls != 0 || rig.synth.count() != 0 {
		t.Errorf("reduced %d, synthesised %d; want neither", rig.llm.calls, rig.synth.count())
	}
}

// Two presses of one button, the second while the first is still synthesising:
// one synthesis, one bill. Whether the second joins the first or reads the row
// the first saved, the counts are the same.
func TestSpokenAnswerConcurrentPressesSynthesiseOnce(t *testing.T) {
	rig := newSpokenRig()
	rig.synth.entered = make(chan struct{}, 2)
	rig.synth.gate = make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	cached := make([]bool, 2)
	press := func(i int) {
		defer wg.Done()
		a, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage())
		errs[i] = err
		if err == nil {
			cached[i] = a.Cached
			_ = a.Body.Close()
		}
	}
	wg.Add(1)
	go press(0)
	<-rig.synth.entered
	wg.Add(1)
	go press(1)
	time.Sleep(20 * time.Millisecond)
	close(rig.synth.gate)
	wg.Wait()

	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errors = %v", errs)
	}
	if rig.synth.count() != 1 || rig.usage.count() != 1 {
		t.Errorf("two concurrent presses synthesised %d times and billed %d; want once", rig.synth.count(), rig.usage.count())
	}
	if cached[0] || !cached[1] {
		t.Errorf("cached = %v, want the first to have paid and the second not", cached)
	}
}

// The reduction is the tenant's cost, so the metered light model must find the
// tenant in the context — and the answer goes to it fenced.
func TestSpokenAnswerReductionIsBilledToTheTenantAndFenced(t *testing.T) {
	rig := newSpokenRig()
	if _, err := rig.svc.Speak(context.Background(), "co-1", spokenMessage()); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if rig.llm.company != "co-1" || rig.llm.thread != "th-1" {
		t.Errorf("the reduction ran as company %q, thread %q", rig.llm.company, rig.llm.thread)
	}
	if !strings.Contains(rig.llm.prompt, guardrails.FenceOpen) || !strings.Contains(rig.llm.prompt, "1.234.567") {
		t.Errorf("the answer is not fenced in the prompt:\n%s", rig.llm.prompt)
	}
}

func TestSpokenAnswerNeedsEveryPartToBeEnabled(t *testing.T) {
	for name, svc := range map[string]*SpokenAnswerService{
		"nil service":         nil,
		"the nop synthesiser": NewSpokenAnswerService(speech.NewSynthesizer(speech.SynthConfig{}), &spokenFakeLLM{}, &spokenFakeRepo{}, &spokenFakeStore{}, nil, 0),
		"no object storage":   NewSpokenAnswerService(&spokenFakeSynth{}, &spokenFakeLLM{}, &spokenFakeRepo{}, nil, nil, 0),
		"no light model":      NewSpokenAnswerService(&spokenFakeSynth{}, nil, &spokenFakeRepo{}, &spokenFakeStore{}, nil, 0),
	} {
		if svc.Enabled() {
			t.Errorf("%s: Enabled() = true", name)
		}
		if _, err := svc.Speak(context.Background(), "co-1", spokenMessage()); !errors.Is(err, speech.ErrDisabled) {
			t.Errorf("%s: error = %v, want ErrDisabled", name, err)
		}
	}
}

// The recordings' sweep and erasure take the spoken answers with them.
func TestVoiceSweepAndErasureTakeSpokenAnswersToo(t *testing.T) {
	clips := &voiceFakeRepo{}
	answers := &spokenFakeRepo{due: []*domain.SpokenAnswer{
		{ID: "s1", CompanyID: "co-1", ObjectKey: "voice/co-1/answers/m1.mp3"},
		{ID: "s2", CompanyID: "co-1"}, // a refusal: no audio
	}}
	store := &voiceFakeStore{}
	v := NewVoiceClips(clips, store).WithSpokenAnswers(answers)
	res, err := v.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Deleted != 2 || res.Kept != 0 {
		t.Errorf("result = %+v, want 2 deleted", res)
	}
	if strings.Join(answers.deleted, ",") != "s1,s2" || strings.Join(store.removed, ",") != "voice/co-1/answers/m1.mp3" {
		t.Errorf("rows deleted %v, audio removed %v", answers.deleted, store.removed)
	}
	if _, err := v.EraseCompany(context.Background(), "co-1"); err != nil {
		t.Fatalf("EraseCompany: %v", err)
	}
	if strings.Join(answers.erasedFor, ",") != "co-1" || strings.Join(clips.erasedFor, ",") != "co-1" ||
		strings.Join(store.prefixes, ",") != "voice/co-1/" {
		t.Errorf("erased answers for %v, clips for %v, prefixes %v", answers.erasedFor, clips.erasedFor, store.prefixes)
	}
}
