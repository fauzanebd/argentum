package app

import (
	"context"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

type fakeReports struct {
	rows map[string]*domain.APIReport
}

func newFakeReports(rows ...*domain.APIReport) *fakeReports {
	f := &fakeReports{rows: map[string]*domain.APIReport{}}
	for _, r := range rows {
		f.rows[r.ID] = r
	}
	return f
}

func (f *fakeReports) Create(_ context.Context, r *domain.APIReport) error {
	f.rows[r.ID] = r
	return nil
}

func (f *fakeReports) GetForCompany(_ context.Context, companyID, id string) (*domain.APIReport, error) {
	r, ok := f.rows[id]
	if !ok || r.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeReports) Get(_ context.Context, id string) (*domain.APIReport, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeReports) AttachThread(_ context.Context, id, threadID string) error {
	if r, ok := f.rows[id]; ok && r.ThreadID == "" {
		r.ThreadID = threadID
	}
	return nil
}

func (f *fakeReports) MarkRunning(_ context.Context, id string) error {
	if r, ok := f.rows[id]; ok && r.Status == domain.APIReportQueued {
		r.Status = domain.APIReportRunning
	}
	return nil
}

func (f *fakeReports) Complete(_ context.Context, id string, status domain.APIReportStatus, documentID, errMsg string, at time.Time) error {
	r, ok := f.rows[id]
	if !ok {
		return domain.ErrNotFound
	}
	// The repository's own WHERE clause refuses to move a terminal row; the
	// fake has to as well, or a test would pass against behaviour production
	// does not have.
	if r.Status.Terminal() {
		return nil
	}
	r.Status = status
	r.DocumentID = documentID
	r.Error = errMsg
	t := at
	r.CompletedAt = &t
	return nil
}

// fakeDocLookup answers NewestForThreadSince from a fixed list, applying the
// same `since` bound the real query does.
type fakeDocLookup struct{ docs []*domain.Document }

func (f *fakeDocLookup) Insert(context.Context, *domain.Document) error { return nil }
func (f *fakeDocLookup) GetByID(context.Context, string) (*domain.Document, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeDocLookup) GetForCompany(_ context.Context, companyID, id string) (*domain.Document, error) {
	for _, d := range f.docs {
		if d.ID == id && d.CompanyID == companyID {
			return d, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (f *fakeDocLookup) ListByCompany(context.Context, string, domain.DocumentFilter) ([]*domain.Document, bool, error) {
	return nil, false, nil
}
func (f *fakeDocLookup) ListByThread(context.Context, string) ([]*domain.Document, error) {
	return nil, nil
}

var reportStart = time.Unix(1_800_000_000, 0)

func queuedReport() *domain.APIReport {
	return &domain.APIReport{
		ID: "rep-1", CompanyID: "co-1", Kind: domain.APIReportAgentic,
		Status: domain.APIReportQueued, Format: domain.DocumentFormatPDF,
		ThreadID: "th-1", CreatedAt: reportStart,
	}
}

func TestCompleteReportAttachesTheTurnsDocument(t *testing.T) {
	reports := newFakeReports(queuedReport())
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-new", CompanyID: "co-1", ThreadID: "th-1", CreatedAt: reportStart.Add(30 * time.Second),
	}}}

	NewAPIReportService(reports, docs, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", "doc-new", nil)

	got := reports.rows["rep-1"]
	if got.Status != domain.APIReportCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.DocumentID != "doc-new" {
		t.Errorf("document_id = %q, want doc-new", got.DocumentID)
	}
	if got.CompletedAt == nil {
		t.Error("completed_at was not set")
	}
}

// A turn that answered in prose without calling generate_document must not
// inherit another turn's document — the caller would download a file answering
// a question they did not ask, and would have no way to tell.
//
// It used to be enforced by a `since` bound on a query. It is now enforced by
// there being no query: the turn reports the id it produced, and this one
// produced none.
func TestCompleteReportIgnoresAnEarlierTurnsDocument(t *testing.T) {
	reports := newFakeReports(queuedReport())
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-old", CompanyID: "co-1", ThreadID: "th-1", CreatedAt: reportStart.Add(-time.Hour),
	}}}

	NewAPIReportService(reports, docs, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", "", nil)

	got := reports.rows["rep-1"]
	// Completed, not failed: the agent was asked for a report and answered.
	// That is a real outcome, and calling it a failure tells an integrator to
	// retry something that will do the same thing again.
	if got.Status != domain.APIReportCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.DocumentID != "" {
		t.Errorf("document_id = %q — a document from before this turn was attached", got.DocumentID)
	}
}

