package slack

import "testing"

func TestMentionsBot(t *testing.T) {
	cases := []struct {
		name, text string
		want       bool
	}{
		{"plain mention", "<@U0BOTBOT> hello", true},
		{"labelled mention", "<@U0BOTBOT|argentum> hello", true},
		{"mid-sentence", "hey <@U0BOTBOT> what's up", true},
		{"someone else", "<@U111AAA> hello", false},
		{"no mention at all", "hello", false},
		{"bare at-name is not a mention", "@argentum hello", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MentionsBot(tc.text, botID); got != tc.want {
				t.Fatalf("MentionsBot(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestMentionsBot_unknownBotID(t *testing.T) {
	if MentionsBot("<@U0BOTBOT> hi", "") {
		t.Fatal("empty bot id must never match")
	}
}

func TestStripMentions(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<@U0BOTBOT> sales last week?", "sales last week?"},
		{"<@U0BOTBOT|argentum> sales last week?", "sales last week?"},
		{"hey <@U0BOTBOT> can <@U111AAA> see this too?", "hey can see this too?"},
		{"  <@U0BOTBOT>   spaced   out  ", "spaced out"},
		{"no mentions here", "no mentions here"},
		{"<@U0BOTBOT>", ""},
	}
	for _, tc := range cases {
		if got := StripMentions(tc.in); got != tc.want {
			t.Fatalf("StripMentions(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
