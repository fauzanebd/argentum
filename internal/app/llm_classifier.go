package app

import (
	"context"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
)

// TopicClassifier decides whether a new user message continues an existing
// conversation thread or starts a new one. It is intentionally tiny: one LLM
// call with a constrained system prompt that returns RELATED or NEW.
type TopicClassifier struct {
	llm interfaces.LLM
}

func NewTopicClassifier(llm interfaces.LLM) *TopicClassifier {
	return &TopicClassifier{llm: llm}
}

const classifierSystemPrompt = `You are a conversation topic classifier for a business analytics chatbot. ` +
	`You will be given (1) a brief summary of a previous conversation thread, and (2) a NEW user message that arrived after a long pause. ` +
	`Decide whether the new message continues the previous topic or starts a brand-new topic. ` +
	`Reply with exactly one word: RELATED or NEW. No explanation.`

// IsRelated returns true if the new message should continue the existing
// thread, false if a new thread should be started.
func (c *TopicClassifier) IsRelated(ctx context.Context, summary, newMessage string) (bool, error) {
	if strings.TrimSpace(summary) == "" {
		// No prior context — always treat as a new thread.
		return false, nil
	}

	prompt := "Previous thread summary:\n" + summary + "\n\nNew user message:\n" + newMessage + "\n\nAnswer (RELATED or NEW):"
	resp, err := c.llm.Generate(ctx, prompt,
		interfaces.WithSystemMessage(classifierSystemPrompt),
		interfaces.WithTemperature(0),
		interfaces.WithStopSequences([]string{"\n"}),
	)
	if err != nil {
		// Fail-open: on classifier failure, continue the existing thread
		// rather than incorrectly fragmenting the conversation.
		return true, err
	}
	return strings.Contains(strings.ToUpper(resp), "RELATED"), nil
}
