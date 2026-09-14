package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/speech"
)

// ErrTranscriptionFailed is a provider that did not transcribe — down, slow, or
// refusing the clip. The route answers with this sentence and never with the
// provider's own words, which are about its API and not about anything the
// person can do.
var ErrTranscriptionFailed = errors.New("the speech service could not transcribe that recording; try again, or type your question")

// The limits a clip is held to before a byte of it reaches a provider.
const (
	// DefaultVoiceMaxClipSeconds is a spoken question, with room to hesitate.
	DefaultVoiceMaxClipSeconds = 60
	// MaxVoiceClipSeconds keeps the byte cap below under the 25 MB both
	// providers refuse above, so a clip this deployment admits is one a provider
	// accepts.
	MaxVoiceClipSeconds = 500
	// DefaultVoiceRetentionDays is how long the audio is kept for somebody to
	// check what was heard (decision 16). A week covers "that is not what I
	// said" raised the next working day.
	DefaultVoiceRetentionDays = 7
	// MaxVoiceRetentionDays bounds it. Audio is the most identifying thing this
	// product stores, and a deployment that wants it for longer is asking a
	// question this setting should not answer quietly.
	MaxVoiceRetentionDays = 90

	// voiceBytesPerSecond is the byte cap's rate: 384 kbit/s, above anything a
	// browser's MediaRecorder writes for a voice (Opus and AAC default well
	// under it) and above 16 kHz mono WAV. A 44.1 kHz stereo WAV is not a
	// dictated question and is refused past a few seconds.
	voiceBytesPerSecond = 48_000
	// voiceContainerSlack is the container's own header, which a very short
	// clip would otherwise be refused over.
	voiceContainerSlack = 64 << 10
	// voiceProviderMaxBytes is the provider ceiling, decimal to stay under it.
	voiceProviderMaxBytes = 25_000_000
)

// VoiceClipStore is the slice of object storage a clip needs.
type VoiceClipStore interface {
	UploadKey(ctx context.Context, key string, r io.Reader, contentType string) (string, error)
	RemoveKey(ctx context.Context, key string) error
	RemovePrefix(ctx context.Context, prefix string) error
}

// TranscriptionRecorder meters a transcription. *UsageService is the
// production one.
type TranscriptionRecorder interface {
	RecordTranscription(ctx context.Context, companyID, threadID, model string, seconds float64)
}

// VoiceCompanyReader is the company row, for its currency.
type VoiceCompanyReader interface {
	GetByID(ctx context.Context, id string) (*domain.Company, error)
}

// VoiceBrandingReader is the tenant's stated document language.
// *branding.Service is the production one.
type VoiceBrandingReader interface {
	Get(ctx context.Context, companyID string) (*domain.ReportBranding, error)
}

// VoiceClips is a clip's life after it was transcribed: kept, swept, erased.
//
// Separate from VoiceService because the worker needs this half and not the
// other. The sweep runs whether or not this deployment can transcribe today —
// voice switched off after a week of use still owes that week's deletions — so
// the thing that deletes must not need the thing that transcribes.
type VoiceClips struct {
	repo  domain.VoiceClipRepository
	store VoiceClipStore
	now   func() time.Time
}

// NewVoiceClips wires the store. store may be nil — a deployment with no object
// storage — and then a clip is its transcript alone. Pass a nil *interface*,
// not a nil *storage.StorageService.
func NewVoiceClips(repo domain.VoiceClipRepository, store VoiceClipStore) *VoiceClips {
	return &VoiceClips{repo: repo, store: store, now: time.Now}
}

// WithClock overrides the clock.
func (v *VoiceClips) WithClock(now func() time.Time) *VoiceClips {
	if now != nil {
		v.now = now
	}
	return v
}

// keep stores the audio, then the row.
//
// **In that order, and a failure of either leaves no orphan the product cannot
// find.** Audio first because its key is on the row. An upload that fails
// leaves a row with no key: the transcript is still the record, and a person
// checking what was heard learns only that the recording was not kept. A row
// that fails takes its audio back out. If that removal fails too, the object
// sits under the company's prefix, and erasure removes the prefix rather than
// walking rows — so erasure still finds it, and only the expiry sweep does not.
func (v *VoiceClips) keep(ctx context.Context, clip *domain.VoiceClip, audio []byte, ext string) error {
	if v == nil || v.repo == nil {
		return errors.New("voice clips are not configured")
	}
	if v.store != nil {
		key := domain.VoiceClipKeyPrefix(clip.CompanyID) + clip.ID + "." + ext
		if _, err := v.store.UploadKey(ctx, key, bytes.NewReader(audio), clip.MimeType); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": clip.CompanyID,
				"clip_id":    clip.ID,
			}).Warn("voice: clip audio not stored; keeping the transcript without it")
		} else {
			clip.ObjectKey = key
		}
	}
	if err := v.repo.Create(ctx, clip); err != nil {
		if clip.ObjectKey != "" {
			if rerr := v.store.RemoveKey(ctx, clip.ObjectKey); rerr != nil {
				logrus.WithError(rerr).WithFields(logrus.Fields{
					"company_id": clip.CompanyID,
					"clip_id":    clip.ID,
				}).Error("voice: clip row not written and its audio could not be removed; only erasure will find it")
			}
			clip.ObjectKey = ""
		}
		return fmt.Errorf("record voice clip: %w", err)
	}
	return nil
}

