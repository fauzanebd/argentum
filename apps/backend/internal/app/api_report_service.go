package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/video"
	"github.com/fauzanebd/argentum/internal/slack"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tracing"
	"github.com/fauzanebd/argentum/internal/webhookout"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// EventReportCompleted is the callback event name. One constant, because it
// appears in three places — the header, the body, and a tenant's switch
// statement — and the third one is not ours to fix if the first two drift.
const EventReportCompleted = "report.completed"

// APIReportService closes out the report jobs `/v1` hands back and delivers
// their callbacks (T-A2).
//
// It lives in the worker because both of its jobs do: an agentic report
// finishes when the agent's turn does, and a render job that overran its
// synchronous window has to be rendered somewhere that is not an HTTP handler.
// The API process constructs one too, but only reads through it.
type APIReportService struct {
	reports domain.APIReportRepository
	docs    domain.DocumentRepository
	gen     *docgen.Service
	sender  *webhookout.Sender
	// progress publishes `render_progress` for a job nobody can otherwise see
	// (T-V3). Nil is legal and means the caller polls, which is what every
	// render did before a format took minutes.
	progress ReportProgressBus
	// announcer puts a threaded render's result back in its conversation.
	announcer ThreadAnnouncer
	// The outbound providers a result is delivered through when the thread
	// lives on a channel (T-G6, finding 6). The same four the chat runner and
	// the watcher service hold; any may be nil, which skips that channel with
	// a log line rather than dialling nothing.
	wa    whatsapp.Provider
	lark  lark.Provider
	slack slack.Provider
	bus   EventBus
}

// ReportProgressBus is the slice of the event bus a render job needs.
// Declared at the consumer and narrow, so the worker's Redis bus satisfies it
// without this package depending on the transport.
type ReportProgressBus interface {
	PublishReport(reportID string, evt ChatEvent) error
}

// WithProgress installs the bus a threadless render job reports progress on.
func (s *APIReportService) WithProgress(bus ReportProgressBus) *APIReportService {
	s.progress = bus
	return s
}

// NewAPIReportService wires the service. gen and sender may be nil: a
// deployment without object storage cannot render at all, and one without a
// queue cannot deliver a callback. Neither is a reason for the rest to fail.
func NewAPIReportService(
	reports domain.APIReportRepository,
	docs domain.DocumentRepository,
	gen *docgen.Service,
	sender *webhookout.Sender,
) *APIReportService {
	return &APIReportService{reports: reports, docs: docs, gen: gen, sender: sender}
}

