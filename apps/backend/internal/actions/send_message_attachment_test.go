package actions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// send_message and `attach_document_id` (T-V3).
//
// The field existed from T-12a and was ignored — "validated as a well-formed id
// if present, never fetched". A watcher briefing that says "the monthly report
// is attached" and attaches nothing is the silent shape this repository keeps
// finding, so what these pin is mostly what happens when the attachment cannot
// be honoured.

type fakeLinker struct {
	att       Attachment
	err       error
	gotCompan string
	gotDoc    string
}

func (f *fakeLinker) LinkForDocument(_ context.Context, companyID, documentID string) (Attachment, error) {
	f.gotCompan, f.gotDoc = companyID, documentID
	if f.err != nil {
		return Attachment{}, f.err
	}
	return f.att, nil
}

func allowedTo(target string) *fakeMessenger {
	return &fakeMessenger{allow: map[string]bool{"whatsapp|" + target: true}}
}

func tenantCtx() context.Context {
	return tenantctx.WithCompanyID(context.Background(), "co-1")
}

func attachParams(t *testing.T, docID string) []byte {
	t.Helper()
	return params(t, map[string]string{
		"channel":            "whatsapp",
		"target_ref":         "+62811",
		"body":               "Here is June's report.",
		"attach_document_id": docID,
	})
}

// The link travels in the body, and it carries what a recipient needs before
// tapping it on mobile data: the name, the size, and the fact that it expires.
func TestAnAttachedDocumentBecomesALink(t *testing.T) {
	msgr := allowedTo("+62811")
	linker := &fakeLinker{att: Attachment{
		Filename:  "june-review.mp4",
		URL:       "https://store.invalid/june-review.mp4?sig=abc",
		SizeBytes: 5_865_049,
		ExpiresIn: time.Hour,
	}}
	a := NewSendMessageAction(msgr).WithDocuments(linker)

	if _, err := a.Execute(tenantCtx(), attachParams(t, "doc-1")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(msgr.sent) != 1 {
		t.Fatalf("%d messages sent", len(msgr.sent))
	}
	body := msgr.sent[0]
	for _, want := range []string{
		"Here is June's report.",
		"[june-review.mp4](https://store.invalid/june-review.mp4?sig=abc)",
		"5.6 MB",
		"expires in about an hour",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the message is missing %q:\n%s", want, body)
		}
	}
}