// Cross-tenant: an id naming another company's document is refused even though
// it arrived from the turn. The id is ours and the turn is ours, but this row is
// what a caller downloads from, and one confused id here is a cross-tenant file
// read — so ownership is checked rather than trusted.
func TestCompleteReportIsCompanyScoped(t *testing.T) {
	reports := newFakeReports(queuedReport())
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-other", CompanyID: "co-2", ThreadID: "th-1", CreatedAt: reportStart.Add(time.Second),
	}}}

	NewAPIReportService(reports, docs, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", "doc-other", nil)

	if got := reports.rows["rep-1"].DocumentID; got != "" {
		t.Errorf("document_id = %q — another tenant's document was attached", got)
	}
}

func TestCompleteReportRecordsAFailure(t *testing.T) {
	reports := newFakeReports(queuedReport())
	NewAPIReportService(reports, &fakeDocLookup{}, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", "", context.DeadlineExceeded)

	got := reports.rows["rep-1"]
	if got.Status != domain.APIReportFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.Error == "" {
		t.Error("a failed report carries no message for the integrator")
	}
	// Never the wrapped Go error: a chain naming our packages tells an
	// integrator nothing they can act on.
	if got.Error == context.DeadlineExceeded.Error() {
		t.Error("the raw Go error was handed to the caller")
	}
}

// asynq can re-run a handler whose acknowledgement was lost. Completing twice
// must not move a terminal row, because the callback fires off the transition
// and a tenant receiving two `report.completed` for one report has to
// deduplicate something we could have deduplicated first.
func TestCompleteReportIsIdempotent(t *testing.T) {
	reports := newFakeReports(queuedReport())
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-new", CompanyID: "co-1", ThreadID: "th-1", CreatedAt: reportStart.Add(time.Second),
	}}}
	svc := NewAPIReportService(reports, docs, nil, nil)

	svc.CompleteReport(context.Background(), "rep-1", "th-1", "doc-new", nil)
	svc.CompleteReport(context.Background(), "rep-1", "th-1", "", context.DeadlineExceeded)

	got := reports.rows["rep-1"]
	if got.Status != domain.APIReportCompleted || got.DocumentID != "doc-new" {
		t.Errorf("a second completion overwrote a terminal report: %+v", got)
	}
}

// A report id that is not there is a warning, not a panic and not a hang. The
// caller keeps polling and sees the truth, which is that nothing finished.
func TestCompleteReportToleratesAMissingRow(t *testing.T) {
	NewAPIReportService(newFakeReports(), &fakeDocLookup{}, nil, nil).
		CompleteReport(context.Background(), "rep-missing", "th-1", "", nil)
}

// ctxAwareReports fails every call once its context is done, which is what a
// real driver does and what the plain fake above does not. Without this the
// regression below cannot be written: every other test in this file would pass
// against a service that ignores cancellation entirely.
type ctxAwareReports struct{ *fakeReports }

func (f ctxAwareReports) Get(ctx context.Context, id string) (*domain.APIReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.fakeReports.Get(ctx, id)
}

func (f ctxAwareReports) Complete(ctx context.Context, id string, status domain.APIReportStatus, documentID, errMsg string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.fakeReports.Complete(ctx, id, status, documentID, errMsg, at)
}

// The turn's context is dead by the time this runs, and that is the normal
// case rather than the exotic one: a turn that overran its deadline is exactly
// the turn whose report has to be marked failed.
//
// `T-A2b`'s gate on 2026-08-13 found three of eight reports sitting at `queued`
// with an empty `error` column for this reason — `chat:run` failed with
// `context deadline exceeded`, and every read and write in here was on that
// same context, so the row never moved and the caller polled a report that
// would never finish. The symptom is the one `T-A2b` exists to prevent, from a
// different cause.
func TestCompleteReportWritesTheTerminalStatusOnADeadContext(t *testing.T) {
	reports := ctxAwareReports{newFakeReports(queuedReport())}

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	NewAPIReportService(reports, &fakeDocLookup{}, nil, nil).
		CompleteReport(dead, "rep-1", "th-1", "", context.DeadlineExceeded)

	got := reports.rows["rep-1"]
	if got.Status != domain.APIReportFailed {
		t.Fatalf("status = %q, want failed — the caller is polling a report that will never finish", got.Status)
	}
	if got.Error == "" {
		t.Error("a failed report carries no message for the integrator")
	}
	if got.CompletedAt == nil {
		t.Error("completed_at was not set")
	}
}

// A nil service is what a stack with no `/v1` report routes installs, and
// ChatRunner calls through it on every turn that carries a report id.
func TestCompleteReportOnANilServiceIsANoOp(t *testing.T) {
	var svc *APIReportService
	svc.CompleteReport(context.Background(), "rep-1", "th-1", "", nil)
}

