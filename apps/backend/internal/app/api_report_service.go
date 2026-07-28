package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/webhookout"
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
func (s *APIReportService) CompleteReport(ctx context.Context, reportID, threadID string, runErr error) {
	if s == nil || s.reports == nil || reportID == "" {
		return
	}
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

	docID := ""
	if runErr == nil && s.docs != nil && threadID != "" {
		// Bounded by the job's own created_at: a turn that generated nothing
		// would otherwise attach the *previous* turn's document to this report,
		// and the caller would download a file answering a question they did
		// not ask.
		doc, err := s.docs.NewestForThreadSince(ctx, rep.CompanyID, threadID, rep.CreatedAt)
		switch {
		case err == nil:
			docID = doc.ID
		case errors.Is(err, domain.ErrNotFound):
			logrus.WithFields(logrus.Fields{
				"company_id": rep.CompanyID,
				"report_id":  reportID,
			}).Info("agentic report turn finished without generating a document")
		default:
			logrus.WithError(err).WithField("report_id", reportID).
				Warn("document lookup failed while completing a report; completing without one")
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

// RunRenderJob renders a spec that overran the synchronous window.
//
// Returning an error asks asynq to retry, and it does so only for failures
// that a retry could fix. A spec the renderer refuses is recorded as a failed
// job and returns nil: rendering is deterministic, so the second attempt
// produces the same refusal and the caller waits longer for the same answer.
func (s *APIReportService) RunRenderJob(ctx context.Context, p queue.ReportRenderPayload) error {
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

	spec := p.Spec
	res, err := s.gen.Generate(ctx, docgen.Input{
		Spec:          &spec,
		CompanyID:     p.CompanyID,
		Source:        domain.DocumentSourceAPI,
		APIKeyID:      p.APIKeyID,
		EnforceLimits: true,
	})
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": p.CompanyID,
			"report_id":  p.ReportID,
		}).Warn("asynchronous render failed")
		s.fail(ctx, p.ReportID, "The document could not be rendered from this spec.")
		return nil
	}

	if err := s.reports.Complete(ctx, p.ReportID, domain.APIReportCompleted, res.Document.ID, "", time.Now()); err != nil {
		// The document exists and is downloadable; only the job's bookkeeping
		// failed. Retrying is right — Complete is idempotent by its WHERE
		// clause, and a caller polling a job that never finishes is worse than
		// a second attempt at one UPDATE.
		return fmt.Errorf("complete render job: %w", err)
	}
	if rep, err := s.reports.Get(ctx, p.ReportID); err == nil {
		s.notify(ctx, rep)
	}
	return nil
}

// fail marks a job failed with a message written for whoever reads it.
func (s *APIReportService) fail(ctx context.Context, reportID, msg string) {
	if err := s.reports.Complete(ctx, reportID, domain.APIReportFailed, "", msg, time.Now()); err != nil {
		logrus.WithError(err).WithField("report_id", reportID).
			Error("report job not marked failed; the caller will keep polling")
		return
	}
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