// VoiceSweepResult is what one sweep did.
type VoiceSweepResult struct {
	Deleted int
	// Kept counts clips that were due and are still here — their audio could not
	// be removed — which the next tick tries again.
	Kept int
}

const (
	voiceSweepBatch      = 200
	voiceSweepMaxBatches = 25
)

// Sweep deletes every clip past its expiry, and every clip whose conversation
// is gone.
//
// Audio before row, for keep's reason in reverse: a row whose audio could not be
// removed is kept so the next tick can find the audio again, where deleting the
// row first would make the object unfindable by anything but erasure.
//
// Bounded at 5,000 clips a tick, so a backlog — a sweep that was switched off
// for a month — drains over several ticks rather than holding one connection
// for as long as it takes.
func (v *VoiceClips) Sweep(ctx context.Context) (VoiceSweepResult, error) {
	var out VoiceSweepResult
	if v == nil || v.repo == nil {
		return out, nil
	}
	for i := 0; i < voiceSweepMaxBatches; i++ {
		due, err := v.repo.Due(ctx, v.now().UTC(), voiceSweepBatch)
		if err != nil {
			return out, fmt.Errorf("list due voice clips: %w", err)
		}
		deleted, kept := 0, 0
		for _, c := range due {
			if c.ObjectKey != "" {
				if v.store == nil {
					kept++
					continue
				}
				if err := v.store.RemoveKey(ctx, c.ObjectKey); err != nil {
					logrus.WithError(err).WithFields(logrus.Fields{
						"company_id": c.CompanyID, "clip_id": c.ID,
					}).Warn("voice sweep: audio not removed; the clip is kept for the next tick")
					kept++
					continue
				}
			}
			if err := v.repo.Delete(ctx, c.CompanyID, c.ID); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"company_id": c.CompanyID, "clip_id": c.ID,
				}).Warn("voice sweep: row not deleted; the next tick tries again")
				kept++
				continue
			}
			deleted++
		}
		out.Deleted += deleted
		out.Kept += kept
		// A short batch is the end of the list. A batch that deleted nothing
		// would be read again unchanged, so it is the end of this tick.
		if len(due) < voiceSweepBatch || deleted == 0 {
			break
		}
	}
	if out.Kept > 0 && v.store == nil {
		// Said at Warn every tick it is true, because it does not fix itself:
		// the API kept audio in a bucket this worker was not given.
		logrus.WithField("kept", out.Kept).
			Warn("voice sweep: clips with stored audio are due and this worker has no object storage; they are kept")
	}
	return out, nil
}

// EraseCompany removes every clip a company has, row and audio (T-H6).
//
// The audio goes by prefix, not by the rows just deleted, so an object whose
// row was never written goes too. The rows go first: if the prefix removal then
// fails, the erasure is recorded as failed, and running it again deletes no
// rows and retries the prefix — which a row-driven removal could not, having
// just deleted its list.
func (v *VoiceClips) EraseCompany(ctx context.Context, companyID string) (int, error) {
	if strings.TrimSpace(companyID) == "" {
		return 0, fmt.Errorf("%w: company is required", domain.ErrInvalidInput)
	}
	if v == nil || v.repo == nil {
		return 0, nil
	}
	n, err := v.repo.DeleteForCompany(ctx, companyID)
	if err != nil {
		return 0, fmt.Errorf("delete voice clips: %w", err)
	}
	if v.store != nil {
		if err := v.store.RemovePrefix(ctx, domain.VoiceClipKeyPrefix(companyID)); err != nil {
			return n, fmt.Errorf("remove voice clip audio: %w", err)
		}
	}
	return n, nil
}

