package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/embedding"
)

// fakeEmbedder hands back a fixed vector. What the tests care about is which
// candidates reach it, never what it returns.
type fakeEmbedder struct {
	calls int
	err   error
}

func (f *fakeEmbedder) For(context.Context, string) (embedding.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f, nil
}
func (f *fakeEmbedder) Embed(_ context.Context, in []string) ([][]float32, error) {
	f.calls += len(in)
	out := make([][]float32, len(in))
	for i := range in {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}
func (f *fakeEmbedder) Model() string { return "fake-embed" }
func (f *fakeEmbedder) Dim() int      { return 3 }

type fakeExamples struct {
	saved     []*domain.QueryExample
	existing  map[string]bool
	upsertErr error
	originErr error
}

func (f *fakeExamples) Upsert(_ context.Context, e *domain.QueryExample) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.saved = append(f.saved, e)
	return nil
}

func (f *fakeExamples) TopK(context.Context, string, []string, []float32, int) ([]domain.QueryExampleHit, error) {
	return nil, nil
}
func (f *fakeExamples) CountByCompany(context.Context, string) (int, error) {
	return len(f.saved), nil
}
func (f *fakeExamples) MarkUsed(context.Context, []int64, time.Time) error { return nil }
func (f *fakeExamples) ExistingOrigins(context.Context, string, []string) (map[string]bool, error) {
	if f.originErr != nil {
		return nil, f.originErr
	}
	if f.existing == nil {
		return map[string]bool{}, nil
	}
	return f.existing, nil
}
func (f *fakeExamples) DeleteByCompany(context.Context, string) (int, error) {
	n := len(f.saved)
	f.saved = nil
	return n, nil
}

type fakeCandidates struct {
	items []domain.CookbookCandidate
	err   error
}

func (f fakeCandidates) Candidates(context.Context, string, time.Time, int) ([]domain.CookbookCandidate, error) {
	return f.items, f.err
}
func (f fakeCandidates) CompaniesWithActivity(context.Context, time.Time) ([]string, error) {
	return []string{"co-1"}, nil
}

type fakeVerdicts struct {
	negative map[string]bool
	err      error
	// asked records the ids the gate looked up, so a test can assert the
	// harvester consults the id space message_feedback actually holds.
	asked *[]string
}