// recordingBus is the report channel, plus the one property the SSE bridge
// depends on: what the row said at the moment each event was published.
type recordingBus struct {
	reports *fakeReports
	events  []ChatEvent
	// statusAt is the report's status as the repository held it when the event
	// went out, which is what makes the ordering assertion real rather than a
	// re-reading of the same variable the test set.
	statusAt []domain.APIReportStatus
}

func (b *recordingBus) PublishReport(reportID string, evt ChatEvent) error {
	b.events = append(b.events, evt)
	var st domain.APIReportStatus
	if r, err := b.reports.Get(context.Background(), reportID); err == nil {
		st = r.Status
	}
	b.statusAt = append(b.statusAt, st)
	return nil
}

func queuedRenderJob() *domain.APIReport {
	return &domain.APIReport{
		ID: "rep-r1", CompanyID: "co-1", Kind: domain.APIReportRender,
		Status: domain.APIReportQueued, Format: domain.DocumentFormatMP4,
		CreatedAt: reportStart,
	}
}

// The defect the 2026-08-09 live gate found: `GET /v1/reports/:id/events`
// forwards progress and closes on `final`/`error`, and a threadless render job
// published neither — so a caller following the collection path the 202 names
// watched progress reach 0.94 and then heartbeat forever against a report that
// had been terminal for ten minutes. A threaded job never had the bug, because
// ChatRunner publishes `final` on the thread's channel.
func TestAFailedRenderJobEndsItsStream(t *testing.T) {
	reports := newFakeReports(queuedRenderJob())
	bus := &recordingBus{reports: reports}
	// gen nil is the "no object storage" deployment, and the one failure branch
	// reachable without a renderer. Every other failure lands in the same fail().
	svc := NewAPIReportService(reports, &fakeDocLookup{}, nil, nil).WithProgress(bus)

	if err := svc.RunRenderJob(context.Background(), queue.ReportRenderPayload{
		ReportID: "rep-r1", CompanyID: "co-1",
	}); err != nil {
		t.Fatalf("RunRenderJob: %v", err)
	}

	if len(bus.events) != 1 {
		t.Fatalf("want exactly one terminal event, got %d: %+v", len(bus.events), bus.events)
	}
	if bus.events[0].Type != "error" {
		t.Errorf("a failed job must end the stream with `error`, got %q", bus.events[0].Type)
	}
	// Published after the row is terminal, never before. The handler answers a
	// terminal event by re-reading the row, so an event that outran the UPDATE
	// would hand the caller a report still reading `running`.
	if !bus.statusAt[0].Terminal() {
		t.Errorf("the terminal event went out while the row still read %q", bus.statusAt[0])
	}
}

// The API process builds one of these to read through and installs no bus.
// Nothing about a render may depend on somebody listening to it.
func TestARenderJobWithNoBusStillFinishes(t *testing.T) {
	reports := newFakeReports(queuedRenderJob())
	svc := NewAPIReportService(reports, &fakeDocLookup{}, nil, nil)

	if err := svc.RunRenderJob(context.Background(), queue.ReportRenderPayload{
		ReportID: "rep-r1", CompanyID: "co-1",
	}); err != nil {
		t.Fatalf("RunRenderJob: %v", err)
	}
	got, _ := reports.Get(context.Background(), "rep-r1")
	if got.Status != domain.APIReportFailed {
		t.Errorf("want the row failed with no bus installed, got %q", got.Status)
	}
}

// The defect the 2026-08-13 gate found, as a test (api-reports.md §7a).
//
// Ten calls sharing one thread: a report timed out generating nothing and was
// completed carrying a document created nine minutes later by a *different*
// request. The old lookup bounded documents by the report's created_at on one
// side only — it excluded older ones and said nothing about newer — so in a
// shared thread a slow report collected a later report's file. Every prompt was
// identical there, so the content was harmless; with two different prompts the
// caller downloads the answer to somebody else's question.
func TestCompleteReportDoesNotCollectALaterReportsDocument(t *testing.T) {
	reports := newFakeReports(queuedReport())
	// A document created nine minutes after this report, by another request on
	// the same thread. The old query would have found exactly this row.
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-somebody-elses", CompanyID: "co-1", ThreadID: "th-1",
		CreatedAt: reportStart.Add(9 * time.Minute),
	}}}

	// This turn generated nothing, and says so.
	NewAPIReportService(reports, docs, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", "", nil)

	if got := reports.rows["rep-1"].DocumentID; got != "" {
		t.Errorf("document_id = %q — a later request's file was attached to this report", got)
	}
}