// CompleteReport closes out an agentic report when its turn ends.
//
// It is called from ChatRunner.completeWith, **before** the `final` event is
// published. That order is the whole reason the SSE bridge is simple: a client
// that sees `final` and then re-reads the report row is guaranteed a terminal
// status, so there is no poll loop and no window in which a finished report
// reports itself as running.
//
// A turn that produced no document still completes. The agent was asked for a
// report and answered in prose — that is a real outcome, and reporting it as a
// failure would tell an integrator to retry something that will do the same
// thing again. The absent `document_id` is what says what happened.
//
// **Every read and write here runs on a context detached from the turn's**, for
// the reason `T-A2b`'s gate of 2026-08-13 made concrete: three of eight reports
// sat at `queued` with an empty `error` column forever. The turn had died of
// `context deadline exceeded`, and the first thing this function does with that
// same context is a `Get` — which fails, logs *"the caller will keep polling"*
// and returns, so the branch that writes `failed` is precisely the branch that
// cannot run when a turn times out. The status write is not part of the turn's
// work; it is what has to happen *because* the turn ended, most of all when it
// ended badly. Same idiom as the audit decorator and `recordBlockedTurn`.
// CompleteReport marks a report finished and attaches the document the turn
// produced.
//
// **`docID` is passed in rather than re-derived, and that is the fix for a
// defect the 2026-08-13 gate found** (api-reports.md §7a). It used to call
// `NewestForThreadSince(companyID, threadID, rep.CreatedAt)`, whose bound is
// one-sided: it excludes documents older than the report and does nothing about
// newer ones. Ten calls sharing one thread produced a report that timed out
// generating nothing and was completed carrying a document created nine minutes
// later by a *different* request. Every prompt was identical there, so the
// content was harmless; with two different prompts the caller downloads the
// answer to somebody else's question and nothing in the response says so.
//
// No query can distinguish two turns on one thread. The turn can: it runs
// `generate_document` itself and knows the id it got back. So the id travels
// with the completion, and an empty one on a successful turn now means what it
// says — this turn generated nothing — instead of triggering a search that
// might find somebody else's file.
func (s *APIReportService) CompleteReport(ctx context.Context, reportID, threadID, docID string, runErr error) {
	if s == nil || s.reports == nil || reportID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	rep, err := s.reports.Get(ctx, reportID)
	if err != nil {
		logrus.WithError(err).WithField("report_id", reportID).
			Warn("report job not found while completing; the caller will keep polling")
		return
	}

	status := domain.APIReportCompleted
	errMsg := ""
	if runErr != nil {
		status = domain.APIReportFailed
		// Never the wrapped Go error. What lands here is read by an integrator
		// in their own logs, and a chain naming our packages tells them nothing
		// they can act on.
		errMsg = "The agent could not complete this report. Try again, or simplify the prompt."
	}

	if runErr != nil {
		// A failed report carries no file, whatever the turn managed to write
		// before it failed.
		docID = ""
	}
	if runErr == nil && docID == "" {
		logrus.WithFields(logrus.Fields{
			"company_id": rep.CompanyID,
			"report_id":  reportID,
		}).Info("agentic report turn finished without generating a document")
	}
	// Ownership is checked rather than trusted. The id arrives from the turn
	// and the turn is ours, but this row is what a caller downloads from, and
	// one confused id here is a cross-tenant file read.
	if docID != "" && s.docs != nil {
		if _, err := s.docs.GetForCompany(ctx, rep.CompanyID, docID); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id":  rep.CompanyID,
				"report_id":   reportID,
				"document_id": docID,
			}).Warn("the document a report turn reported does not belong to it; completing without one")
			docID = ""
		}
	}

	if err := s.reports.Complete(ctx, reportID, status, docID, errMsg, time.Now()); err != nil {
		logrus.WithError(err).WithField("report_id", reportID).
			Error("report job not marked complete; the caller will keep polling")
		return
	}
	rep.Status = status
	rep.DocumentID = docID
	rep.Error = errMsg
	s.notify(ctx, rep)
}

