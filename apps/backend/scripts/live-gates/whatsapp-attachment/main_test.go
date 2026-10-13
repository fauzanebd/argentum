package main

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// The tool is only worth running if the API accepts what it signs. Checked
// against the verifier the webhook itself uses, so a drift between the two is
// a failing test here rather than a 401 in the middle of a live gate.
func TestTheSignedFormPassesTheWebhookVerifier(t *testing.T) {
	const token = "twilio-auth-token"
	verifier := whatsapp.NewTwilioClient("AC123", token, "+14155238886")

	for _, target := range []string{
		"http://localhost:8080/webhook/whatsapp",
		"https://api.argentum.example/webhook/whatsapp",
		"http://localhost:8080/webhook/whatsapp?tenant=1",
	} {
		t.Run(target, func(t *testing.T) {
			form := voiceNoteForm("+628110000000", "+14155238886", "", "audio/ogg", 1)
			signed, err := signedURL(target)
			if err != nil {
				t.Fatal(err)
			}
			sig := signature(token, signed, form)
			if !verifier.VerifyWebhook([]byte(form.Encode()), sig, signed) {
				t.Fatalf("the webhook refuses the form this tool signed for %s", signed)
			}
			form.Set("Body", "tampered")
			if verifier.VerifyWebhook([]byte(form.Encode()), sig, signed) {
				t.Fatal("a changed form still verified; the test proves nothing")
			}
		})
	}
}

func TestSignedURLIsAlwaysHTTPS(t *testing.T) {
	got, err := signedURL("http://localhost:8080/webhook/whatsapp")
	if err != nil || got != "https://localhost:8080/webhook/whatsapp" {
		t.Fatalf("signedURL = %q, %v", got, err)
	}
	if _, err := signedURL("/webhook/whatsapp"); err == nil {
		t.Fatal("a relative URL was accepted")
	}
}

// What Q8 posts: one audio attachment and no text, which is what the handler
// answers with the attachment reply.
func TestVoiceNoteFormIsAnAttachmentWithNoText(t *testing.T) {
	f := voiceNoteForm("+628110000000", "whatsapp:+14155238886", "", "audio/ogg", 1)
	if f.Get("Body") != "" || f.Get("NumMedia") != "1" || f.Get("MediaContentType0") != "audio/ogg" {
		t.Errorf("form = %v", f)
	}
	if f.Get("From") != "whatsapp:+628110000000" || f.Get("To") != "whatsapp:+14155238886" {
		t.Errorf("From/To = %q/%q", f.Get("From"), f.Get("To"))
	}
	if f.Get("MediaUrl0") == "" || f.Get("MessageSid") == "" {
		t.Errorf("form = %v, want a media URL and a message sid", f)
	}
	if none := voiceNoteForm("+628110000000", "+1", "hi", "audio/ogg", 0); none.Get("MediaUrl0") != "" || none.Get("MediaContentType0") != "" {
		t.Errorf("NumMedia=0 still carries media: %v", none)
	}
}
