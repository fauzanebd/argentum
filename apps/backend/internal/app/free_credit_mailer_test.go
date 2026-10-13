package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

// R18 / R19 / AC13 / AC14: the two free-credit emails, word for word, and the
// rules that keep them best-effort.

// 07:32 UTC is 14:32 in Jakarta, the time the operator's page shows.
var signedUp = time.Date(2026, 10, 13, 7, 32, 0, 0, time.UTC)

func TestTheOperatorAlertSaysWhoAndLinksToTheQueue(t *testing.T) {
	sender := &recordingSender{enabled: true}
	m := NewTopupMailer(sender, []string{"owner@example.com", "ops@argentum.id"}, "https://app.argentum.id")
	m.NotifyFreeCredit(context.Background(), FreeCreditAlert{
		CompanyName: "Toko Segar", RequesterEmail: "dina@tokosegar.id", SignedUpAt: signedUp, AmountMicroUSD: 500_000,
	})
	if len(sender.sent) != 2 {
		t.Fatalf("sent %d, want one per operator", len(sender.sent))
	}
	got := sender.sent[0]
	if len(got.To) != 1 || got.To[0] != "owner@example.com" || sender.sent[1].To[0] != "ops@argentum.id" {
		t.Errorf("recipients = %v, %v", got.To, sender.sent[1].To)
	}
	if got.Subject != "Free credit to approve: Toko Segar" {
		t.Errorf("subject = %q", got.Subject)
	}
	want := "Toko Segar (dina@tokosegar.id) signed up on 13 Oct 2026, 14:32 and is waiting for $0.50 of free credit.\n" +
		"Review it: https://app.argentum.id/operator/free-credit"
	if got.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", got.Text, want)
	}
}

func TestTheOperatorAlertIsSilentWhenItCannotLink(t *testing.T) {
	for name, m := range map[string]*TopupMailer{
		"no SMTP":      NewTopupMailer(&recordingSender{enabled: false}, []string{"owner@example.com"}, "https://app.argentum.id"),
		"no base URL":  NewTopupMailer(&recordingSender{enabled: true}, []string{"owner@example.com"}, ""),
		"no operators": NewTopupMailer(&recordingSender{enabled: true}, nil, "https://app.argentum.id"),
	} {
		t.Run(name, func(t *testing.T) {
			m.NotifyFreeCredit(context.Background(), FreeCreditAlert{CompanyName: "Toko Segar"})
			if s := m.sender.(*recordingSender); len(s.sent) != 0 {
				t.Errorf("sent %d", len(s.sent))
			}
		})
	}
	// A failing relay is a log line, not a panic or an error anywhere.
	failing := NewTopupMailer(&recordingSender{enabled: true, err: errors.New("dial tcp: refused")}, []string{"owner@example.com"}, "https://app.argentum.id")
	failing.NotifyFreeCredit(context.Background(), FreeCreditAlert{CompanyName: "Toko Segar"})
}

func TestTheApprovalEmailTellsTheAdminToComeBack(t *testing.T) {
	sender := &recordingSender{enabled: true}
	// No operators: the approval email does not need any.
	m := NewTopupMailer(sender, nil, "https://app.argentum.id")
	m.NotifyFreeCreditReady(context.Background(), FreeCreditReady{
		CompanyID: "c1", CompanyName: "Toko Segar", To: "dina@tokosegar.id", AddedMicroUSD: 500_000,
	})
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d, want 1", len(sender.sent))
	}
	got := sender.sent[0]
	if got.To[0] != "dina@tokosegar.id" || got.Subject != "Your Argentum free credit is ready" {
		t.Errorf("to %v, subject %q", got.To, got.Subject)
	}
	if want := "We've added $0.50 of free credit to Toko Segar. Ask your first question: https://app.argentum.id/chat"; got.Text != want {
		t.Errorf("text = %q, want %q", got.Text, want)
	}

	// Nobody to send to, or nowhere to link: nothing.
	quiet := &recordingSender{enabled: true}
	NewTopupMailer(quiet, nil, "https://app.argentum.id").NotifyFreeCreditReady(context.Background(), FreeCreditReady{CompanyName: "Toko Segar"})
	NewTopupMailer(quiet, nil, "").NotifyFreeCreditReady(context.Background(), FreeCreditReady{To: "dina@tokosegar.id"})
	if len(quiet.sent) != 0 {
		t.Errorf("sent %d without a recipient or a link", len(quiet.sent))
	}
	// A failing relay never fails the approval it follows.
	NewTopupMailer(&recordingSender{enabled: true, err: errors.New("550")}, nil, "https://app.argentum.id").
		NotifyFreeCreditReady(context.Background(), FreeCreditReady{To: "dina@tokosegar.id"})
}
