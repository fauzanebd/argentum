package lark

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Envelope is the outer shape of every Lark callback. When encrypt_key is
// configured Lark wraps the actual event in `encrypt`; otherwise the event
// fields sit at the top level. url_verification is the one-shot challenge
// Lark posts when the webhook URL is first registered.
type Envelope struct {
	Encrypt   string          `json:"encrypt,omitempty"`
	Schema    string          `json:"schema,omitempty"`
	Type      string          `json:"type,omitempty"`
	Token     string          `json:"token,omitempty"`
	Challenge string          `json:"challenge,omitempty"`
	Header    EventHeader     `json:"header,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
}

// EventHeader is the v2 (schema=2.0) header sent on every non-verification
// callback.
type EventHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token      string `json:"token"`
	AppID      string `json:"app_id"`
	TenantKey  string `json:"tenant_key"`
}

// MessageReceiveEvent is the body of an `im.message.receive_v1` event.
type MessageReceiveEvent struct {
	Sender struct {
		SenderID struct {
			OpenID  string `json:"open_id"`
			UnionID string `json:"union_id"`
			UserID  string `json:"user_id"`
		} `json:"sender_id"`
		SenderType string `json:"sender_type"`
		TenantKey  string `json:"tenant_key"`
	} `json:"sender"`
	Message struct {
		MessageID   string    `json:"message_id"`
		RootID      string    `json:"root_id,omitempty"`
		ParentID    string    `json:"parent_id,omitempty"`
		ThreadID    string    `json:"thread_id,omitempty"`
		CreateTime  string    `json:"create_time"`
		ChatID      string    `json:"chat_id"`
		ChatType    string    `json:"chat_type"`
		MessageType string    `json:"message_type"`
		Content     string    `json:"content"`
		Mentions    []Mention `json:"mentions,omitempty"`
	} `json:"message"`
}

// Mention is one @mention inside a message.
type Mention struct {
	Key       string `json:"key"`
	ID        struct {
		OpenID  string `json:"open_id"`
		UnionID string `json:"union_id"`
		UserID  string `json:"user_id"`
	} `json:"id"`
	Name       string `json:"name"`
	TenantKey  string `json:"tenant_key"`
}

// TextContent is the unmarshal target for message_type=="text" content
// strings (Lark wraps the user text inside a JSON object).
type TextContent struct {
	Text string `json:"text"`
}

// ParseEnvelope reads the outer envelope and, if encrypted, returns the
// decrypted inner JSON body. When the payload is plaintext (no encrypt
// field), plaintext == body. The caller verifies signature separately.
func ParseEnvelope(body []byte, encryptKey string) (*Envelope, []byte, error) {
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, nil, fmt.Errorf("parse envelope: %w", err)
	}
	if env.Encrypt == "" {
		return &env, body, nil
	}
	plain, err := Decrypt(encryptKey, env.Encrypt)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt event: %w", err)
	}
	// Re-parse the decrypted body so callers see header/type/event.
	var inner Envelope
	if err := json.Unmarshal(plain, &inner); err != nil {
		return nil, nil, fmt.Errorf("parse decrypted envelope: %w", err)
	}
	return &inner, plain, nil
}

// DecodeText pulls the user-visible text out of message.content. Lark
// serializes the content as a JSON string, e.g. `{"text":"@_user_1 hi"}`.
func DecodeText(content string) (string, error) {
	if content == "" {
		return "", nil
	}
	var tc TextContent
	if err := json.Unmarshal([]byte(content), &tc); err != nil {
		return "", err
	}
	return tc.Text, nil
}

// ErrNotMessageEvent signals to webhook callers that this callback isn't
// an inbound message and should be silently ignored.
var ErrNotMessageEvent = errors.New("not a message receive event")
