package app

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// A render result reaches the channel that asked for it (T-G6, finding 6).
//
// Before this seam the worker wrote the result to the thread and published
// it on the dashboard bus, and a WhatsApp user who had been told "it will be
// posted into this conversation" received nothing. These tests drive the one
// announcement reachable without a renderer — the "not available" refusal —
// because the seam is the same for every result, and a refusal that never
// arrives is the worst of them.

type recordingAnnouncer struct {
	appended  []*domain.Message
	published []ChatEvent
}

func (a *recordingAnnouncer) Append(_ context.Context, m *domain.Message) error {
	a.appended = append(a.appended, m)
	return nil
}
func (a *recordingAnnouncer) Publish(_ string, evt ChatEvent) error {
	a.published = append(a.published, evt)
	return nil
}

// recLark records both doors, because the choice between them is the test.
type recLark struct{ replies, sends []string }

func (l *recLark) Reply(_ context.Context, _, messageID, _ string) error {
	l.replies = append(l.replies, messageID)
	return nil
}
func (l *recLark) Send(_ context.Context, _, chatID, _ string) error {
	l.sends = append(l.sends, chatID)
	return nil
}

type recSlack struct{ channels, threadTS, bodies []string }

func (s *recSlack) Reply(_ context.Context, _, channelID, threadTS, content string) error {
	s.channels = append(s.channels, channelID)
	s.threadTS = append(s.threadTS, threadTS)
	s.bodies = append(s.bodies, content)
	return nil
}
func (s *recSlack) Send(ctx context.Context, companyID, channelID, content string) error {
	return s.Reply(ctx, companyID, channelID, "", content)
}

type deliveryRig struct {
	svc       *APIReportService
	announcer *recordingAnnouncer
	wa        *fakeWA
	lark      *recLark
	slack     *recSlack
	bus       *fakeBus
}

func newDeliveryRig() *deliveryRig {
	r := &deliveryRig{announcer: &recordingAnnouncer{}, wa: &fakeWA{}, lark: &recLark{}, slack: &recSlack{}, bus: &fakeBus{}}
	// gen nil: the deployment without object storage, whose refusal is the
	// one result reachable here. Everything else about delivery is the same.
	r.svc = NewAPIReportService(newFakeReports(), &fakeDocLookup{}, nil, nil).
		WithThreadAnnouncer(r.announcer).
		WithChannelDelivery(r.wa, r.lark, r.slack, r.bus)
	return r
}

func threadedJob(target *tenantctx.ReplyTarget) queue.ReportRenderPayload {
	return queue.ReportRenderPayload{CompanyID: "co-1", ThreadID: "th-1", Target: target}
}

