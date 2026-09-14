package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/speech"
)

// VoiceTranscriber is what the voice route needs of app.VoiceService.
type VoiceTranscriber interface {
	Transcribe(ctx context.Context, in app.VoiceInput) (*app.VoiceTranscription, error)
	MaxClipSeconds() int
	MaxClipBytes() int64
}

// ThreadFinder is the tenant-scoped conversation read.
type ThreadFinder interface {
	GetForCompany(ctx context.Context, companyID, id string) (*domain.ConversationThread, error)
}

// VoiceHandler serves `POST /api/threads/:id/voice` (T-W7): a recording in, a
// transcript out.
//
// **It does not start a turn and does not write a message** (roadmap 11,
// decision 13). The transcript goes back to the composer, and the person sends
// it through `POST /api/chat` like anything they typed — or edits it first,
// which is the reason it is not sent for them. The conversation in the path is
// what the clip is filed under and billed to, not where anything is posted.
//
// The route is registered only on a deployment that can transcribe. Elsewhere
// it does not exist, and a request to it is the router's own 404 — before
// authentication, before the capability check, and without a sentence
// suggesting a grant would help.
type VoiceHandler struct {
	svc           VoiceTranscriber
	threads       ThreadFinder
	conversations ConversationReader
}

// NewVoiceHandler wires the route.
func NewVoiceHandler(svc VoiceTranscriber, threads ThreadFinder) *VoiceHandler {
	return &VoiceHandler{svc: svc, threads: threads}
}

// WithConversationAccess hides a conversation the caller may not read (T-Z10):
// it is not found here either, before a byte of the recording is read.
func (h *VoiceHandler) WithConversationAccess(r ConversationReader) *VoiceHandler {
	h.conversations = r
	return h
}

// Register installs the route. It requires the `voice` capability, which
// capabilityPolicy in cmd/api/policy.go enforces ahead of this handler.
func (h *VoiceHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/threads/:id/voice", h.transcribe)
}

// voiceFormSlack is the multipart framing and the two small fields beside the
// recording, which the body cap must leave room for.
const voiceFormSlack = 16 << 10

func (h *VoiceHandler) transcribe(c *gin.Context) {
	// The conversation first, and its visibility, before the body: a person who
	// may not read this conversation is told it does not exist without their
	// upload being read at all.
	thread, err := h.threads.GetForCompany(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load that conversation"})
		return
	}
	if !readableConversation(c, h.conversations, thread.ID) {
		return
	}

	maxBytes := h.svc.MaxClipBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+voiceFormSlack)
	file, err := c.FormFile("audio")
	if err != nil {
		// isBodyTooLarge's reason: an oversized body arrives here looking like a
		// malformed form, and it is not one.
		if isBodyTooLarge(err) {
			h.tooLong(c)
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": `expected a recording in the "audio" field`})
		return
	}
	if file.Size > maxBytes {
		h.tooLong(c)
		return
	}

	ms, err := strconv.ParseInt(strings.TrimSpace(c.PostForm("duration_ms")), 10, 64)
	if err != nil || ms <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duration_ms is required: the recording's length in milliseconds"})
		return
	}
	if ms > int64(h.svc.MaxClipSeconds())*1000 {
		h.tooLong(c)
		return
	}

	lang := ""
	if raw := c.PostForm("language"); strings.TrimSpace(raw) != "" {
		l, ok := speech.NormalizeLanguage(raw)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": `language must be a two-letter code such as "id" or "en"`})
			return
		}
		lang = l
	}

	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the recording"})
		return
	}
	defer func() { _ = f.Close() }()
	audio, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the recording"})
		return
	}
	mediaType, ext, ok := speech.Accept(file.Header.Get("Content-Type"), audio)
	if !ok {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "expected audio: WebM, Ogg, MP4, MP3, WAV or FLAC"})
		return
	}

	out, err := h.svc.Transcribe(c.Request.Context(), app.VoiceInput{
		CompanyID:       companyID(c),
		UserID:          userID(c),
		ThreadID:        thread.ID,
		Audio:           audio,
		MediaType:       mediaType,
		Ext:             ext,
		DeclaredSeconds: float64(ms) / 1000,
		Language:        lang,
	})
	if err != nil {
		voiceFail(c, err)
		return
	}
	resp := VoiceTranscriptionResponse{Transcript: out.Text, Language: out.Language, Seconds: out.Seconds}
	if out.Clip != nil {
		resp.ClipID = out.Clip.ID
		expires := out.Clip.ExpiresAt
		resp.ExpiresAt = &expires
	}
	c.JSON(http.StatusOK, resp)
}

// tooLong is the one sentence the byte cap and the declared length both answer
// with. The person's remedy is the same either way — a shorter recording — and
// "must be 2 MB or smaller" is a limit nobody holding a microphone can act on.
func (h *VoiceHandler) tooLong(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, gin.H{
		"error": fmt.Sprintf("a recording must be %d seconds or shorter", h.svc.MaxClipSeconds()),
	})
}

// voiceFail maps the service's errors. Nothing a provider said reaches the
// response: ErrTranscriptionFailed's wrapped detail is for the log.
func voiceFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInsufficientCredits):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": app.CreditsExhaustedMessage})
	case errors.Is(err, app.ErrTranscriptionFailed):
		c.JSON(http.StatusBadGateway, gin.H{"error": app.ErrTranscriptionFailed.Error()})
	case errors.Is(err, speech.ErrDisabled):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not transcribe that recording"})
	}
}