func (f fakeVerdicts) Upsert(context.Context, *domain.MessageFeedback) error { return nil }
func (f fakeVerdicts) GetByMessage(context.Context, string, string) ([]*domain.MessageFeedback, error) {
	return nil, nil
}
func (f fakeVerdicts) ListByCompany(context.Context, string, bool, int, int) ([]*domain.MessageFeedback, error) {
	return nil, nil
}
func (f fakeVerdicts) Summarize(context.Context, string, time.Time, time.Time) (domain.FeedbackSummary, error) {
	return domain.FeedbackSummary{}, nil
}
func (f fakeVerdicts) NegativeMessageIDs(_ context.Context, _ string, ids []string) (map[string]bool, error) {
	if f.asked != nil {
		*f.asked = append(*f.asked, ids...)
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.negative == nil {
		return map[string]bool{}, nil
	}
	return f.negative, nil
}

// candidate builds a turn the way the repository returns one: MessageID is the
// user's question, AnswerMessageID is the assistant reply. The two must differ,
// because in production they always do and a fake that reuses one id hides the
// entire verdict gate — which is exactly what this file used to do.
func candidate(id, sql string) domain.CookbookCandidate {
	return domain.CookbookCandidate{
		MessageID: id, AnswerMessageID: id + "-answer",
		SourceID: "src-1", Question: "what were sales last month?",
		SQL: sql, RowCount: 12, RanAt: time.Now(),
	}
}

// The gate this ticket is gated on. An answer somebody marked wrong must never
// become an example: `result_status = ok` means the query executed, never that
// it answered.
//
// The verdict is keyed on the ANSWER's message id, which is the only kind
// message_feedback holds — FeedbackService.Rate refuses anything that is not an
// assistant message. Until 2026-08-11 this test filed it against the question's
// id instead, which the service also read, so test and code agreed and both
// disagreed with production: no real thumbs-down could ever match a real
// candidate, and every turn somebody called wrong was learned from anyway.
// Found against the live control database, not here.
func TestHarvestRefusesToLearnFromAnAnswerMarkedWrong(t *testing.T) {
	examples := &fakeExamples{}
	embedder := &fakeEmbedder{}
	svc := NewCookbookService(
		examples,
		fakeCandidates{items: []domain.CookbookCandidate{
			candidate("msg-good", "SELECT SUM(sales_amount) FROM fact_sales"),
			candidate("msg-bad", "SELECT SUM(unit_price) FROM fact_sales"),
		}},
		fakeVerdicts{negative: map[string]bool{"msg-bad-answer": true}},
		embedder,
	)

	res, err := svc.Harvest(context.Background(), "co-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}
	if res.Learned != 1 || res.SkippedNegative != 1 {
		t.Errorf("learned %d, skipped %d negative; want 1 and 1 (%+v)",
			res.Learned, res.SkippedNegative, res)
	}
	if len(examples.saved) != 1 || examples.saved[0].OriginMessageID != "msg-good" {
		t.Fatalf("the wrong turn was learned from: %+v", examples.saved)
	}
	// The gate runs BEFORE the embedding call, so a rejected turn costs nothing.
	if embedder.calls != 1 {
		t.Errorf("embedded %d questions, want 1 — a rejected turn was embedded anyway", embedder.calls)
	}
}

// The gate must ask about the id space message_feedback actually holds.
//
// Separate from the test above because that one can be satisfied by a service
// that reads the right id for the wrong reason; this one pins the query. Every
// row in message_feedback is keyed by an assistant message, so a harvest that
// never names an answer id has a verdict gate that cannot fire — which is what
// shipped, and what the live control database showed on 121 real candidates.
func TestHarvestAsksAboutTheAnswerNotOnlyTheQuestion(t *testing.T) {
	var asked []string
	svc := NewCookbookService(
		&fakeExamples{},
		fakeCandidates{items: []domain.CookbookCandidate{candidate("msg-1", "SELECT SUM(x) FROM fact_sales")}},
		fakeVerdicts{asked: &asked},
		&fakeEmbedder{},
	)
	if _, err := svc.Harvest(context.Background(), "co-1", time.Time{}, 10); err != nil {
		t.Fatalf("Harvest: %v", err)
	}

	var sawAnswer bool
	for _, id := range asked {
		if id == "msg-1-answer" {
			sawAnswer = true
		}
	}
	if !sawAnswer {
		t.Errorf("the verdict gate was asked about %v — never about the answer id, "+
			"which is the only kind message_feedback holds", asked)
	}
}

// A turn already in the cookbook is not re-embedded. This is what makes the
// hourly harvest cheap: on the second run almost everything is known.
func TestHarvestDoesNotRelearnWhatItAlreadyKnows(t *testing.T) {
	examples := &fakeExamples{existing: map[string]bool{"msg-1": true}}
	embedder := &fakeEmbedder{}
	svc := NewCookbookService(
		examples,
		fakeCandidates{items: []domain.CookbookCandidate{
			candidate("msg-1", "SELECT 1 FROM t"),
			candidate("msg-2", "SELECT 2 FROM t"),
		}},
		fakeVerdicts{}, embedder,
	)
	res, err := svc.Harvest(context.Background(), "co-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}
	if res.SkippedKnown != 1 || res.Learned != 1 {
		t.Errorf("skipped_known=%d learned=%d, want 1 and 1", res.SkippedKnown, res.Learned)
	}
	if embedder.calls != 1 {
		t.Errorf("embedded %d questions, want 1 — a known turn was re-embedded", embedder.calls)
	}
}

// Half a query is a syntax error to imitate, so the cap skips the candidate
// rather than truncating it — and skips it before spending an embedding call.
func TestHarvestSkipsOverLongSQL(t *testing.T) {
	long := "SELECT " + strings.Repeat("a,", domain.QueryExampleMaxSQLChars) + "1 FROM t"
	examples := &fakeExamples{}
	embedder := &fakeEmbedder{}
	svc := NewCookbookService(
		examples,
		fakeCandidates{items: []domain.CookbookCandidate{candidate("msg-1", long)}},
		fakeVerdicts{}, embedder,
	)
	res, err := svc.Harvest(context.Background(), "co-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}
	if res.SkippedTooLong != 1 || res.Learned != 0 {
		t.Errorf("skipped_too_long=%d learned=%d, want 1 and 0", res.SkippedTooLong, res.Learned)
	}
	for _, e := range examples.saved {
		if len(e.SQL) <= domain.QueryExampleMaxSQLChars {
			t.Error("an over-long query was truncated and stored instead of skipped")
		}
	}
}

// "We could not check" must never resolve to "assume it was fine" — not for
// the one gate that exists to keep wrong answers out of the prompt.
func TestHarvestAbortsWhenVerdictsCannotBeRead(t *testing.T) {
	examples := &fakeExamples{}
	svc := NewCookbookService(
		examples,
		fakeCandidates{items: []domain.CookbookCandidate{candidate("msg-1", "SELECT 1 FROM t")}},
		fakeVerdicts{err: errors.New("database is down")},
		&fakeEmbedder{},
	)
	_, err := svc.Harvest(context.Background(), "co-1", time.Time{}, 10)
	if err == nil {
		t.Fatal("Harvest continued after failing to read the verdicts")
	}
	if !strings.Contains(err.Error(), "feedback") {
		t.Errorf("error does not name the gate that failed: %v", err)
	}
	if len(examples.saved) != 0 {
		t.Error("an example was written after the verdict read failed")
	}
}

func TestHarvestIsANoOpWithoutCandidates(t *testing.T) {
	svc := NewCookbookService(&fakeExamples{}, fakeCandidates{}, fakeVerdicts{}, &fakeEmbedder{})
	res, err := svc.Harvest(context.Background(), "co-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}
	if res.Examined != 0 || res.Learned != 0 {
		t.Errorf("an empty harvest reported %+v", res)
	}
}

func TestHarvestRefusesWhenUnconfigured(t *testing.T) {
	svc := NewCookbookService(nil, nil, nil, nil)
	if _, err := svc.Harvest(context.Background(), "co-1", time.Time{}, 10); err == nil {
		t.Error("an unconfigured cookbook accepted a harvest")
	}
	if _, err := svc.Forget(context.Background(), "co-1"); err == nil {
		t.Error("an unconfigured cookbook accepted a forget")
	}
	if n, err := svc.Count(context.Background(), "co-1"); err != nil || n != 0 {
		t.Errorf("Count on an unconfigured cookbook = %d, %v", n, err)
	}
}

func TestForgetEmptiesTheCookbook(t *testing.T) {
	examples := &fakeExamples{saved: []*domain.QueryExample{{ID: 1}, {ID: 2}}}
	svc := NewCookbookService(examples, fakeCandidates{}, fakeVerdicts{}, &fakeEmbedder{})
	n, err := svc.Forget(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if n != 2 {
		t.Errorf("Forget deleted %d, want 2", n)
	}
	if len(examples.saved) != 0 {
		t.Error("examples survived Forget")
	}
}
