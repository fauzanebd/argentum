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
func (f *fakeDocLookup) NewestForThreadSince(_ context.Context, companyID, threadID string, since time.Time) (*domain.Document, error) {
	var newest *domain.Document
	for _, d := range f.docs {
		if d.CompanyID != companyID || d.ThreadID != threadID || d.CreatedAt.Before(since) {
			continue
		}
		if newest == nil || d.CreatedAt.After(newest.CreatedAt) {
			newest = d
		}
	}
	if newest == nil {
		return nil, domain.ErrNotFound
	}
	return newest, nil
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
		CompleteReport(context.Background(), "rep-1", "th-1", nil)

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

// The bound that matters. A turn that answered in prose without calling
// generate_document must not inherit the previous turn's document — the caller
// would download a file answering a question they did not ask, and would have
// no way to tell.
func TestCompleteReportIgnoresAnEarlierTurnsDocument(t *testing.T) {
	reports := newFakeReports(queuedReport())
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-old", CompanyID: "co-1", ThreadID: "th-1", CreatedAt: reportStart.Add(-time.Hour),
	}}}

	NewAPIReportService(reports, docs, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", nil)

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

// Cross-tenant: a document on the same thread id belonging to another company
// is not this report's. The bound is on company as well as thread because the
// query is, and a test that only covered the thread would let a repository
// regression through.
func TestCompleteReportIsCompanyScoped(t *testing.T) {
	reports := newFakeReports(queuedReport())
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-other", CompanyID: "co-2", ThreadID: "th-1", CreatedAt: reportStart.Add(time.Second),
	}}}

	NewAPIReportService(reports, docs, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", nil)

	if got := reports.rows["rep-1"].DocumentID; got != "" {
		t.Errorf("document_id = %q — another tenant's document was attached", got)
	}
}

func TestCompleteReportRecordsAFailure(t *testing.T) {
	reports := newFakeReports(queuedReport())
	NewAPIReportService(reports, &fakeDocLookup{}, nil, nil).
		CompleteReport(context.Background(), "rep-1", "th-1", context.DeadlineExceeded)

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

	svc.CompleteReport(context.Background(), "rep-1", "th-1", nil)
	svc.CompleteReport(context.Background(), "rep-1", "th-1", context.DeadlineExceeded)

	got := reports.rows["rep-1"]
	if got.Status != domain.APIReportCompleted || got.DocumentID != "doc-new" {
		t.Errorf("a second completion overwrote a terminal report: %+v", got)
	}
}

// A report id that is not there is a warning, not a panic and not a hang. The
// caller keeps polling and sees the truth, which is that nothing finished.
func TestCompleteReportToleratesAMissingRow(t *testing.T) {
	NewAPIReportService(newFakeReports(), &fakeDocLookup{}, nil, nil).
		CompleteReport(context.Background(), "rep-missing", "th-1", nil)
}

// A nil service is what a stack with no `/v1` report routes installs, and
// ChatRunner calls through it on every turn that carries a report id.
func TestCompleteReportOnANilServiceIsANoOp(t *testing.T) {
	var svc *APIReportService
	svc.CompleteReport(context.Background(), "rep-1", "th-1", nil)
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
