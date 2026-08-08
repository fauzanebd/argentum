package slack

import (
	"errors"
	"testing"
)

const botID = "U0BOTBOT"

func TestParseEnvelope_urlVerification(t *testing.T) {
	env, err := ParseEnvelope([]byte(`{
		"token":"tok","challenge":"3eZbrw1a","type":"url_verification"}`))
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != TypeURLVerification {
		t.Fatalf("type: %q", env.Type)
	}
	if env.Challenge != "3eZbrw1a" {
		t.Fatalf("challenge: %q", env.Challenge)
	}
}

func TestParseEnvelope_eventCallback(t *testing.T) {
	env, err := ParseEnvelope([]byte(`{
		"type":"event_callback",
		"team_id":"T123",
		"api_app_id":"A123",
		"event_id":"Ev123",
		"authorizations":[{"team_id":"T123","user_id":"U0BOTBOT","is_bot":true}],
		"event":{"type":"app_mention","user":"U999","text":"<@U0BOTBOT> hi","ts":"1700000000.000100","channel":"C123"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if env.TeamID != "T123" || env.APIAppID != "A123" {
		t.Fatalf("envelope ids: %+v", env)
	}
	if got := env.BotUserID(); got != botID {
		t.Fatalf("BotUserID: %q", got)
	}

	ev, err := ParseEvent(env.Event)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != EventAppMention || ev.User != "U999" || ev.Channel != "C123" {
		t.Fatalf("event: %+v", ev)
	}
}

func TestBotUserID_absent(t *testing.T) {
	env, err := ParseEnvelope([]byte(`{"type":"event_callback",
		"authorizations":[{"team_id":"T1","user_id":"U777","is_bot":false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.BotUserID(); got != "" {
		t.Fatalf("expected no bot id, got %q", got)
	}
}

func TestParseEvent_empty(t *testing.T) {
	if _, err := ParseEvent(nil); !errors.Is(err, ErrNotMessageEvent) {
		t.Fatalf("expected ErrNotMessageEvent, got %v", err)
	}
}

func TestThreadKey(t *testing.T) {
	// Top-level mention: replying to its own ts opens the thread.
	top := &Event{TS: "1700000000.000100"}
	if got := top.ThreadKey(); got != "1700000000.000100" {
		t.Fatalf("top-level: %q", got)
	}
	// Already threaded: stay in the parent thread.
	reply := &Event{TS: "1700000009.000200", ThreadTS: "1700000000.000100"}
	if got := reply.ThreadKey(); got != "1700000000.000100" {
		t.Fatalf("threaded: %q", got)
	}
}

func TestActionable(t *testing.T) {
	base := func(mut func(*Event)) *Event {
		e := &Event{
			Type:    EventAppMention,
			User:    "U999",
			Text:    "<@U0BOTBOT> sales last week?",
			TS:      "1700000000.000100",
			Channel: "C123",
		}
		if mut != nil {
			mut(e)
		}
		return e
	}

	cases := []struct {
		name string
		ev   *Event
		want bool
	}{
		{"app_mention in channel", base(nil), true},
		{"DM message", base(func(e *Event) {
			e.Type = EventMessage
			e.ChannelType = "im"
			e.Channel = "D123"
		}), true},
		{"DM detected by channel id prefix", base(func(e *Event) {
			e.Type = EventMessage
			e.Channel = "D123"
		}), true}, // channel_type missing; the D-prefix still identifies a DM
		{"channel message without mention", base(func(e *Event) {
			e.Type = EventMessage
			e.ChannelType = "channel"
		}), false},
		{"bot's own message", base(func(e *Event) { e.User = botID }), false},
		{"message posted by a bot", base(func(e *Event) { e.BotID = "B123" }), false},
		{"edited message", base(func(e *Event) { e.Subtype = "message_changed" }), false},
		{"join notice", base(func(e *Event) { e.Subtype = "channel_join" }), false},
		{"empty text", base(func(e *Event) { e.Text = "   " }), false},
		{"no user", base(func(e *Event) { e.User = "" }), false},
		{"unknown event type", base(func(e *Event) { e.Type = "reaction_added" }), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.Actionable(botID); got != tc.want {
				t.Fatalf("Actionable = %v, want %v", got, tc.want)
			}
		})
	}
}

// Actionable must still cut the bot's own echo when bot_user_id was never
// configured — bot_id is always stamped by Slack on bot posts.
func TestActionable_unknownBotUserID(t *testing.T) {
	echo := &Event{
		Type: EventAppMention, User: botID, BotID: "B123",
		Text: "the answer", TS: "1700000000.000100", Channel: "C123",
	}
	if echo.Actionable("") {
		t.Fatal("bot echo accepted when bot_user_id unknown")
	}
	human := &Event{
		Type: EventAppMention, User: "U999",
		Text: "<@U0BOTBOT> hi", TS: "1700000000.000100", Channel: "C123",
	}
	if !human.Actionable("") {
		t.Fatal("human mention rejected when bot_user_id unknown")
	}
}

func TestIsDM(t *testing.T) {
	if !(&Event{ChannelType: "im"}).IsDM() {
		t.Fatal("channel_type=im not detected")
	}
	// app_mention omits channel_type; the D-prefix is the only signal.
	if !(&Event{Channel: "D0123"}).IsDM() {
		t.Fatal("D-prefixed channel not detected")
	}
	if (&Event{Channel: "C0123", ChannelType: "channel"}).IsDM() {
		t.Fatal("public channel misread as DM")
	}
}