// RunRenderJob renders a spec that overran the synchronous window, or a video,
// which never waits for one (T-V3).
//
// Returning an error asks asynq to retry, and it does so only for failures
// that a retry could fix. A spec the renderer refuses is recorded as a failed
// job and returns nil: rendering is deterministic, so the second attempt
// produces the same refusal and the caller waits longer for the same answer.
func (s *APIReportService) RunRenderJob(ctx context.Context, p queue.ReportRenderPayload) error {
	// A job with no report id came from a turn, not from `/v1`, and its result
	// goes back to the thread. Split before the repository check below, because
	// that path needs no `api_reports` row and must not fail for the want of
	// one.
	if p.ReportID == "" {
		return s.runThreadRender(ctx, p)
	}
	if s.reports == nil {
		return fmt.Errorf("api report repository is not configured")
	}
	if s.gen == nil {
		s.fail(ctx, p.ReportID, "Document rendering is not available on this deployment.")
		return nil
	}
	if err := s.reports.MarkRunning(ctx, p.ReportID); err != nil {
		logrus.WithError(err).WithField("report_id", p.ReportID).
			Warn("report job not marked running; continuing")
	}

	ctx = tenantctx.WithCompanyID(ctx, p.CompanyID)
	if p.RequestID != "" {
		ctx = tenantctx.WithRequestID(ctx, p.RequestID)
	}
	// One span for the render, joined to the request that asked for it
	// (T-17b). A video is minutes long and the caller is holding nothing —
	// this is the only place the time goes, and until now none of it appeared
	// in a trace at all.
	ctx = tracing.Extract(ctx, p.Trace)
	ctx, span := tracing.Step(ctx, "report.render")
	defer span.End()
	tracing.QueueWait(span, p.EnqueuedAt)

	spec := p.Spec
	res, err := s.gen.Generate(ctx, docgen.Input{
		Spec:          &spec,
		CompanyID:     p.CompanyID,
		Source:        domain.DocumentSourceAPI,
		APIKeyID:      p.APIKeyID,
		EnforceLimits: true,
		OnProgress:    s.progressFor(p.ReportID),
	})
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": p.CompanyID,
			"report_id":  p.ReportID,
		}).Warn("asynchronous render failed")
		s.fail(ctx, p.ReportID, renderFailureMessage(err))
		return nil
	}

	if err := s.reports.Complete(ctx, p.ReportID, domain.APIReportCompleted, res.Document.ID, "", time.Now()); err != nil {
		// The document exists and is downloadable; only the job's bookkeeping
		// failed. Retrying is right — Complete is idempotent by its WHERE
		// clause, and a caller polling a job that never finishes is worse than
		// a second attempt at one UPDATE.
		return fmt.Errorf("complete render job: %w", err)
	}
	s.settled(p.ReportID, "final")
	if rep, err := s.reports.Get(ctx, p.ReportID); err == nil {
		s.notify(ctx, rep)
	}
	return nil
}

// settled tells an open SSE stream that a threadless render job is over.
//
// A threaded job gets this for free: ChatRunner publishes `final` on the
// thread's channel and the bridge in `GET /v1/reports/:id/events` closes on
// it. A render job has no thread, so the only thing ever published on its own
// channel was progress — and before a format took minutes that was harmless,
// because the job was already terminal by the time anyone could subscribe and
// the handler answered from its early return. `T-V3` made the streaming branch
// reachable for the first time and left nothing to end it: the 2026-08-09 gate
// watched progress climb to 0.94 and then heartbeat for ten minutes against a
// report that had been `completed`, with its file downloadable, since second
// seventy-one.
//
// Published after the row is terminal, never before — the same ordering
// `CompleteReport` documents, and what lets the handler answer by re-reading
// the row rather than by trusting the event's contents.
func (s *APIReportService) settled(reportID, kind string) {
	if s.progress == nil || reportID == "" {
		return
	}
	// Publish and forget. Nobody streaming is the ordinary case — a caller who
	// polls, or a callback consumer — and a Redis hiccup must not fail a
	// render whose file is already in object storage.
	_ = s.progress.PublishReport(reportID, ChatEvent{
		Type:      kind,
		Timestamp: time.Now(),
	})
}

// ThreadAnnouncer is how a render that finished after its turn gets back to
// the conversation that asked for it (T-V3).
//
// Two methods rather than a reach into ChatRunner: everything a chat turn does
// after the model stops — memory, usage, the fabrication check, channel
// delivery — is wrong for a file that arrives four minutes later, and the only
// parts that are right are the two below.
type ThreadAnnouncer interface {
	// Append persists the assistant message carrying the link.
	Append(ctx context.Context, m *domain.Message) error
	// Publish puts it on the thread's channel so an open dashboard shows it
	// without a reload.
	Publish(threadID string, evt ChatEvent) error
}

// WithThreadAnnouncer installs the seam a threaded render answers through.
// Nil leaves the document collectable — the row and the presigned URL exist
// either way — and silently, which is why the worker always installs one.
func (s *APIReportService) WithThreadAnnouncer(a ThreadAnnouncer) *APIReportService {
	s.announcer = a
	return s
}