// VoiceService turns a recording into a transcript and nothing else (T-W7).
//
// **It holds no message repository and no enqueuer.** "No message is created"
// is therefore not a promise this type keeps by being careful; it is one it
// could not break without a new dependency (roadmap 11, decision 13).
type VoiceService struct {
	transcriber speech.Transcriber
	clips       *VoiceClips
	usage       TranscriptionRecorder
	companies   VoiceCompanyReader
	branding    VoiceBrandingReader
	budget      BudgetChecker
	maxSeconds  int
	retention   time.Duration
	now         func() time.Time
}

// NewVoiceService wires the service. Everything but the transcriber may be nil;
// the limits are normalised rather than refused.
func NewVoiceService(t speech.Transcriber, clips *VoiceClips, usage TranscriptionRecorder, companies VoiceCompanyReader, maxSeconds, retentionDays int) *VoiceService {
	if maxSeconds <= 0 {
		maxSeconds = DefaultVoiceMaxClipSeconds
	}
	if maxSeconds > MaxVoiceClipSeconds {
		maxSeconds = MaxVoiceClipSeconds
	}
	if retentionDays <= 0 {
		retentionDays = DefaultVoiceRetentionDays
	}
	if retentionDays > MaxVoiceRetentionDays {
		retentionDays = MaxVoiceRetentionDays
	}
	return &VoiceService{
		transcriber: t,
		clips:       clips,
		usage:       usage,
		companies:   companies,
		maxSeconds:  maxSeconds,
		retention:   time.Duration(retentionDays) * 24 * time.Hour,
		now:         time.Now,
	}
}

// WithBudget refuses a transcription a tenant cannot pay for, before the
// provider is called. The same checker ChatEnqueuer uses, so a tenant refused a
// turn is refused the transcript that would have started it — rather than
// charged for a question they then cannot ask.
func (s *VoiceService) WithBudget(b BudgetChecker) *VoiceService {
	s.budget = b
	return s
}

// WithBranding lets the tenant's stated document language be the hint.
func (s *VoiceService) WithBranding(b VoiceBrandingReader) *VoiceService {
	s.branding = b
	return s
}

// WithClock overrides the clock.
func (s *VoiceService) WithClock(now func() time.Time) *VoiceService {
	if now != nil {
		s.now = now
	}
	return s
}

// Enabled reports whether a clip sent here would reach a provider.
func (s *VoiceService) Enabled() bool {
	return s != nil && s.transcriber != nil && s.transcriber.Enabled()
}

// MaxClipSeconds is the longest recording the route admits.
func (s *VoiceService) MaxClipSeconds() int { return s.maxSeconds }

// MaxClipBytes is the largest body the route reads for one.
//
// **This is the cap that actually bounds a bill**, and the seconds limit is not.
// A route cannot measure a recording's length without decoding it, and the
// WebM a browser's MediaRecorder writes carries no duration header to read. So
// the length a client declares is checked, refused past the limit, and trusted
// no further; the bytes are what a lying client cannot get past.
func (s *VoiceService) MaxClipBytes() int64 {
	n := int64(s.maxSeconds)*voiceBytesPerSecond + voiceContainerSlack
	if n > voiceProviderMaxBytes {
		n = voiceProviderMaxBytes
	}
	return n
}

// VoiceInput is one recording, as the route accepted it.
type VoiceInput struct {
	CompanyID string
	UserID    string
	ThreadID  string
	Audio     []byte
	// MediaType and Ext are speech.Accept's answer: the route has already
	// checked that the bytes are the audio they claim to be.
	MediaType string
	Ext       string
	// DeclaredSeconds is the client's claim, used only when the provider
	// reports no length.
	DeclaredSeconds float64
	// Language overrides the tenant's, as speech.NormalizeLanguage returned it.
	Language string
}

// VoiceTranscription is what came back.
type VoiceTranscription struct {
	Text string
	// Language is the hint sent, or empty when the provider was left to detect.
	Language string
	// Seconds is the length billed.
	Seconds float64
	// Clip is what was recorded, or nil when nothing could be. The transcript is
	// good either way.
	Clip *domain.VoiceClip
}

