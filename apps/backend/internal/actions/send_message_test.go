package actions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeMessenger records calls and answers Allowed from a fixed set, so a test can
// assert that Execute refuses an un-allowlisted target before Send is reached.
type fakeMessenger struct {
	allow    map[string]bool
	sent     []string // "channel|target|body" per delivered message
	allowErr error
	sendErr  error
}

func (f *fakeMessenger) Allowed(_ context.Context, channel domain.Channel, targetRef string) (bool, error) {
	if f.allowErr != nil {
		return false, f.allowErr
	}
	return f.allow[string(channel)+"|"+targetRef], nil
}

func (f *fakeMessenger) Send(_ context.Context, channel domain.Channel, targetRef, body string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, string(channel)+"|"+targetRef+"|"+body)
	return nil
}

func params(t *testing.T, m map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSendMessageValidate(t *testing.T) {
	a := NewSendMessageAction(&fakeMessenger{})
	cases := []struct {
		name    string
		p       map[string]string
		wantErr bool
	}{
		{"ok", map[string]string{"channel": "whatsapp", "target_ref": "+62811", "body": "hi"}, false},
		{"unsupported channel", map[string]string{"channel": "discord", "target_ref": "c1", "body": "hi"}, true},
		{"missing target", map[string]string{"channel": "whatsapp", "body": "hi"}, true},
		{"missing body", map[string]string{"channel": "whatsapp", "target_ref": "+62811"}, true},
		{"case-insensitive channel", map[string]string{"channel": "WhatsApp", "target_ref": "+62811", "body": "hi"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := a.Validate(params(t, tc.p))
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate(%v) err=%v, wantErr=%v", tc.p, err, tc.wantErr)
			}
		})
	}
}

func TestSendMessageDescribe(t *testing.T) {
	a := NewSendMessageAction(&fakeMessenger{})
	got, err := a.Describe(params(t, map[string]string{"channel": "whatsapp", "target_ref": "+62811", "body": "Q3 is up 12%"}))
	if err != nil {
		t.Fatal(err)
	}
	// The sentence a human approves must name the recipient and quote the body.
	if !strings.Contains(got, "+62811") || !strings.Contains(got, "Q3 is up 12%") {
		t.Fatalf("Describe = %q; want recipient and body", got)
	}
}

func TestSendMessageExecuteRefusesUnallowlistedTarget(t *testing.T) {
	msgr := &fakeMessenger{allow: map[string]bool{}} // nothing allowlisted
	a := NewSendMessageAction(msgr)
	_, err := a.Execute(context.Background(),
		params(t, map[string]string{"channel": "whatsapp", "target_ref": "+62999", "body": "hi"}))
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Execute err = %v; want ErrInvalidInput for un-allowlisted target", err)
	}
	// The guardrail is that nothing was sent, not merely that an error came back.
	if len(msgr.sent) != 0 {
		t.Fatalf("delivered %d messages to an un-allowlisted target; want 0", len(msgr.sent))
	}
}

func TestSendMessageExecuteDeliversToAllowlistedTarget(t *testing.T) {
	msgr := &fakeMessenger{allow: map[string]bool{"whatsapp|+62811": true}}
	a := NewSendMessageAction(msgr)
	res, err := a.Execute(context.Background(),
		params(t, map[string]string{"channel": "whatsapp", "target_ref": "+62811", "body": "hello"}))
	if err != nil {
		t.Fatalf("Execute err = %v; want nil", err)
	}
	if len(msgr.sent) != 1 || msgr.sent[0] != "whatsapp|+62811|hello" {
		t.Fatalf("sent = %v; want one whatsapp|+62811|hello", msgr.sent)
	}
	if !strings.Contains(string(res), "delivered") {
		t.Fatalf("result = %s; want a delivered marker", res)
	}
}