// **The tenant boundary.** The id comes from a model, and an action executes in
// a worker on somebody's behalf: the company from the context is what the
// lookup is scoped by, not a comparison made afterwards.
func TestTheAttachmentIsResolvedForTheTurnsCompany(t *testing.T) {
	linker := &fakeLinker{att: Attachment{Filename: "f.pdf", URL: "https://store.invalid/f"}}
	a := NewSendMessageAction(allowedTo("+62811")).WithDocuments(linker)

	if _, err := a.Execute(tenantCtx(), attachParams(t, "doc-9")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if linker.gotCompan != "co-1" || linker.gotDoc != "doc-9" {
		t.Fatalf("looked up (%q, %q), want (co-1, doc-9)", linker.gotCompan, linker.gotDoc)
	}
}

// A document that cannot be resolved refuses the **whole** action. Sending the
// message anyway would deliver a sentence about a report with no report in it,
// and the approver cannot check that afterwards because the message has gone.
func TestAnUnresolvableDocumentRefusesTheMessage(t *testing.T) {
	msgr := allowedTo("+62811")
	a := NewSendMessageAction(msgr).
		WithDocuments(&fakeLinker{err: errors.New("no such document")})

	_, err := a.Execute(tenantCtx(), attachParams(t, "doc-gone"))
	if err == nil {
		t.Fatal("a message with an unresolvable attachment was sent")
	}
	if len(msgr.sent) != 0 {
		t.Fatalf("the message went out anyway: %v", msgr.sent)
	}
}

// The allowlist still runs first. An attachment must not become a way to make
// the action fetch something before the recipient has been checked.
func TestTheAllowlistIsCheckedBeforeTheDocument(t *testing.T) {
	linker := &fakeLinker{att: Attachment{Filename: "f.pdf", URL: "https://store.invalid/f"}}
	msgr := &fakeMessenger{allow: map[string]bool{}} // nothing allowlisted
	a := NewSendMessageAction(msgr).WithDocuments(linker)

	if _, err := a.Execute(tenantCtx(), attachParams(t, "doc-1")); err == nil {
		t.Fatal("an un-allowlisted target was messaged")
	}
	if linker.gotDoc != "" {
		t.Errorf("the document was resolved for a refused recipient: %q", linker.gotDoc)
	}
}

// With no linker — a deployment with no object storage — the proposal is
// refused at Validate, so it is never stored and never put in front of a human.
// The same rule generate_document's format enum follows: do not offer what this
// process cannot finish.
func TestAnAttachmentIsRefusedWithoutALinker(t *testing.T) {
	a := NewSendMessageAction(allowedTo("+62811"))

	err := a.Validate(attachParams(t, "doc-1"))
	if err == nil {
		t.Fatal("a proposal that cannot be honoured was accepted")
	}
	if !strings.Contains(err.Error(), "not deliverable") {
		t.Errorf("unhelpful refusal: %v", err)
	}
	// And the ordinary message is untouched on the same deployment.
	if err := a.Validate(params(t, map[string]string{
		"channel": "whatsapp", "target_ref": "+62811", "body": "hello",
	})); err != nil {
		t.Errorf("a message with no attachment was refused: %v", err)
	}
}

// The approval card says a document travels. An approver who is not told is
// approving a different action from the one that runs.
func TestTheApprovalCardNamesTheAttachment(t *testing.T) {
	a := NewSendMessageAction(allowedTo("+62811")).WithDocuments(&fakeLinker{})

	got, err := a.Describe(attachParams(t, "doc-1"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(got, "doc-1") || !strings.Contains(got, "download link") {
		t.Errorf("the card does not say a document travels: %s", got)
	}
}

// A message with no attachment is byte-for-byte what it was before this
// change. The risk in appending to a body is appending to every body.
func TestAnOrdinaryMessageIsUnchanged(t *testing.T) {
	msgr := allowedTo("+62811")
	a := NewSendMessageAction(msgr).WithDocuments(&fakeLinker{})

	if _, err := a.Execute(tenantCtx(), params(t, map[string]string{
		"channel": "whatsapp", "target_ref": "+62811", "body": "Revenue is up 3.9%.",
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if want := "whatsapp|+62811|Revenue is up 3.9%."; msgr.sent[0] != want {
		t.Errorf("body = %q, want %q", msgr.sent[0], want)
	}
}

// humanSize and roughDuration are read by a person in a chat message, so the
// edges are pinned rather than left to whatever %.1f does.
func TestTheAttachmentLineReadsWell(t *testing.T) {
	cases := []struct {
		att  Attachment
		want []string
	}{
		{Attachment{Filename: "a.pdf", URL: "u", SizeBytes: 900}, []string{"900 bytes"}},
		{Attachment{Filename: "a.pdf", URL: "u", SizeBytes: 4096}, []string{"4 KB"}},
		{Attachment{Filename: "a.mp4", URL: "u", SizeBytes: 5 << 20}, []string{"5.0 MB"}},
		{Attachment{Filename: "a.mp4", URL: "u", ExpiresIn: 2 * time.Hour}, []string{"2 hours"}},
		{Attachment{Filename: "a.mp4", URL: "u", ExpiresIn: 15 * time.Minute}, []string{"15 minutes"}},
		// No size and no expiry: the line is still a working link rather than a
		// sentence with two gaps in it.
		{Attachment{Filename: "", URL: "u"}, []string{"[the document](u)"}},
	}
	for _, tc := range cases {
		line := attachmentLine(tc.att)
		for _, want := range tc.want {
			if !strings.Contains(line, want) {
				t.Errorf("attachmentLine(%+v) = %q, missing %q", tc.att, line, want)
			}
		}
	}
}

// The channel restriction is unchanged: an attachment does not open a door for
// a channel send_message cannot address safely.
func TestAttachmentDoesNotWidenTheChannelSet(t *testing.T) {
	a := NewSendMessageAction(allowedTo("+62811")).WithDocuments(&fakeLinker{})
	err := a.Validate(params(t, map[string]string{
		"channel": "discord", "target_ref": "chan-1", "body": "hi",
		"attach_document_id": "doc-1",
	}))
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("discord was accepted: %v", err)
	}
	_ = domain.ChannelDiscord
}