// WithChannelDelivery installs the providers a threaded render's result is
// sent through when the turn that asked came in on a channel (T-G6,
// finding 6).
//
// Before this seam existed a render result was written to the thread and
// published on the dashboard bus, and that was all: a WhatsApp user who asked
// for a video was told "it is being rendered and will be posted into this
// conversation" and then nothing reached the phone, because the only process
// that delivers to a phone does so inside a turn and the turn was over. The
// decision was "every render result, or none"; this is every.
func (s *APIReportService) WithChannelDelivery(wa whatsapp.Provider, larkProv lark.Provider, slackProv slack.Provider, bus EventBus) *APIReportService {
	s.wa = wa
	s.lark = larkProv
	s.slack = slackProv
	s.bus = bus
	return s
}

// runThreadRender renders a video the agent asked for and announces it.
//
// The turn that asked is over. `generate_document` answered "it is rendering"
// and the model wrote its reply around that, because a tool call that blocks
// for four minutes spends `T-16`'s whole budget on waiting and then has no
// iterations left to say anything about the file. So this is the other half of
// that answer, and its failure mode is the one that matters: a video that
// never arrives and never explains itself is worse than one that was refused.
func (s *APIReportService) runThreadRender(ctx context.Context, p queue.ReportRenderPayload) error {
	if p.ThreadID == "" {
		return fmt.Errorf("render job has neither a report id nor a thread id")
	}
	ctx = tenantctx.WithCompanyID(ctx, p.CompanyID)
	ctx = tenantctx.WithThreadID(ctx, p.ThreadID)
	// Joined to the turn that asked for the video, which is the whole reason
	// this trace is worth having: the turn ends in seconds and the file arrives
	// minutes later, and a waterfall that shows only the first half explains
	// nothing about the wait the user actually experienced.
	ctx = tracing.Extract(ctx, p.Trace)
	ctx, span := tracing.Step(ctx, "report.render.threaded")
	defer span.End()
	tracing.QueueWait(span, p.EnqueuedAt)
	if p.AgentID != "" {
		// The id only, not the turn's whole scope: nothing in a render reads a
		// source or MCP allowlist, and reconstructing one from a payload would
		// be a second answer to "what could that agent reach" — able to
		// disagree with the roster row the turn itself resolved.
		ctx = agentscope.WithScope(ctx, agentscope.Scope{AgentID: p.AgentID})
	}

	if s.gen == nil {
		s.announce(ctx, p, "The video could not be rendered: document generation is not available on this deployment.")
		return nil
	}

	spec := p.Spec
	res, err := s.gen.Generate(ctx, docgen.Input{
		Spec:       &spec,
		CompanyID:  p.CompanyID,
		ThreadID:   p.ThreadID,
		Source:     domain.DocumentSourceAgent,
		Images:     p.Images,
		OnProgress: s.threadProgress(p.ThreadID),
	})
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": p.CompanyID,
			"thread_id":  p.ThreadID,
			"format":     spec.Format,
		}).Warn("threaded render failed")
		s.announceWith(ctx, p, renderFailureMessage(err), map[string]interface{}{"format": spec.Format})
		return nil
	}
	if res.Document.Format == domain.DocumentFormatCarousel {
		s.announceTo(ctx, p, carouselMessage(res), carouselChannelMessage(res, s.presignPages(ctx, p, res.Document)), map[string]interface{}{
			"format":      string(domain.DocumentFormatCarousel),
			"document_id": res.Document.ID,
			"page_count":  res.Document.PageCount,
		})
		return nil
	}
	s.announceWith(ctx, p, fmt.Sprintf("Your video is ready: [%s](%s). The link expires in about an hour; ask again and I will produce a fresh one.",
		res.Document.Filename, res.DownloadURL), map[string]interface{}{"format": string(res.Document.Format)})
	return nil
}

