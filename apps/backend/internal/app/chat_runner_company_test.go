package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-B1 at the turn: what a run reads, and what it does when the read fails.
//
// The rule under every case here is that the business profile makes an answer
// better and never makes an answer possible. Nothing about it is a permission,
// so nothing about it may stop a turn.

func TestTheTurnReadsTheCompanyProfile(t *testing.T) {
	repo := newProfileRepo()
	repo.rows["co-1"] = &domain.CompanyProfile{
		CompanyID: "co-1", Industry: "Grocery retail",
		Description: "38 stores across Java.", FiscalYearStartMonth: 4,
	}
	r := (&ChatRunner{}).WithCompanyContext(repo)

	block := r.companyContext(context.Background(), "co-1")
	for _, want := range []string{"Grocery retail", "38 stores across Java.", "April"} {
		if !strings.Contains(block, want) {
			t.Errorf("block = %q, want it to contain %q", block, want)
		}
	}
}

// Locked decision 7, at the layer that has to honour it: a company that has
// never described itself gets exactly the turn it got before this ticket.
func TestACompanyWithNoProfileGetsNoBlock(t *testing.T) {
	r := (&ChatRunner{}).WithCompanyContext(newProfileRepo())
	if got := r.companyContext(context.Background(), "co-1"); got != "" {
		t.Errorf("block = %q, want empty for a company with no profile", got)
	}
}

// A deployment that never wired the loader — the eval harness, and every test
// above this one — runs unchanged.
func TestNoLoaderIsNotAFailure(t *testing.T) {
	if got := (&ChatRunner{}).companyContext(context.Background(), "co-1"); got != "" {
		t.Errorf("block = %q, want empty when no loader is installed", got)
	}
}

// Deliberately the opposite of the roster's binding lookup, which fails closed:
// a binding decides *which agent* answers and getting that wrong is answering
// as somebody else, while a profile only decides how well it answers. Refusing
// the turn would trade a less informed answer for no answer.
func TestAProfileLookupFailureStillRunsTheTurn(t *testing.T) {
	repo := newProfileRepo()
	repo.err = errors.New("control database is down")
	r := (&ChatRunner{}).WithCompanyContext(repo)

	if got := r.companyContext(context.Background(), "co-1"); got != "" {
		t.Errorf("block = %q, want empty when the profile cannot be read", got)
	}
}

// The cap reaches the prompt, not just the dashboard preview.
func TestTheTurnGetsTheCappedBlock(t *testing.T) {
	repo := newProfileRepo()
	repo.rows["co-1"] = &domain.CompanyProfile{
		CompanyID: "co-1", Description: strings.Repeat("a", 20000),
	}
	r := (&ChatRunner{}).WithCompanyContext(repo)

	block := r.companyContext(context.Background(), "co-1")
	if got := len([]rune(block)); got > domain.CompanyContextMaxTokens*4 {
		t.Errorf("block = %d chars, want at most %d", got, domain.CompanyContextMaxTokens*4)
	}
}
