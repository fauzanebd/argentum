package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
	"github.com/fauzanebd/argentum/internal/webhookout"
)

// V1ReportsHandler is the two doors T-A2's locked decision 2 describes.
//
//	POST /v1/reports/render — a spec in, a file out. No LLM, no thread,
//	                          sub-second, priced as a render.
//	POST /v1/reports        — a prompt in, a real agent turn, seconds to
//	                          minutes, priced in tokens.
//
// They are two endpoints rather than one with a mode flag because a flag that
// changes the latency, the cost, the failure modes and the shape of the
// response is two endpoints wearing a coat — and the caller has to write two
// code paths either way.
type V1ReportsHandler struct {
	gen      *docgen.Service
	reports  domain.APIReportRepository
	docs     domain.DocumentRepository
	chat     *app.ChatEnqueuer
	enqueuer *queue.Enqueuer
	rdb      *redis.Client
	idem     idempotency.Store
	// syncRenderTimeout is how long a render may hold the connection before it
	// becomes a job.
	syncRenderTimeout time.Duration
	allowPrivateHooks bool
}

func NewV1ReportsHandler(
	gen *docgen.Service,
	reports domain.APIReportRepository,
	docs domain.DocumentRepository,
	chat *app.ChatEnqueuer,
	enqueuer *queue.Enqueuer,
	rdb *redis.Client,
	idem idempotency.Store,
	syncRenderTimeout time.Duration,
	allowPrivateHooks bool,
) *V1ReportsHandler {
	if syncRenderTimeout <= 0 {
		syncRenderTimeout = 20 * time.Second
	}
	return &V1ReportsHandler{
		gen: gen, reports: reports, docs: docs, chat: chat, enqueuer: enqueuer,
		rdb: rdb, idem: idem,
		syncRenderTimeout: syncRenderTimeout,
		allowPrivateHooks: allowPrivateHooks,
	}
}

// Register installs the routes on a group already carrying APIKeyAuth.
//
// Every route names its scope. That is the review rule T-04's policy table
// cannot enforce here — scopes are per-key rather than per-role, so there is
// no table to diff against the router — and a `/v1` route without a
// RequireScope reaches every key the tenant has ever minted.
func (h *V1ReportsHandler) Register(rg *gin.RouterGroup) {
	write := middleware.RequireScope(domain.ScopeWriteReports)
	read := middleware.RequireScope(domain.ScopeReadDocuments)

	// Idempotency is required on both doors and on neither of the reads. A GET
	// is already idempotent, and recording one would put a Redis key and a
	// 24-hour TTL behind every poll an integrator makes.
	rg.POST("/reports/render", write,
		middleware.Idempotency(h.idem,
			middleware.IdempotencyRequired(),
			middleware.IdempotencyReplayWith(h.replayRender)),
		h.render)
	rg.POST("/reports", write,
		middleware.Idempotency(h.idem,
			middleware.IdempotencyRequired(),
			middleware.IdempotencyReplayWith(h.replayReport)),
		h.createReport)
	rg.GET("/reports/:id", read, h.getReport)
	rg.GET("/reports/:id/events", read, h.streamReport)
}

// renderRequest is the render door's body: the same spec.Document the
// generate_document tool accepts, so a caller who has seen one agent-produced
// document already knows the shape. Decoded into spec.Document directly rather
// than through a wrapper — a wrapper would be a second definition of the
// contract, able to drift from the one the renderers read.

