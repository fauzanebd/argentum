package app

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RetentionService is the purge, the erasure and the export (T-H6).
//
// **Two operations that look alike and are not.** A *purge* runs on a schedule
// because the tenant set a window; an *erasure* runs because somebody asked.
// They share a delete shape and a record table, and they differ in the two
// ways that matter: a purge is bounded by a date and unattended, an erasure is
// total and has a named requester. Keeping them in one service is what makes
// "when was this tenant's data last deleted, and how much" one query.
//
// **The export is not a convenience.** Erasure with no way to leave with your
// own transcript is a product that can only be exited by destroying the thing
// you might need. The ticket asks for both for that reason.
type RetentionService struct {
	repo      domain.RetentionRepository
	records   domain.DataErasureRepository
	companies domain.CompanyRepository
	now       func() time.Time
}

// NewRetentionService wires the service. now is injectable for the same reason
// apiobs injects one: a retention window is arithmetic on a clock, and a test
// that cannot move the clock can only assert that nothing was deleted.
func NewRetentionService(
	repo domain.RetentionRepository,
	records domain.DataErasureRepository,
	companies domain.CompanyRepository,
) *RetentionService {
	return &RetentionService{repo: repo, records: records, companies: companies, now: time.Now}
}

// WithClock overrides the clock. Test-only in practice; kept exported because
// the alternative is an unexported field set by a same-package test, which
// stops working the moment the test moves.
func (s *RetentionService) WithClock(now func() time.Time) *RetentionService {
	if now != nil {
		s.now = now
	}
	return s
}

// PurgeResult is what one tick did, for the log line and for the handler.
type PurgeResult struct {
	Companies int
	Threads   int
	Messages  int
}

// PurgeExpired runs one retention tick across every company that has set a
// window.
//
// **One company's failure does not stop the others.** A tenant whose delete
// deadlocks against a live turn is a reason to try again in an hour, not a
// reason for every other tenant's retention promise to go unkept — and the
// failure is recorded against that tenant rather than lost, so a window that
// has silently stopped being enforced is visible in their own erasure history.
func (s *RetentionService) PurgeExpired(ctx context.Context) (PurgeResult, error) {
	targets, err := s.repo.CompaniesWithRetention(ctx)
	if err != nil {
		return PurgeResult{}, fmt.Errorf("list companies with retention: %w", err)
	}

	var out PurgeResult
	for _, t := range targets {
		if !domain.ValidRetentionDays(t.Days) || t.Days == domain.RetentionForever {
			// The repository only returns rows above zero, so reaching here
			// means the column holds something the domain does not accept.
			// Skipping is the safe half of the ambiguity: a nonsense window
			// must not be read as "delete everything".
			logrus.WithFields(logrus.Fields{"company_id": t.CompanyID, "days": t.Days}).
				Warn("retention: window out of range; skipping this tenant rather than guessing")
			continue
		}
		threads, messages, err := s.purgeOne(ctx, t)
		if err != nil {
			logrus.WithError(err).WithField("company_id", t.CompanyID).
				Error("retention: purge failed for this tenant; other tenants continue")
			continue
		}
		out.Companies++
		out.Threads += threads
		out.Messages += messages
	}
	return out, nil
}

// purgeOne is one tenant's tick, with its record.
//
// A tick that deleted nothing writes no record. The alternative is a
// `data_erasures` table that grows by one row per tenant per night forever and
// buries the four rows somebody is looking for — and "nothing was expired" is
// already answerable from the window and the transcript.
func (s *RetentionService) purgeOne(ctx context.Context, t domain.CompanyRetention) (int, int, error) {
	before := s.now().UTC().AddDate(0, 0, -t.Days)

	rec := &domain.DataErasure{
		CompanyID: t.CompanyID,
		Scope:     domain.ErasureScopeRetention,
		Status:    domain.ErasureStatusRunning,
	}
	if err := s.records.Begin(ctx, rec); err != nil {
		// The record is the evidence. Deleting without it would leave a tenant
		// with fewer messages than yesterday and nothing to explain why, which
		// is worse than a window enforced one tick late.
		return 0, 0, fmt.Errorf("open retention record: %w", err)
	}

	threads, messages, err := s.repo.PurgeCompanyMessages(ctx, t.CompanyID, before)
	if err != nil {
		if ferr := s.records.Fail(ctx, rec.ID, err.Error()); ferr != nil {
			logrus.WithError(ferr).WithField("company_id", t.CompanyID).
				Warn("retention: could not record the failure; the running row stays open")
		}
		return 0, 0, err
	}
	if err := s.records.Complete(ctx, rec.ID, threads, messages); err != nil {
		// The rows are gone either way; losing the record is a reporting
		// failure, not a data one, and must not be reported as a purge that
		// did not happen.
		logrus.WithError(err).WithField("company_id", t.CompanyID).
			Warn("retention: purge completed but its record did not close")
	}
	if threads > 0 || messages > 0 {
		logrus.WithFields(logrus.Fields{
			"company_id": t.CompanyID,
			"days":       t.Days,
			"threads":    threads,
			"messages":   messages,
		}).Info("retention: purged expired conversations")
	}
	return threads, messages, nil
}

