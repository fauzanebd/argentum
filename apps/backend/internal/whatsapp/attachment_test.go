package whatsapp

import (
	"strings"
	"testing"
)

// RIZ-85, R2: Twilio carries no message type, only a MIME type per attachment,
// and the handler labels the message with the kind this returns.
func TestTwilioMediaKind(t *testing.T) {
	cases := []struct {
		contentType string
		want        string
	}{
		// What a WhatsApp voice note is on Twilio, with and without the codec.
		{"audio/ogg", MediaKindAudio},
		{"audio/ogg; codecs=opus", MediaKindAudio},
		{"audio/mpeg", MediaKindAudio},
		{"audio/amr", MediaKindAudio},
		{"image/jpeg", MediaKindImage},
		{"image/png", MediaKindImage},
		// A sticker. Twilio sends no sticker type, so it is an image.
		{"image/webp", MediaKindImage},
		{"video/mp4", MediaKindVideo},
		{"video/3gpp", MediaKindVideo},
		{"application/pdf", MediaKindDocument},
		{"text/vcard", MediaKindDocument},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", MediaKindDocument},
		// Case and padding are Twilio's to choose, not ours to depend on.
		{"  Audio/OGG ", MediaKindAudio},
		{"IMAGE/JPEG", MediaKindImage},
		// NumMedia said there was one and the type is missing: still an
		// attachment, still worth a reply.
		{"", MediaKindDocument},
		// A prefix match must not be a substring match.
		{"application/audio", MediaKindDocument},
		{"audio", MediaKindDocument},
	}
	for _, tc := range cases {
		t.Run(tc.contentType, func(t *testing.T) {
			if got := TwilioMediaKind(tc.contentType); got != tc.want {
				t.Errorf("TwilioMediaKind(%q) = %q, want %q", tc.contentType, got, tc.want)
			}
		})
	}
}

// RIZ-85, R2: Meta's message `type` is used as it is. Five types are
// attachments; everything else that is not text is a drop, not a reply.
func TestIsAttachment(t *testing.T) {
	cases := []struct {
		messageType string
		want        bool
	}{
		{"audio", true},
		{"image", true},
		{"video", true},
		{"document", true},
		{"sticker", true},
		{"text", false},
		{"reaction", false},
		{"location", false},
		{"contacts", false},
		{"button", false},
		{"interactive", false},
		{"unsupported", false},
		{"", false},
		// Meta sends lowercase; anything else is not a type it sends.
		{"AUDIO", false},
	}
	for _, tc := range cases {
		t.Run(tc.messageType, func(t *testing.T) {
			if got := IsAttachment(tc.messageType); got != tc.want {
				t.Errorf("IsAttachment(%q) = %v, want %v", tc.messageType, got, tc.want)
			}
		})
	}
}

// Every kind TwilioMediaKind can return is one IsAttachment accepts, or a Twilio
// voice note would be labelled and then dropped.
func TestTwilioMediaKindsAreAttachments(t *testing.T) {
	for _, ct := range []string{"audio/ogg", "image/jpeg", "video/mp4", "application/pdf", ""} {
		if kind := TwilioMediaKind(ct); !IsAttachment(kind) {
			t.Errorf("TwilioMediaKind(%q) = %q, which IsAttachment refuses", ct, kind)
		}
	}
}

// The copy is the owner's approved text (decision 1), word for word: Indonesian
// first, a blank line, then English. A one-character edit here is a copy change
// and should have to change this test.
func TestAttachmentReplyIsTheApprovedCopy(t *testing.T) {
	paragraphs := strings.Split(AttachmentReply, "\n\n")
	if len(paragraphs) != 2 {
		t.Fatalf("AttachmentReply has %d paragraphs, want 2: %q", len(paragraphs), AttachmentReply)
	}
	if want := "Maaf, saya belum bisa mendengarkan pesan suara atau membuka lampiran. Silakan ketik pertanyaan Anda."; paragraphs[0] != want {
		t.Errorf("Indonesian paragraph = %q, want %q", paragraphs[0], want)
	}
	if want := "Sorry, I can't listen to voice notes or open attachments yet. Please type your question."; paragraphs[1] != want {
		t.Errorf("English paragraph = %q, want %q", paragraphs[1], want)
	}
}