// carouselMessage is the assistant message a finished carousel becomes (T-G6):
// the caption in a fenced block so it copies clean, the hashtags, one image
// per slide, and the zip.
//
// **The images point at our own authenticated route, never at a presigned
// URL** (decision 6). This message is persisted, and a presigned image URL in
// it is a row of broken images on tomorrow's reload; the dashboard's markdown
// renderer fetches `/api/documents/:id/pages/:n` through its API client, with
// the bearer header an `<img src>` cannot send. The zip link is presigned, as
// every other document's is — the rule is only that an *image* in persisted
// content is never presigned.
func carouselMessage(res *docgen.Result) string {
	var b strings.Builder
	n := res.Document.PageCount
	fmt.Fprintf(&b, "Your carousel is ready — %d slides.\n\n", n)
	if caption := docgen.CaptionText(res.Carousel); caption != "" {
		b.WriteString("**Caption**\n\n```text\n")
		b.WriteString(caption)
		b.WriteString("\n```\n\n")
	}
	for i := 1; i <= n; i++ {
		alt := ""
		if res.Carousel != nil && i-1 < len(res.Carousel.Alts) {
			alt = res.Carousel.Alts[i-1]
		}
		fmt.Fprintf(&b, "![%s](/api/documents/%s/pages/%d)\n", altLabel(i, alt), res.Document.ID, i)
	}
	fmt.Fprintf(&b, "\n[Download all slides (%s)](%s) — the link expires in about an hour; ask again and I will produce a fresh one.",
		res.Document.Filename, res.DownloadURL)
	return b.String()
}

// altLabel is the markdown alt for slide i: "Slide 3 — <alt>", with the
// characters that would end the alt early removed. The alt is also what the
// renderer shows while the image loads and if it cannot.
func altLabel(i int, alt string) string {
	alt = strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(strings.TrimSpace(alt))
	if alt == "" {
		return fmt.Sprintf("Slide %d", i)
	}
	return fmt.Sprintf("Slide %d — %s", i, alt)
}

// announce writes one assistant message and publishes it.
//
// Persisted first, then published: a dashboard that is open sees it live and a
// dashboard that is not sees it on the next load, and the row is what makes
// the second true. A publish failure is logged and swallowed — the message is
// in the thread either way.
func (s *APIReportService) announce(ctx context.Context, p queue.ReportRenderPayload, content string) {
	s.announceWith(ctx, p, content, nil)
}

// announceWith is announce with extra metadata on the message — the format,
// and for a carousel the document id and page count, so a client can find the
// slides without parsing the prose (T-G6). `kind` is always render_result.
// The channel gets the same text the thread does.
func (s *APIReportService) announceWith(ctx context.Context, p queue.ReportRenderPayload, content string, extra map[string]interface{}) {
	s.announceTo(ctx, p, content, content, extra)
}

// announceTo writes `content` to the thread and sends `channelContent` to the
// channel the turn came in on, when it came in on one (T-G6, finding 6).
//
// Two texts because the two readers differ: the thread's message carries
// inline slides on the authenticated page route, which a phone cannot fetch,
// and a phone gets signed links instead. For the mp4 and every failure the
// two are the same string.
//
// The thread first, then the channel. The row is the record; a delivery
// that fails is logged and the message is still in the thread, which is the
// same order a chat turn keeps.
func (s *APIReportService) announceTo(ctx context.Context, p queue.ReportRenderPayload, content, channelContent string, extra map[string]interface{}) {
	if s.announcer == nil {
		logrus.WithField("thread_id", p.ThreadID).
			Warn("no thread announcer: a rendered document will not be announced in its thread")
		return
	}
	defer s.deliver(ctx, p, channelContent)
	metadata := map[string]interface{}{"kind": "render_result"}
	for k, v := range extra {
		metadata[k] = v
	}
	msg := &domain.Message{
		ThreadID: p.ThreadID,
		Role:     domain.MessageRoleAssistant,
		Content:  content,
		Metadata: metadata,
	}
	if err := s.announcer.Append(ctx, msg); err != nil {
		logrus.WithError(err).WithField("thread_id", p.ThreadID).
			Error("rendered video not written to its thread")
		return
	}
	if err := s.announcer.Publish(p.ThreadID, ChatEvent{
		ThreadID:  p.ThreadID,
		Type:      "final",
		Content:   content,
		Timestamp: time.Now(),
	}); err != nil {
		logrus.WithError(err).WithField("thread_id", p.ThreadID).
			Warn("rendered video written but not published; it appears on the next load")
	}
}

