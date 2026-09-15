package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/speech"
)

// SpokenAnswerer is what the answer audio route needs of app.SpokenAnswerService.
type SpokenAnswerer interface {
	Speak(ctx context.Context, companyID string, msg *domain.Message) (*app.SpokenAudio, error)
}

// AnswerFinder is the tenant-scoped message read.
type AnswerFinder interface {
	GetForCompany(ctx context.Context, companyID, id string) (*domain.Message, error)
}

// SpokenAnswerHandler serves `GET /api/messages/:id/audio` (T-W8): an answer,
// read aloud.
//
// A GET, synthesised on the first request and served from the cache after, so
// "nobody pays to synthesise an answer nobody plays" is a property of the route's
// shape: nothing is synthesised until somebody asks to hear it.
//
// Registered only where the service is Enabled, for VoiceHandler's reason.
type SpokenAnswerHandler struct {
	svc           SpokenAnswerer
	messages      AnswerFinder
	conversations ConversationReader
}

// NewSpokenAnswerHandler wires the route.
func NewSpokenAnswerHandler(svc SpokenAnswerer, messages AnswerFinder) *SpokenAnswerHandler {
	return &SpokenAnswerHandler{svc: svc, messages: messages}
}

// WithConversationAccess hides an answer in a conversation the caller may not
// read (T-Z10): not found, before anything is synthesised or billed.
func (h *SpokenAnswerHandler) WithConversationAccess(r ConversationReader) *SpokenAnswerHandler {
	h.conversations = r
	return h
}

// Register installs the route. It requires the `voice` capability, which
// capabilityPolicy enforces ahead of this handler.
func (h *SpokenAnswerHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/messages/:id/audio", h.audio)
}

func (h *SpokenAnswerHandler) audio(c *gin.Context) {
	msg, err := h.messages.GetForCompany(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load that answer"})
		return
	}
	if !readableConversation(c, h.conversations, msg.ThreadID) {
		return
	}
	out, err := h.svc.Speak(c.Request.Context(), companyID(c), msg)
	if err != nil {
		spokenFail(c, err)
		return
	}
	defer func() { _ = out.Body.Close() }()
	// Private: the audio is a tenant's answer. An hour, because a person replaying
	// it in the same sitting should not cost a request, and the object behind it
	// lives for days.
	c.Header("Cache-Control", "private, max-age=3600")
	c.DataFromReader(http.StatusOK, out.Size, out.MediaType, out.Body, nil)
}

// spokenFail maps the service's errors. A refusal carries its reason, because
// the person asking is the one reading the answer the reason is about; a
// provider's own words never reach the response.
func spokenFail(c *gin.Context, err error) {
	var refused *app.SpokenRefusalError
	switch {
	case errors.As(err, &refused):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": app.ErrSpokenRefused.Error(), "reason": refused.Reason})
	case errors.Is(err, app.ErrNotAnAnswer):
		c.JSON(http.StatusNotFound, gin.H{"error": app.ErrNotAnAnswer.Error()})
	case errors.Is(err, domain.ErrInsufficientCredits):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": app.CreditsExhaustedMessage})
	case errors.Is(err, app.ErrSynthesisFailed):
		c.JSON(http.StatusBadGateway, gin.H{"error": app.ErrSynthesisFailed.Error()})
	case errors.Is(err, speech.ErrDisabled):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read that answer aloud"})
	}
}