func TestARenderResultReachesEachChannel(t *testing.T) {
	cases := []struct {
		name   string
		target tenantctx.ReplyTarget
		check  func(t *testing.T, r *deliveryRig)
	}{
		{"whatsapp", tenantctx.ReplyTarget{Channel: "whatsapp", PhoneNumber: "+628123"}, func(t *testing.T, r *deliveryRig) {
			if len(r.wa.sent) != 1 || !strings.Contains(r.wa.sent[0], "could not be rendered") {
				t.Errorf("whatsapp got %q", r.wa.sent)
			}
		}},
		{"discord", tenantctx.ReplyTarget{Channel: "discord", DiscordChannelID: "chan-9", DiscordUserID: "user-3"}, func(t *testing.T, r *deliveryRig) {
			if len(r.bus.outbound) != 1 {
				t.Fatalf("discord outbound = %+v", r.bus.outbound)
			}
			e := r.bus.outbound[0]
			if e.Channel != "discord" || e.ChannelRef != "chan-9" || e.UserRef != "user-3" || e.CompanyID != "co-1" {
				t.Errorf("outbound = %+v", e)
			}
		}},
		{"lark replies to the message that asked", tenantctx.ReplyTarget{Channel: "lark", LarkMessageID: "om_1", LarkChatID: "oc_1"}, func(t *testing.T, r *deliveryRig) {
			if len(r.lark.replies) != 1 || r.lark.replies[0] != "om_1" || len(r.lark.sends) != 0 {
				t.Errorf("lark replies=%v sends=%v", r.lark.replies, r.lark.sends)
			}
		}},
		{"lark sends to the chat when there is no message", tenantctx.ReplyTarget{Channel: "lark", LarkChatID: "oc_1"}, func(t *testing.T, r *deliveryRig) {
			if len(r.lark.sends) != 1 || r.lark.sends[0] != "oc_1" || len(r.lark.replies) != 0 {
				t.Errorf("lark replies=%v sends=%v", r.lark.replies, r.lark.sends)
			}
		}},
		{"slack into the thread", tenantctx.ReplyTarget{Channel: "slack", SlackChannelID: "C1", SlackThreadTS: "171.2"}, func(t *testing.T, r *deliveryRig) {
			if len(r.slack.channels) != 1 || r.slack.channels[0] != "C1" || r.slack.threadTS[0] != "171.2" {
				t.Errorf("slack channels=%v ts=%v", r.slack.channels, r.slack.threadTS)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newDeliveryRig()
			if err := r.svc.RunRenderJob(context.Background(), threadedJob(&tc.target)); err != nil {
				t.Fatalf("RunRenderJob: %v", err)
			}
			// The thread is still the record, and still published for the
			// dashboard: delivery is in addition, never instead.
			if len(r.announcer.appended) != 1 || len(r.announcer.published) != 1 {
				t.Errorf("thread got %d messages and %d events, want 1 and 1", len(r.announcer.appended), len(r.announcer.published))
			}
			tc.check(t, r)
		})
	}
}

// A dashboard, API or widget turn — or a job queued before the target
// existed — is delivered by the thread message alone. Sending anywhere else
// would be a second copy of an answer already on screen.
func TestAThreadOnlyTargetSendsNowhere(t *testing.T) {
	for _, target := range []*tenantctx.ReplyTarget{
		nil,
		{Channel: "dashboard"},
		{Channel: "api"},
		{Channel: "widget"},
	} {
		r := newDeliveryRig()
		if err := r.svc.RunRenderJob(context.Background(), threadedJob(target)); err != nil {
			t.Fatalf("RunRenderJob: %v", err)
		}
		if len(r.announcer.appended) != 1 {
			t.Errorf("target %+v: thread got %d messages, want 1", target, len(r.announcer.appended))
		}
		if len(r.wa.sent)+len(r.bus.outbound)+len(r.lark.replies)+len(r.lark.sends)+len(r.slack.channels) != 0 {
			t.Errorf("target %+v: something was sent to a channel", target)
		}
	}
}

// A channel target on a deployment without that channel's provider — Lark
// disabled, say — is a log line, not a nil dereference, and the thread still
// gets its message.
func TestAMissingProviderIsSkippedNotFatal(t *testing.T) {
	announcer := &recordingAnnouncer{}
	svc := NewAPIReportService(newFakeReports(), &fakeDocLookup{}, nil, nil).WithThreadAnnouncer(announcer)
	for _, target := range []tenantctx.ReplyTarget{
		{Channel: "whatsapp", PhoneNumber: "+62"},
		{Channel: "discord", DiscordChannelID: "c"},
		{Channel: "lark", LarkChatID: "oc"},
		{Channel: "slack", SlackChannelID: "C1"},
	} {
		if err := svc.RunRenderJob(context.Background(), threadedJob(&target)); err != nil {
			t.Fatalf("%s: RunRenderJob: %v", target.Channel, err)
		}
	}
	if len(announcer.appended) != 4 {
		t.Errorf("thread got %d messages, want 4", len(announcer.appended))
	}
}

func carouselResult() *docgen.Result {
	return &docgen.Result{
		Document:    &domain.Document{ID: "doc-1", Format: domain.DocumentFormatCarousel, PageCount: 3, Filename: "carousel.zip"},
		DownloadURL: "https://bucket.example/carousel.zip?sig=1",
		Carousel: &docgen.CarouselManifest{
			Caption:  "Agustus tumbuh 9,8%",
			Hashtags: []string{"promo", "agustus"},
			Alts:     []string{"Cover", "Revenue by channel", "Call to action"},
			Pages:    3,
		},
	}
}

// The roadmap's acceptance line for a WhatsApp-bound thread: the caption and
// the zip link, and no `![` text. The thread's message carries inline images
// on the authenticated page route, which is a broken path on a phone; the
// channel's carries a signed link per slide instead.
func TestTheChannelCarouselMessageHasNoInlineImages(t *testing.T) {
	pages := []string{"https://b/1.jpg?s", "https://b/2.jpg?s", "https://b/3.jpg?s"}
	msg := carouselChannelMessage(carouselResult(), pages)

	if strings.Contains(msg, "![") {
		t.Errorf("an inline image reached a channel message:\n%s", msg)
	}
	if strings.Contains(msg, "```") {
		t.Errorf("a fence reached a channel message; a phone shows the backticks:\n%s", msg)
	}
	if strings.Contains(msg, "/api/documents/") {
		t.Errorf("the authenticated page route reached a channel message:\n%s", msg)
	}
	for _, want := range []string{
		"3 slides",
		"Agustus tumbuh 9,8%",
		"#promo #agustus",
		"[Slide 1 — Cover](https://b/1.jpg?s)",
		"[Slide 3 — Call to action](https://b/3.jpg?s)",
		"[Download all slides (carousel.zip)](https://bucket.example/carousel.zip?sig=1)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("channel message lacks %q:\n%s", want, msg)
		}
	}

	// A page that could not be signed is left out, and the zip still travels.
	short := carouselChannelMessage(carouselResult(), nil)
	if strings.Contains(short, "Slide ") || !strings.Contains(short, "Download all slides") {
		t.Errorf("with no pages signed:\n%s", short)
	}
}