// deliver sends a render result to the channel the turn arrived on, through
// the provider that channel's replies use (T-G6, finding 6). It is the
// channel half of ChatRunner.completeWith, for a message that arrives after
// the turn: WhatsApp with its links flattened, Discord over the outbound bus,
// Lark as a reply to the message that asked (a new message in the chat when
// there is none), Slack into the thread.
//
// No target, or a dashboard, API or widget target, sends nothing — the
// thread message and the `final` event already reached those. A target
// naming a channel whose provider is not installed is logged at Warn, because
// that is a deployment that told a user a file was coming and cannot deliver
// it, and the log line is the only place that shows.
func (s *APIReportService) deliver(ctx context.Context, p queue.ReportRenderPayload, content string) {
	t := p.Target
	if t == nil || content == "" {
		return
	}
	fields := logrus.Fields{"thread_id": p.ThreadID, "company_id": p.CompanyID, "channel": t.Channel}
	var err error
	switch domain.Channel(t.Channel) {
	case domain.ChannelWhatsApp:
		if s.wa == nil || t.PhoneNumber == "" {
			logrus.WithFields(fields).Warn("render result not delivered: no whatsapp provider or phone number")
			return
		}
		err = s.wa.SendMessage(t.PhoneNumber, stripMarkdownLinks(content))
	case domain.ChannelDiscord:
		if s.bus == nil || t.DiscordChannelID == "" {
			logrus.WithFields(fields).Warn("render result not delivered: no outbound bus or discord channel")
			return
		}
		err = s.bus.PublishOutbound(OutboundEvent{
			Channel:    string(domain.ChannelDiscord),
			CompanyID:  p.CompanyID,
			ChannelRef: t.DiscordChannelID,
			UserRef:    t.DiscordUserID,
			Content:    content,
		})
	case domain.ChannelLark:
		switch {
		case s.lark == nil:
			logrus.WithFields(fields).Warn("render result not delivered: no lark provider")
			return
		case t.LarkMessageID != "":
			err = s.lark.Reply(ctx, p.CompanyID, t.LarkMessageID, content)
		case t.LarkChatID != "":
			err = s.lark.Send(ctx, p.CompanyID, t.LarkChatID, content)
		default:
			logrus.WithFields(fields).Warn("render result not delivered: no lark message or chat id")
			return
		}
	case domain.ChannelSlack:
		if s.slack == nil || t.SlackChannelID == "" {
			logrus.WithFields(fields).Warn("render result not delivered: no slack provider or channel")
			return
		}
		err = s.slack.Reply(ctx, p.CompanyID, t.SlackChannelID, t.SlackThreadTS, content)
	default:
		// dashboard, api, widget, or a channel this worker does not know: the
		// thread message is the delivery, exactly as completeWith treats them.
		return
	}
	if err != nil {
		logrus.WithError(err).WithFields(fields).Error("render result not delivered to its channel")
		return
	}
	logrus.WithFields(fields).Info("render result delivered to its channel")
}