// reportResponse is the object both doors and the poll route return. One
// shape, so a caller writing the collection path writes it once whether the
// job came from a prompt or from a spec that ran long.
type reportResponse struct {
	ID     string `json:"id"`
	Object string `json:"object"`
	Status string `json:"status"`
	Kind   string `json:"kind"`
	Format string `json:"format"`
	// ThreadID lets a caller continue the conversation that produced a report
	// through `POST /v1/chat` (T-A3). Absent for a render job, which has none.
	ThreadID  string            `json:"thread_id,omitempty"`
	Document  *documentResponse `json:"document,omitempty"`
	Error     string            `json:"error,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	RequestID string            `json:"request_id,omitempty"`
}

// render is `POST /v1/reports/render`.
//
// Deterministic: a spec goes in, a file comes out, and nothing about it
// touches an LLM or a thread. `Accept: application/json` gets the document
// object with a presigned URL; the format's own content type gets the bytes
// inline, which is what a caller writing them straight to disk wants.
func (h *V1ReportsHandler) render(c *gin.Context) {
	if h.gen == nil {
		apierr.Abort(c, apierr.TypeServer, "rendering_unavailable",
			"Document rendering is not available on this deployment.")
		return
	}

	var doc spec.Document
	if err := c.ShouldBindJSON(&doc); err != nil {
		apierr.Abort(c, apierr.TypeInvalidRequest, "invalid_spec",
			"The request body is not a valid report spec: "+err.Error())
		return
	}

	company := companyID(c)
	keyID := c.GetString(middleware.CtxAPIKeyID)
	ctx := tenantctx.WithCompanyID(c.Request.Context(), company)

	// The renderer is not context-aware — maroto lays out a document without
	// ever checking for cancellation — so the deadline cannot be enforced by
	// passing a context into it. It runs on its own goroutine and this select
	// stops *waiting* at the timeout. The goroutine finishes eventually and its
	// result is dropped; the alternative is holding a connection open for
	// however long the spec takes, which is the thing the timeout exists to
	// prevent.
	type outcome struct {
		res *docgen.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := h.gen.Generate(context.WithoutCancel(ctx), docgen.Input{
			Spec:          &doc,
			CompanyID:     company,
			Source:        domain.DocumentSourceAPI,
			APIKeyID:      keyID,
			EnforceLimits: true,
		})
		done <- outcome{res, err}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			h.abortRender(c, out.err)
			return
		}
		middleware.DeclareIdempotentResult(c, map[string]string{
			"document_id": out.res.Document.ID,
			"status":      "completed",
		})
		h.writeRendered(c, out.res.Document, out.res.Data, out.res.DownloadURL, out.res.ExpiresAt)
	case <-time.After(h.syncRenderTimeout):
		h.renderAsync(c, company, keyID, doc)
	}
}

// abortRender maps a generation failure onto the envelope.
//
// A spec over a cap is the one failure with a field to name, and naming it is
// the difference between an integrator fixing their request and an integrator
// opening a ticket. Everything else the renderers reject is a malformed spec —
// a chart with three values against five labels, an unknown section type — and
// those are the caller's to fix too, so they are `invalid_request` rather than
// a 500 that invites a retry of something that cannot succeed.
func (h *V1ReportsHandler) abortRender(c *gin.Context, err error) {
	if le, ok := docgen.AsLimitError(err); ok {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "spec_too_large", le.Error(), le.Param)
		return
	}
	if errors.Is(err, domain.ErrInvalidInput) || isSpecError(err) {
		apierr.Abort(c, apierr.TypeInvalidRequest, "invalid_spec", err.Error())
		return
	}
	logrus.WithError(err).WithField("company_id", companyID(c)).Error("report render failed")
	apierr.Abort(c, apierr.TypeServer, "render_failed",
		"The document could not be rendered. The request id below identifies this attempt.")
}

// isSpecError reports whether an error came from spec validation rather than
// from infrastructure. Validate returns bare fmt.Errorf values — it predates
// this route by four tickets and is called by the agent, where an error string
// is a repair instruction rather than an HTTP class — so the classification
// has to be made here rather than by unwrapping a sentinel that does not
// exist. Storage and database failures are wrapped with an operation name
// ("upload document:", "persist document:"), which is what this excludes.
func isSpecError(err error) bool {
	msg := err.Error()
	for _, marker := range []string{"upload document:", "persist document:", "presign document:"} {
		if strings.Contains(msg, marker) {
			return false
		}
	}
	return true
}

// renderAsync converts an overrunning render into a job.
//
// It reuses the same `api_reports` row and the same response shape the agentic
// door returns, deliberately: an integrator should write one collection path
// for `/v1/reports*`, not one per endpoint. The work already done on the
// abandoned goroutine is thrown away and redone by the worker — a real cost,
// paid only by specs that are pathological to begin with, and the alternative
// is handing a background goroutine a channel that nothing is left to read.
func (h *V1ReportsHandler) renderAsync(c *gin.Context, company, keyID string, doc spec.Document) {
	if h.enqueuer == nil || h.reports == nil {
		apierr.Abort(c, apierr.TypeServer, "render_timeout",
			"This spec took longer than the synchronous window and asynchronous rendering is not configured here.")
		return
	}
	rep := &domain.APIReport{
		CompanyID: company,
		APIKeyID:  keyID,
		Kind:      domain.APIReportRender,
		Status:    domain.APIReportQueued,
		Format:    domain.DocumentFormat(spec.FormatOf(&doc)),
		RequestID: requestIDOf(c),
	}
	if err := h.reports.Create(c.Request.Context(), rep); err != nil {
		logrus.WithError(err).Error("create render job")
		apierr.Abort(c, apierr.TypeServer, "render_failed", "The render job could not be created.")
		return
	}
	if _, err := h.enqueuer.EnqueueReportRender(c.Request.Context(), queue.ReportRenderPayload{
		ReportID:  rep.ID,
		CompanyID: company,
		APIKeyID:  keyID,
		RequestID: rep.RequestID,
		Spec:      doc,
	}); err != nil {
		logrus.WithError(err).Error("enqueue render job")
		apierr.Abort(c, apierr.TypeServer, "render_failed", "The render job could not be queued.")
		return
	}
	middleware.DeclareIdempotentResult(c, map[string]string{
		"report_id": rep.ID,
		"status":    string(domain.APIReportQueued),
	})
	c.JSON(http.StatusAccepted, h.reportBody(c, rep))
}

// writeRendered answers with either the document object or the bytes,
// according to Accept.
//
// The bytes path exists because the common caller is a server writing a file
// to disk, and making it follow a presigned URL to do that is a second round
// trip plus an egress path through object storage for something already in
// memory. The JSON path exists because the other common caller wants to hand
// the URL to a browser.
func (h *V1ReportsHandler) writeRendered(c *gin.Context, doc *domain.Document, data []byte, url string, expiresAt time.Time) {
	if wantsBytes(c, doc.Format) {
		// The filename is quoted and already sanitised by docgen — a raw
		// caller-supplied name in this header is a response-splitting bug and
		// a download-to-anywhere bug at the same time.
		c.Header("Content-Disposition", `attachment; filename="`+doc.Filename+`"`)
		c.Header("X-Document-Id", doc.ID)
		c.Data(http.StatusOK, doc.Format.ContentType(), data)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"object":     "document",
		"document":   documentBody(doc, url, expiresAt),
		"request_id": requestIDOf(c),
	})
}

// wantsBytes reads Accept. An absent or wildcard Accept means JSON: a caller
// that did not say is a caller reading a response body in a REPL, and handing
// them a megabyte of PDF is the less useful default.
func wantsBytes(c *gin.Context, format domain.DocumentFormat) bool {
	accept := strings.ToLower(c.GetHeader("Accept"))
	if accept == "" || strings.Contains(accept, "application/json") {
		return false
	}
	ct := format.ContentType()
	if i := strings.Index(ct, ";"); i > 0 {
		ct = ct[:i]
	}
	return strings.Contains(accept, ct) || strings.Contains(accept, "application/octet-stream")
}

// replayRender answers a replayed `POST /v1/reports/render`.
//
// This is why the idempotency middleware takes a Replayer at all. The stored
// record holds a document id and nothing else — never the payload, which for
// a 10 MB PDF would turn Redis into a document store — so a replay has to
// re-read the row and **re-presign**. That is also the only way a replayed
// download link is still valid an hour after the original call.
func (h *V1ReportsHandler) replayRender(c *gin.Context, rec *idempotency.Record) bool {
	var stored struct {
		DocumentID string `json:"document_id"`
		ReportID   string `json:"report_id"`
	}
	if err := json.Unmarshal(rec.Result, &stored); err != nil {
		return false
	}
	if stored.ReportID != "" {
		// The original overran and became a job. Replay its current state
		// rather than the 202 it returned: a caller retrying five minutes later
		// should be told the report is ready, not told again that it is queued.
		rep, err := h.reports.GetForCompany(c.Request.Context(), companyID(c), stored.ReportID)
		if err != nil {
			return false
		}
		c.JSON(http.StatusAccepted, h.reportBody(c, rep))
		return true
	}
	if stored.DocumentID == "" || h.docs == nil || h.gen == nil {
		return false
	}
	doc, err := h.docs.GetForCompany(c.Request.Context(), companyID(c), stored.DocumentID)
	if err != nil {
		return false
	}
	url, expiresAt, err := h.gen.Presign(c.Request.Context(), doc)
	if err != nil {
		return false
	}
	// The bytes are not replayed even when the original returned them: they
	// are not in the record, and re-rendering would bill a second document.
	// A replay of a bytes request gets the object with a fresh URL, and the
	// `Idempotent-Replay: true` header the middleware already set is what says
	// why the shape changed.
	c.JSON(http.StatusOK, gin.H{
		"object":     "document",
		"document":   documentBody(doc, url, expiresAt),
		"request_id": requestIDOf(c),
	})
	return true
}

// replayReport answers a replayed `POST /v1/reports`.
//
// Without it the middleware's default replay writes the stored record verbatim
// — `{"report_id":"…","status":"queued"}` — which is a *different shape* from
// the report object the original call returned, on a published contract. The
// live gate found it: a retry of an accepted request came back 202 with a body
// no client could parse with the code it wrote for the first one.
//
// Re-reading the row also makes the replay honest about time. A caller
// retrying five minutes later is told the report is ready, rather than handed
// the `queued` the original answered with.
func (h *V1ReportsHandler) replayReport(c *gin.Context, rec *idempotency.Record) bool {
	var stored struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(rec.Result, &stored); err != nil || stored.ReportID == "" {
		return false
	}
	rep, err := h.reports.GetForCompany(c.Request.Context(), companyID(c), stored.ReportID)
	if err != nil {
		return false
	}
	c.JSON(http.StatusAccepted, h.reportBody(c, rep))
	return true
}

// createReportRequest is the agentic door's body.
type createReportRequest struct {
	Prompt string `json:"prompt"`
	Format string `json:"format"`
	// UserRef is the tenant's own identifier for the person this is on behalf
	// of. It is what makes the turn attributable in `usage/by-user`, which is
	// the report a tenant reads to police their own integration.
	UserRef  string `json:"user_ref,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	// CallbackURL receives the signed `report.completed` body. Optional: most
	// callers poll or stream.
	CallbackURL string `json:"callback_url,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

// createReport is `POST /v1/reports` — a prompt, a real turn, a document at
// the end of it.
func (h *V1ReportsHandler) createReport(c *gin.Context) {
	if h.chat == nil || h.reports == nil {
		apierr.Abort(c, apierr.TypeServer, "reports_unavailable",
			"Agentic reports are not available on this deployment.")
		return
	}
	var req createReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.Abort(c, apierr.TypeInvalidRequest, "invalid_request", "The request body is not valid JSON.")
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "prompt_required",
			"Send a `prompt` describing the report you want.", "prompt")
		return
	}
	format := domain.DocumentFormat(strings.ToLower(strings.TrimSpace(req.Format)))
	if format == "" {
		format = domain.DocumentFormatPDF
	}
	if !format.Valid() {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_format",
			"`format` must be one of pdf, pptx, xlsx, csv.", "format")
		return
	}
	if req.UserRef == "" && req.ThreadID == "" {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "user_ref_required",
			"Send a `user_ref` identifying who this report is for, or a `thread_id` to continue an existing conversation.", "user_ref")
		return
	}
	if req.CallbackURL != "" {
		// Validated here so the caller gets a 400 they can act on, and again in
		// the worker because DNS can change between the two.
		if err := webhookout.CheckTarget(req.CallbackURL, h.allowPrivateHooks); err != nil {
			apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_callback_url", err.Error(), "callback_url")
			return
		}
	}

	company := companyID(c)
	rep := &domain.APIReport{
		CompanyID:   company,
		APIKeyID:    c.GetString(middleware.CtxAPIKeyID),
		Kind:        domain.APIReportAgentic,
		Status:      domain.APIReportQueued,
		Format:      format,
		Prompt:      req.Prompt,
		CallbackURL: req.CallbackURL,
		RequestID:   requestIDOf(c),
	}
	if err := h.reports.Create(c.Request.Context(), rep); err != nil {
		logrus.WithError(err).Error("create report job")
		apierr.Abort(c, apierr.TypeServer, "report_failed", "The report job could not be created.")
		return
	}
	// Written through to Redis immediately, so a retry arriving while this turn
	// is still running gets a 409 naming the report it is already waiting on
	// rather than starting a second billed turn.
	middleware.DeclareIdempotentProgress(c, h.idem, map[string]string{
		"report_id": rep.ID,
		"status":    string(domain.APIReportQueued),
	})

	res, err := h.chat.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:     domain.ChannelAPI,
		CompanyID:   company,
		APIUserRef:  req.UserRef,
		ThreadID:    req.ThreadID,
		Message:     reportDirective(req, format),
		APIReportID: rep.ID,
		APIKeyID:    rep.APIKeyID,
	})
	if err != nil {
		h.abortEnqueue(c, rep, err)
		return
	}
	rep.ThreadID = res.Thread.ID
	// Persisted, not merely returned. The live gate found what happens
	// otherwise: the row keeps a null thread, so `GET /v1/reports/:id` omits
	// it and the SSE bridge has no channel to subscribe to — it closes
	// immediately on the one operation long enough to be worth streaming.
	// A failure here is logged rather than fatal: the turn is already running
	// and the caller can still poll.
	if err := h.reports.AttachThread(c.Request.Context(), rep.ID, rep.ThreadID); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"report_id": rep.ID,
			"thread_id": rep.ThreadID,
		}).Warn("report job did not record its thread; its event stream will not open")
	}

	middleware.DeclareIdempotentResult(c, map[string]string{
		"report_id": rep.ID,
		"status":    string(domain.APIReportQueued),
	})
	c.JSON(http.StatusAccepted, h.reportBody(c, rep))
}

// abortEnqueue maps a refused turn onto the envelope and closes the job.
//
// A budget refusal is a typed 402 rather than a 500 on purpose: a programmatic
// caller retries a 500, and retrying a turn the tenant cannot pay for produces
// a loop that stops only when someone notices.
func (h *V1ReportsHandler) abortEnqueue(c *gin.Context, rep *domain.APIReport, err error) {
	_ = h.reports.Complete(c.Request.Context(), rep.ID, domain.APIReportFailed, "",
		"The report could not be started.", time.Now())

	switch {
	case errors.Is(err, domain.ErrInsufficientCredits):
		apierr.Abort(c, apierr.TypeBudgetExhausted, "credits_exhausted", app.CreditsExhaustedMessage)
	case errors.Is(err, domain.ErrInvalidInput):
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_thread",
			"That `thread_id` is not an API thread for this company.", "thread_id")
	case errors.Is(err, domain.ErrNotFound):
		apierr.AbortParam(c, apierr.TypeNotFound, "thread_not_found",
			"No such thread for this company.", "thread_id")
	default:
		logrus.WithError(err).WithField("company_id", rep.CompanyID).Error("enqueue report turn")
		apierr.Abort(c, apierr.TypeServer, "report_failed", "The report turn could not be started.")
	}
}

// reportDirective is the message the agent actually receives.
//
// The caller's prompt with an instruction appended, rather than a system-prompt
// change: the directive applies to this turn and to no other, and a caller
// asking "what were sales last month?" through `/v1/chat` must not get a PDF
// because a sibling endpoint wanted one.
//
// **The negative half is the half that works.** The live gate's first agentic
// run answered a prompt containing the words "bar chart" by calling
// `create_visualization` twice and finishing without a document — obeying the
// system prompt, which teaches that a chart is a Metabase card, over a
// directive that only said what to do at the end. A chart *inside* a report is
// a `chart` section in the spec, and the agent has to be told that the other
// tool is not what this turn wants.
func reportDirective(req createReportRequest, format domain.DocumentFormat) string {
	var b strings.Builder

	// The directive goes **before** the request, not after it. The live gate's
	// second agentic run had it appended, and the model answered by writing the
	// `generate_document` arguments into its prose as a fenced JSON block —
	// narrating the call instead of making it. An instruction that arrives
	// after the question reads as commentary on the answer; one that arrives
	// first reads as the task.
	b.WriteString("[REPORT REQUEST — the deliverable is a file, not a chat reply]\n")
	b.WriteString("You MUST end this turn by actually invoking the generate_document tool with format=")
	b.WriteString(string(format))
	if format == domain.DocumentFormatPDF || format == domain.DocumentFormatPPTX {
		b.WriteString(" and spec_version=2")
	}
	b.WriteString(".\n")
	// Named failure modes, because each of these is something a model did on a
	// real run of this endpoint rather than something imagined here.
	b.WriteString("Invoke the tool. Do not print its arguments as JSON in your reply — a code block is not a document and the caller receives no file.\n")
	b.WriteString("Do not call create_visualization or create_dashboard: a chart in this report is a \"chart\" section inside the generate_document spec, not a Metabase card.\n")
	b.WriteString("Query only what you need first; the document is the last thing you do.\n")
	if req.Locale != "" {
		b.WriteString("Use locale=" + req.Locale + ".\n")
	}
	if req.Currency != "" {
		b.WriteString("Use currency=" + req.Currency + ".\n")
	}

	b.WriteString("\nThe report to produce:\n")
	b.WriteString(req.Prompt)
	return b.String()
}

// getReport is `GET /v1/reports/:id`.
func (h *V1ReportsHandler) getReport(c *gin.Context) {
	rep, ok := h.loadReport(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, h.reportBody(c, rep))
}

// streamReport is `GET /v1/reports/:id/events` — SSE progress for a job that
// can take minutes.
//
// The SSE bridge for the report endpoint ships here rather than in T-A3
// because T-A3 may land after this ticket, and a flagship that cannot stream
// progress on a two-minute operation is a flagship people poll in a `while`
// loop.
//
// SSE rather than a WebSocket, per locked decision 3: the consumer is a
// server, every HTTP library and proxy handles SSE, and a WebSocket client in
// a backend is an extra dependency plus a reconnect state machine the
// integrator has to write. This is a second reader of the same Redis pubsub
// the dashboard's WebSocket uses, not a second event pipeline.
func (h *V1ReportsHandler) streamReport(c *gin.Context) {
	rep, ok := h.loadReport(c)
	if !ok {
		return
	}
	if h.rdb == nil {
		apierr.Abort(c, apierr.TypeServer, "streaming_unavailable",
			"Event streaming is not available on this deployment.")
		return
	}

	sseStart(c)

	// A job that finished before the caller connected gets its terminal event
	// and nothing else. Checked before subscribing: the events it would have
	// streamed are already published and gone, and a subscriber to a finished
	// turn waits for a message that will never come.
	if rep.Status.Terminal() {
		sseEvent(c, "", "report", h.reportBody(c, rep))
		return
	}
	if rep.ThreadID == "" {
		// A queued render job publishes nothing on a thread channel — it has no
		// thread. Poll is the collection path for those, and saying so beats
		// holding a connection open on a stream that cannot produce an event.
		sseEvent(c, "", "report", h.reportBody(c, rep))
		return
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	pubsub := h.rdb.Subscribe(ctx, eventbus.ChannelFor(rep.ThreadID))
	defer func() { _ = pubsub.Close() }()
	if _, err := pubsub.Receive(ctx); err != nil {
		logrus.WithError(err).Warn("report SSE subscribe failed")
		sseEvent(c, "", "error", gin.H{"message": "The event stream could not be opened. Poll the report instead."})
		return
	}

	// Re-read after subscribing. The turn can finish in the window between the
	// terminal check above and the SUBSCRIBE, and a caller who connected one
	// millisecond too late would otherwise wait for a `final` that was
	// published while they were still setting up.
	if fresh, err := h.reports.GetForCompany(ctx, companyID(c), rep.ID); err == nil && fresh.Status.Terminal() {
		sseEvent(c, "", "report", h.reportBody(c, fresh))
		return
	}

	msgCh := pubsub.Channel()
	// A heartbeat, because an idle SSE connection through a load balancer with
	// a 60-second idle timeout is a connection that dies silently mid-report.
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sseComment(c, "heartbeat") {
				return
			}
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			var evt app.ChatEvent
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				continue
			}
			// The token deltas are not forwarded. A report's consumer wants
			// progress and a file, and streaming the prose of an answer they
			// asked for as a PDF is bandwidth spent on something nobody reads.
			switch evt.Type {
			case "started", "tool_call", "tool_result", "thinking":
				if !sseEvent(c, "", "progress", progressBody(&evt)) {
					return
				}
			case "final", "error":
				// The report row is already terminal here: ChatRunner completes
				// it before publishing `final`, which is what lets this be a
				// forwarder rather than a poll loop.
				fresh, err := h.reports.GetForCompany(ctx, companyID(c), rep.ID)
				if err != nil {
					fresh = rep
				}
				sseEvent(c, "", "report", h.reportBody(c, fresh))
				return
			}
		}
	}
}

// progressBody is the trimmed event a report's consumer gets: what is
// happening, not what the agent is saying.
func progressBody(evt *app.ChatEvent) gin.H {
	body := gin.H{"type": evt.Type, "at": evt.Timestamp.UTC().Format(time.RFC3339)}
	if evt.ToolCall != nil {
		body["tool"] = evt.ToolCall.Name
	}
	if evt.ThinkingStep != "" {
		body["step"] = evt.ThinkingStep
	}
	return body
}

// loadReport resolves `:id` inside the tenant boundary, writing the envelope
// and returning false when it cannot.
func (h *V1ReportsHandler) loadReport(c *gin.Context) (*domain.APIReport, bool) {
	if h.reports == nil {
		apierr.Abort(c, apierr.TypeServer, "reports_unavailable",
			"Reports are not available on this deployment.")
		return nil, false
	}
	rep, err := h.reports.GetForCompany(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		// Including a malformed uuid, which Postgres refuses with a cast error
		// rather than an empty result. A caller that sent a bad id and a caller
		// that sent another tenant's id learn the same thing, which is the
		// point.
		apierr.Abort(c, apierr.TypeNotFound, "report_not_found", "No such report for this company.")
		return nil, false
	}
	return rep, true
}

// reportBody renders the response, attaching the document and a fresh
// presigned URL once there is one.
func (h *V1ReportsHandler) reportBody(c *gin.Context, rep *domain.APIReport) reportResponse {
	out := reportResponse{
		ID:        rep.ID,
		Object:    "report",
		Status:    string(rep.Status),
		Kind:      string(rep.Kind),
		Format:    string(rep.Format),
		ThreadID:  rep.ThreadID,
		Error:     rep.Error,
		CreatedAt: rep.CreatedAt,
		RequestID: requestIDOf(c),
	}
	if rep.DocumentID == "" || h.docs == nil || h.gen == nil {
		return out
	}
	doc, err := h.docs.GetForCompany(c.Request.Context(), rep.CompanyID, rep.DocumentID)
	if err != nil {
		return out
	}
	// Re-presigned on every read, never stored: a URL saved on the row expires
	// and an integrator holding it would have to regenerate the document to get
	// a working one.
	url, expiresAt, err := h.gen.Presign(c.Request.Context(), doc)
	if err != nil {
		logrus.WithError(err).WithField("document_id", doc.ID).Warn("presign failed; returning the document without a URL")
		body := documentBody(doc, "", time.Time{})
		out.Document = &body
		return out
	}
	body := documentBody(doc, url, expiresAt)
	out.Document = &body
	return out
}

// requestIDOf reads the id the RequestID middleware assigned. Every `/v1`
// response carries it, so a support conversation starts from a string the
// caller already has.
func requestIDOf(c *gin.Context) string { return c.GetString("request_id") }
