package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
)

// failingDebit is a CreditsRepository whose Decrement always fails, the way
// the Postgres statement did on every call until RIZ-79's QA found it.
type failingDebit struct{ fakeCreditsRepo }

func (failingDebit) Decrement(context.Context, string, int64) error {
	return errors.New("pq: operator is not unique: - unknown")
}

// A failed debit is free usage. It must not fail the turn that already ran,
// and it must not be silent: a Warn naming the company and one count on the
// credit_debit_failures metric.
func TestAFailedDebitIsCountedAndLogged(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()
	before := metrics.Default().GetSnapshot().LLM.CreditDebitFailures

	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, failingDebit{}, DefaultPricing)
	svc.RecordLLM(context.Background(), "co-1", "th-1", "m-1", "unpriced-model", 1000, 500, 0, 0)

	if len(repo.events) != 1 || repo.events[0].CostMicroUSD <= 0 {
		t.Fatalf("usage events = %+v, want one priced event — the record is what a reconciliation reads", repo.events)
	}
	if got := metrics.Default().GetSnapshot().LLM.CreditDebitFailures - before; got != 1 {
		t.Errorf("credit_debit_failures moved by %d, want 1", got)
	}
	var warned *logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			warned = e
		}
	}
	if warned == nil {
		t.Fatal("no Warn for a failed debit")
	}
	if warned.Data["company_id"] != "co-1" || warned.Data["cost_micro_usd"] != repo.events[0].CostMicroUSD {
		t.Errorf("warning fields = %v, want company_id co-1 and the event's cost", warned.Data)
	}
}

// The ordinary path stays quiet: a debit that lands is not counted.
func TestASuccessfulDebitIsNotCounted(t *testing.T) {
	before := metrics.Default().GetSnapshot().LLM.CreditDebitFailures
	credits := &recordingCredits{}
	svc := NewUsageService(&fakeUsageRepo{}, credits, DefaultPricing)
	svc.RecordLLM(context.Background(), "co-1", "th-1", "m-1", "unpriced-model", 1000, 500, 0, 0)

	if len(credits.decrements) != 1 {
		t.Fatalf("decrements = %v, want 1", credits.decrements)
	}
	if got := metrics.Default().GetSnapshot().LLM.CreditDebitFailures - before; got != 0 {
		t.Errorf("credit_debit_failures moved by %d on a debit that landed", got)
	}
}

var _ domain.CreditsRepository = failingDebit{}
