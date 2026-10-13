package whatsapp

import "strings"

// AttachmentReply is what a WhatsApp sender hears back when they send a voice
// note, photo, video, file or sticker with no text (RIZ-85, slice A).
//
// Both languages in one message, Indonesian first. A voice note carries no text,
// so there is nothing to detect a language from; most senders are Indonesian,
// while the channel's other fixed replies (not authorised, credits exhausted)
// are English. Two paragraphs are right for every sender and need no lookup of
// the company's locale on the webhook path.
//
// It says "yet" on purpose: transcribing a voice note is slice B, and this line
// is what a sender reads until it ships. Exported so the handler's tests compare
// it exactly rather than by a fragment that would survive a wrong edit.
const AttachmentReply = "Maaf, saya belum bisa mendengarkan pesan suara atau membuka lampiran. Silakan ketik pertanyaan Anda.\n\n" +
	"Sorry, I can't listen to voice notes or open attachments yet. Please type your question."

// Media kinds. Meta names an inbound message by these words in its `type`
// field, and TwilioMediaKind maps Twilio's MIME types onto the same words, so a
// log line reads the same whichever transport the deployment runs.
const (
	MediaKindAudio    = "audio"
	MediaKindImage    = "image"
	MediaKindVideo    = "video"
	MediaKindDocument = "document"
	MediaKindSticker  = "sticker"
)

// IsAttachment reports whether an inbound message type carries media the bot
// cannot read: a voice note or other audio, an image, a video, a document or a
// sticker.
//
// The other non-text types Meta sends — reaction, location, contacts, button,
// interactive, unsupported — are deliberately not attachments. A reaction or a
// shared location is not someone waiting for an answer, and replying "please
// type" to a thumbs-up would be noise; those are logged and dropped instead.
func IsAttachment(messageType string) bool {
	switch messageType {
	case MediaKindAudio, MediaKindImage, MediaKindVideo, MediaKindDocument, MediaKindSticker:
		return true
	}
	return false
}

// TwilioMediaKind maps a Twilio `MediaContentType0` onto a media kind.
//
// Twilio has no message type, only the MIME type of each attachment, so the
// kind is read off the top-level type. A WhatsApp voice note arrives as
// `audio/ogg` (with or without `; codecs=opus`), and a sticker as `image/webp` —
// which is why there is no sticker arm here: on Twilio a sticker is an image,
// and telling them apart would mean guessing from the subtype. Anything that is
// not audio, image or video — a PDF, a vCard, a type Twilio adds next year, or
// no content type at all — is a document, because it is still an attachment the
// sender is waiting on an answer about.
func TwilioMediaKind(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(ct, "audio/"):
		return MediaKindAudio
	case strings.HasPrefix(ct, "image/"):
		return MediaKindImage
	case strings.HasPrefix(ct, "video/"):
		return MediaKindVideo
	}
	return MediaKindDocument
}