// presignPages returns one signed URL per page of a carousel, for the channel
// message. Empty when there is no channel to send to — the dashboard reads
// pages through its own route and a signed URL would be paid for and unread
// — and a page whose URL cannot be signed is left out and logged, so the
// message that reaches the phone is shorter rather than absent.
func (s *APIReportService) presignPages(ctx context.Context, p queue.ReportRenderPayload, doc *domain.Document) []string {
	if s.gen == nil || doc == nil || !deliversToChannel(p.Target) {
		return nil
	}
	urls := make([]string, 0, doc.PageCount)
	for i := 1; i <= doc.PageCount; i++ {
		u, err := s.gen.PresignPage(ctx, doc, i)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"document_id": doc.ID, "page": i}).
				Warn("carousel page not presigned for channel delivery")
			continue
		}
		urls = append(urls, u)
	}
	return urls
}

// deliversToChannel reports whether a target names somewhere other than the
// thread itself.
func deliversToChannel(t *tenantctx.ReplyTarget) bool {
	if t == nil {
		return false
	}
	switch domain.Channel(t.Channel) {
	case domain.ChannelWhatsApp, domain.ChannelDiscord, domain.ChannelLark, domain.ChannelSlack:
		return true
	}
	return false
}

// carouselChannelMessage is the carousel as a channel reads it (T-G6,
// finding 6): the caption as plain text — a fence is three backticks on a
// phone — one signed link per slide, and the zip. No `![`: an inline image
// on the authenticated page route is a broken path off the dashboard, which
// is the roadmap's own acceptance line for a WhatsApp-bound thread.
//
// Links are markdown, as a chat reply's are. The WhatsApp path flattens them
// to "text: url", Slack rewrites them to mrkdwn, Discord renders them, and
// Lark shows them as the turn's own replies show them.
func carouselChannelMessage(res *docgen.Result, pages []string) string {
	var b strings.Builder
	n := res.Document.PageCount
	fmt.Fprintf(&b, "Your carousel is ready — %d slides.\n\n", n)
	if caption := docgen.CaptionText(res.Carousel); caption != "" {
		b.WriteString("Caption:\n")
		b.WriteString(caption)
		b.WriteString("\n\n")
	}
	for i, u := range pages {
		alt := ""
		if res.Carousel != nil && i < len(res.Carousel.Alts) {
			alt = res.Carousel.Alts[i]
		}
		fmt.Fprintf(&b, "[%s](%s)\n", altLabel(i+1, alt), u)
	}
	if len(pages) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "[Download all slides (%s)](%s) — the links expire in about an hour; ask again and I will produce fresh ones.",
		res.Document.Filename, res.DownloadURL)
	return b.String()
}

// threadProgress reports a threaded render's progress on the thread's own
// channel, so the dashboard that asked for the video has a number to show.
func (s *APIReportService) threadProgress(threadID string) func(float64) {
	if s.announcer == nil || threadID == "" {
		return nil
	}
	return func(f float64) {
		_ = s.announcer.Publish(threadID, ChatEvent{
			ThreadID:  threadID,
			Type:      EventRenderProgress,
			Progress:  f,
			Timestamp: time.Now(),
		})
	}
}

// progressFor returns the callback a long render reports through, or nil when
// there is no bus to report on.
//
// It publishes and forgets: a report whose progress nobody is watching is the
// normal case, and a Redis hiccup must not fail a render that is working. The
// rate cap is upstream — the render client polls once a second and only calls
// this when it does — because the cheapest way to honour "at most once per
// second" is not to generate the events.
func (s *APIReportService) progressFor(reportID string) func(float64) {
	if s.progress == nil || reportID == "" {
		return nil
	}
	return func(p float64) {
		_ = s.progress.PublishReport(reportID, ChatEvent{
			Type:      EventRenderProgress,
			Progress:  p,
			Timestamp: time.Now(),
		})
	}
}

