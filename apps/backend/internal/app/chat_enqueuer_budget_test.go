package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// TestEnqueueRefusesAnExhaustedTenant is T-03's gate at unit level: a company
// at zero credits gets ErrInsufficientCredits, and no usage event is written
// because nothing downstream ran.
//
// The ChatEnqueuer is deliberately built with nil thread service and nil
// enqueuer. That is the assertion, not laziness: if the budget gate ever
// stops short-circuiting, the very next line dereferences a nil and this test
// fails loudly instead of quietly passing while a thread, a user message and
// a queued agent turn are created for a tenant who cannot pay for them.
func TestEnqueueRefusesAnExhaustedTenant(t *testing.T) {
	usageRepo := &fakeUsageRepo{}
	credits := &stubCredits{record: &domain.CompanyCredits{
		BalanceMicroUSD:      0,
		MonthlyGrantMicroUSD: testGrant,
	}}
	usageSvc := NewUsageService(usageRepo, credits, DefaultPricing).
		WithCredits(enforcing(), &stubLLMCreds{}, nil)

	enq := NewChatEnqueuer(nil, nil, nil, nil).WithBudget(usageSvc)

	res, err := enq.Enqueue(context.Background(), ChatInput{
		Channel:   domain.ChannelDashboard,
		CompanyID: "c1",
		UserID:    "u1",
		Message:   "what were our sales last month?",
	})
	if err == nil {
		t.Fatal("Enqueue = nil error, want a refusal")
	}
	if !errors.Is(err, domain.ErrInsufficientCredits) {
		t.Errorf("err = %v, want it to wrap domain.ErrInsufficientCredits", err)
	}
	if !strings.Contains(err.Error(), "top up") {
		t.Errorf("err = %q, want it to carry the plain-language message", err)
	}
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	if len(usageRepo.events) != 0 {
		t.Errorf("usage events written = %d, want 0 — the turn was refused before anything could cost money", len(usageRepo.events))
	}
}

// TestEnqueueBudgetPassThrough covers the two verdicts that let the turn run.
// It calls checkBudget rather than Enqueue because the happy path needs a
// thread service, a message repository and a live queue — none of which say
// anything about the budget decision.
func TestEnqueueBudgetPassThrough(t *testing.T) {
	cases := []struct {
		name        string
		balance     int64
		wantVerdict BudgetVerdict
	}{
		{name: "healthy", balance: 20_000_000, wantVerdict: BudgetOK},
		{name: "near the limit", balance: 2_000_000, wantVerdict: BudgetWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credits := &stubCredits{record: &domain.CompanyCredits{
				BalanceMicroUSD:      tc.balance,
				MonthlyGrantMicroUSD: testGrant,
			}}
			usageSvc := NewUsageService(&fakeUsageRepo{}, credits, DefaultPricing).
				WithCredits(enforcing(), &stubLLMCreds{}, nil)
			enq := NewChatEnqueuer(nil, nil, nil, nil).WithBudget(usageSvc)

			st, err := enq.checkBudget(context.Background(), "c1")
			if err != nil {
				t.Fatalf("checkBudget = %v, want nil", err)
			}
			if st.Verdict != tc.wantVerdict {
				t.Errorf("verdict = %q, want %q", st.Verdict, tc.wantVerdict)
			}
		})
	}
}

// TestEnqueueWithoutABudgetCheckerIsUnchanged pins the kill switch at the
// wiring level: an enqueuer with no checker must behave exactly as it did
// before T-03.
func TestEnqueueWithoutABudgetCheckerIsUnchanged(t *testing.T) {
	enq := NewChatEnqueuer(nil, nil, nil, nil)
	st, err := enq.checkBudget(context.Background(), "c1")
	if err != nil {
		t.Fatalf("checkBudget = %v, want nil", err)
	}
	if st.Verdict != BudgetOK {
		t.Errorf("verdict = %q, want %q", st.Verdict, BudgetOK)
	}
}

// TestEnqueueValidatesBeforeCheckingBudget keeps the refusal from masking a
// malformed request: a message with no company id is invalid input, not a
// payment problem, and answering it with 402 would send an integrator to
// their billing page over a missing field.
func TestEnqueueValidatesBeforeCheckingBudget(t *testing.T) {
	credits := &stubCredits{record: &domain.CompanyCredits{MonthlyGrantMicroUSD: testGrant}}
	usageSvc := NewUsageService(&fakeUsageRepo{}, credits, DefaultPricing).
		WithCredits(enforcing(), &stubLLMCreds{}, nil)
	enq := NewChatEnqueuer(nil, nil, nil, nil).WithBudget(usageSvc)

	_, err := enq.Enqueue(context.Background(), ChatInput{
		Channel: domain.ChannelDashboard,
		Message: "hello",
	})
	if err == nil {
		t.Fatal("Enqueue = nil error, want a validation error")
	}
	if errors.Is(err, domain.ErrInsufficientCredits) {
		t.Errorf("err = %v, want a validation error rather than a credit refusal", err)
	}
}