// Transcribe sends a recording to the provider and returns what it heard.
//
// **Nothing said is logged, at any level.** Not the transcript, not the audio,
// not the provider's answer body. The log carries shapes — bytes, seconds, the
// type, the language — which is T-H7's rule for query text applied to speech.
func (s *VoiceService) Transcribe(ctx context.Context, in VoiceInput) (*VoiceTranscription, error) {
	if !s.Enabled() {
		return nil, speech.ErrDisabled
	}
	if in.CompanyID == "" || in.ThreadID == "" {
		return nil, fmt.Errorf("%w: a recording belongs to a conversation", domain.ErrInvalidInput)
	}
	if len(in.Audio) == 0 {
		return nil, fmt.Errorf("%w: the recording is empty", domain.ErrInvalidInput)
	}
	if int64(len(in.Audio)) > s.MaxClipBytes() {
		return nil, fmt.Errorf("%w: a recording must be %d seconds or shorter", domain.ErrInvalidInput, s.maxSeconds)
	}
	if s.budget != nil {
		st, err := s.budget.CheckBudget(ctx, in.CompanyID)
		if err != nil {
			return nil, fmt.Errorf("check budget: %w", err)
		}
		if st.Blocked() {
			return nil, fmt.Errorf("%w: %s", domain.ErrInsufficientCredits, CreditsExhaustedMessage)
		}
	}

	lang := s.language(ctx, in.CompanyID, in.Language)
	model := s.transcriber.Model()
	fields := logrus.Fields{
		"company_id": in.CompanyID,
		"thread_id":  in.ThreadID,
		"user_id":    in.UserID,
		"bytes":      len(in.Audio),
		"mime_type":  in.MediaType,
		"language":   lang,
		"model":      model,
	}

	started := s.now()
	heard, err := s.transcriber.Transcribe(ctx, bytes.NewReader(in.Audio), in.MediaType, lang)
	fields["took_ms"] = s.now().Sub(started).Milliseconds()
	if err != nil {
		if errors.Is(err, speech.ErrDisabled) {
			return nil, err
		}
		// The provider's error names its status and its own sentence, never the
		// request, so it is safe to log — and it is the only way an operator
		// learns which of "down", "slow" and "refused the clip" this was.
		logrus.WithError(err).WithFields(fields).Warn("voice: transcription failed; nothing kept and nothing billed")
		return nil, fmt.Errorf("%w (%v)", ErrTranscriptionFailed, err)
	}

	seconds, measured := heard.Seconds, true
	if seconds <= 0 {
		seconds, measured = in.DeclaredSeconds, false
	}
	fields["seconds"] = seconds
	fields["measured"] = measured
	if s.usage != nil {
		s.usage.RecordTranscription(ctx, in.CompanyID, in.ThreadID, model, seconds)
	}
	if measured && seconds > float64(s.maxSeconds)+1 {
		// Already paid for, so refusing now would save nothing. What it is worth
		// is the number: a client whose declared length is routinely short of
		// the measured one is a client to look at.
		fields["declared_seconds"] = in.DeclaredSeconds
		logrus.WithFields(fields).Warn("voice: a clip was longer than its client declared; billed on the measured length")
	}

	now := s.now().UTC()
	out := &VoiceTranscription{Text: heard.Text, Language: lang, Seconds: seconds}
	clip := &domain.VoiceClip{
		ID:         uuid.NewString(),
		CompanyID:  in.CompanyID,
		ThreadID:   in.ThreadID,
		UserID:     in.UserID,
		MimeType:   in.MediaType,
		SizeBytes:  int64(len(in.Audio)),
		Seconds:    seconds,
		Transcript: heard.Text,
		Language:   lang,
		Model:      model,
		CreatedAt:  now,
		ExpiresAt:  now.Add(s.retention),
	}
	if s.clips != nil {
		if err := s.clips.keep(ctx, clip, in.Audio, in.Ext); err != nil {
			// Decision 15's rule at a smaller scale: a record that could not be
			// written is not a reason to withhold what the person said.
			logrus.WithError(err).WithFields(fields).Warn("voice: clip not recorded; returning the transcript without it")
		} else {
			out.Clip = clip
			fields["clip_id"] = clip.ID
			fields["kept_audio"] = clip.ObjectKey != ""
		}
	}
	logrus.WithFields(fields).Info("voice: transcribed")
	return out, nil
}

// language is the hint a provider is sent.
//
// The person's own choice first; then the tenant's stated document language;
// then Indonesian for a rupiah tenant, format.LocaleForCurrency's rule. **Then
// nothing** — not English. There is no language field on a company, and a
// tenant that has stated neither a locale nor rupiah has not said it speaks
// English either: an empty hint lets the provider detect what was spoken, where
// "en" would make it transcribe Thai as English.
func (s *VoiceService) language(ctx context.Context, companyID, override string) string {
	if override != "" {
		return override
	}
	if s.branding != nil {
		if b, err := s.branding.Get(ctx, companyID); err == nil && b != nil {
			if l, ok := speech.NormalizeLanguage(b.Locale); ok {
				return l
			}
		}
	}
	if s.companies != nil {
		if c, err := s.companies.GetByID(ctx, companyID); err == nil && c != nil &&
			strings.EqualFold(strings.TrimSpace(c.DefaultCurrency), "IDR") {
			return "id"
		}
	}
	return ""
}