// renderFailureMessage says which of the three failures this was.
//
// "The document could not be rendered" is true of all three and actionable for
// none: an integrator whose deployment has no render service, one whose render
// service is down, and one whose spec was refused have three different next
// steps, and `T-A5` exists because we used to hand them the same sentence.
func renderFailureMessage(err error) string {
	switch {
	case errors.Is(err, video.ErrNotConfigured):
		return "Video rendering is not configured on this deployment. Every other format is available."
	case errors.Is(err, video.ErrPlanRejected):
		// The renderer's own words: they name the cap and the observed value,
		// which is the whole reason those messages are written the way they are.
		// "rendered" rather than "rendered as a video" since T-G6: the same
		// sentinel covers a carousel, and the reason names the format itself.
		return "This spec cannot be rendered: " + unwrapMessage(err)
	case errors.Is(err, video.ErrUnavailable):
		return "The render service did not answer. Nothing was billed for the render; try again."
	}
	return "The document could not be rendered from this spec."
}

// unwrapMessage strips the sentinel prefix so a tenant reads the reason rather
// than our error chain.
func unwrapMessage(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 && i+2 < len(msg) {
		return msg[i+2:]
	}
	return msg
}

// fail marks a job failed with a message written for whoever reads it.
func (s *APIReportService) fail(ctx context.Context, reportID, msg string) {
	if err := s.reports.Complete(ctx, reportID, domain.APIReportFailed, "", msg, time.Now()); err != nil {
		logrus.WithError(err).WithField("report_id", reportID).
			Error("report job not marked failed; the caller will keep polling")
		return
	}
	// A failed render ends a stream exactly as a finished one does. The
	// handler re-reads the row either way, so what the caller gets is the
	// report object carrying the message written above rather than this
	// event's own contents.
	s.settled(reportID, "error")
	if rep, err := s.reports.Get(ctx, reportID); err == nil {
		s.notify(ctx, rep)
	}
}

// notify delivers the signed callback, if the job asked for one.
//
// A failed callback is logged and does not fail anything: the report is
// finished either way, and the two other ways to collect it — polling and the
// SSE stream — are unaffected. The delivery log is where a tenant finds out
// what happened to the third.
func (s *APIReportService) notify(ctx context.Context, rep *domain.APIReport) {
	if rep == nil || rep.CallbackURL == "" || s.sender == nil {
		return
	}
	body := map[string]any{
		"event":      EventReportCompleted,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       s.reportPayload(ctx, rep),
	}
	if _, err := s.sender.Send(ctx, rep.CompanyID, EventReportCompleted, rep.CallbackURL, body); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": rep.CompanyID,
			"report_id":  rep.ID,
		}).Warn("report callback not queued; the report itself is unaffected")
	}
}

// reportPayload is the `data` block of the callback.
//
// It carries a presigned download URL because the alternative — an id the
// receiver has to exchange for a URL — turns a one-way notification into a
// round trip against an endpoint they must authenticate to, and the whole
// point of the callback is that they do not have to poll us.
func (s *APIReportService) reportPayload(ctx context.Context, rep *domain.APIReport) map[string]any {
	data := map[string]any{
		"id":     rep.ID,
		"object": "report",
		"status": string(rep.Status),
		"format": string(rep.Format),
	}
	if rep.Error != "" {
		data["error"] = rep.Error
	}
	if rep.DocumentID == "" || s.docs == nil || s.gen == nil {
		return data
	}
	doc, err := s.docs.GetForCompany(ctx, rep.CompanyID, rep.DocumentID)
	if err != nil {
		return data
	}
	data["document"] = map[string]any{
		"id":         doc.ID,
		"filename":   doc.Filename,
		"format":     string(doc.Format),
		"size_bytes": doc.SizeBytes,
	}
	signed, expiresAt, err := s.gen.Presign(ctx, doc)
	if err != nil {
		logrus.WithError(err).WithField("document_id", doc.ID).
			Warn("callback sent without a download URL; the document itself is fine")
		return data
	}
	data["download_url"] = signed
	data["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	return data
}