// EraseCompanyData deletes every conversation a company has and returns the
// written record.
//
// `requestedBy` is the user id of whoever asked. It is stored rather than
// merely logged: the question a regulator asks is not "was it deleted" but
// "who authorised it, and when".
func (s *RetentionService) EraseCompanyData(ctx context.Context, companyID, requestedBy string) (*domain.DataErasure, error) {
	if companyID == "" {
		return nil, fmt.Errorf("%w: company is required", domain.ErrInvalidInput)
	}

	rec := &domain.DataErasure{
		CompanyID:   companyID,
		RequestedBy: requestedBy,
		Scope:       domain.ErasureScopeAll,
		Status:      domain.ErasureStatusRunning,
	}
	if err := s.records.Begin(ctx, rec); err != nil {
		return nil, fmt.Errorf("open erasure record: %w", err)
	}

	threads, messages, err := s.repo.EraseCompanyConversations(ctx, companyID)
	if err != nil {
		if ferr := s.records.Fail(ctx, rec.ID, err.Error()); ferr != nil {
			logrus.WithError(ferr).WithField("company_id", companyID).
				Warn("erasure: could not record the failure; the running row stays open")
		}
		return nil, fmt.Errorf("erase company conversations: %w", err)
	}
	if err := s.records.Complete(ctx, rec.ID, threads, messages); err != nil {
		return nil, fmt.Errorf("close erasure record: %w", err)
	}

	completed := s.now().UTC()
	rec.Status = domain.ErasureStatusCompleted
	rec.ThreadsDeleted = threads
	rec.MessagesDeleted = messages
	rec.CompletedAt = &completed

	logrus.WithFields(logrus.Fields{
		"company_id":   companyID,
		"requested_by": requestedBy,
		"threads":      threads,
		"messages":     messages,
		"erasure_id":   rec.ID,
	}).Warn("erasure: every conversation for this tenant was deleted on request")

	return rec, nil
}

// ExportCompanyData streams a company's transcripts to fn, oldest first.
func (s *RetentionService) ExportCompanyData(ctx context.Context, companyID string, fn func(domain.ExportedMessage) error) error {
	if companyID == "" {
		return fmt.Errorf("%w: company is required", domain.ErrInvalidInput)
	}
	return s.repo.ExportCompanyConversations(ctx, companyID, fn)
}

// History returns a company's purge and erasure record, newest first.
func (s *RetentionService) History(ctx context.Context, companyID string, limit int) ([]*domain.DataErasure, error) {
	return s.records.ListByCompany(ctx, companyID, limit)
}

// SetRetention changes a company's window.
//
// Validated here rather than only at the column, because the failure this
// guards is not a malformed write — it is a well-formed one that means
// something catastrophic. `message_retention_days = 1` on a tenant who meant
// "one year" deletes their history tonight, so the bound is checked and the
// change is logged at Warn with both values.
func (s *RetentionService) SetRetention(ctx context.Context, companyID string, days int) error {
	if !domain.ValidRetentionDays(days) {
		return fmt.Errorf("%w: message_retention_days must be between %d and %d (%d means keep forever)",
			domain.ErrInvalidInput, domain.RetentionForever, domain.MaxMessageRetentionDays, domain.RetentionForever)
	}
	company, err := s.companies.GetByID(ctx, companyID)
	if err != nil {
		return fmt.Errorf("load company: %w", err)
	}
	was := company.MessageRetentionDays
	if was == days {
		return nil
	}
	company.MessageRetentionDays = days
	if err := s.companies.Update(ctx, company); err != nil {
		return fmt.Errorf("update retention: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"was_days":   was,
		"now_days":   days,
	}).Warn("retention: window changed; the next purge tick enforces it")
	return nil
}