// The WhatsApp path flattens links the way a chat reply's are flattened, so
// the phone auto-links the raw URL: "Slide 1 — Cover: https://…", not a
// markdown link.
func TestWhatsAppGetsFlattenedSlideLinks(t *testing.T) {
	r := newDeliveryRig()
	target := tenantctx.ReplyTarget{Channel: "whatsapp", PhoneNumber: "+628123"}
	r.svc.deliver(context.Background(), threadedJob(&target), carouselChannelMessage(carouselResult(), []string{"https://b/1.jpg?s"}))

	if len(r.wa.sent) != 1 {
		t.Fatalf("whatsapp got %d messages", len(r.wa.sent))
	}
	got := r.wa.sent[0]
	if strings.Contains(got, "](") {
		t.Errorf("a markdown link reached WhatsApp:\n%s", got)
	}
	if !strings.Contains(got, "Slide 1 — Cover: https://b/1.jpg?s") {
		t.Errorf("the slide link was not flattened:\n%s", got)
	}
}

// Presigning slides is for the channel. A dashboard thread reads pages through
// its own route, so no URL is signed for it — and with no renderer installed
// there is nothing to sign with either way.
func TestPagesArePresignedOnlyForAChannel(t *testing.T) {
	r := newDeliveryRig()
	doc := &domain.Document{ID: "doc-1", Format: domain.DocumentFormatCarousel, PageCount: 3}
	if got := r.svc.presignPages(context.Background(), threadedJob(&tenantctx.ReplyTarget{Channel: "dashboard"}), doc); got != nil {
		t.Errorf("a dashboard thread got %v signed", got)
	}
	if !deliversToChannel(&tenantctx.ReplyTarget{Channel: "whatsapp"}) || deliversToChannel(nil) || deliversToChannel(&tenantctx.ReplyTarget{Channel: "api"}) {
		t.Error("deliversToChannel disagrees with deliver's switch")
	}
}
