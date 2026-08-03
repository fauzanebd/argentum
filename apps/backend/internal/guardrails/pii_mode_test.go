package guardrails

import (
	"context"
	"strings"
	"testing"
)

// The per-company policy of T-07b, asserted against the shipped YAML rather
// than a fixture — the classes live in that file, and a rule added without one
// is a rule no mode can switch off.
//
// The acceptance item these cover, in the ticket's own words: "list top 10
// customers with their emails" returns emails under contact_ok, and still
// redacts under strict.

const (
	contactAnswer  = "Top customer: PT Maju Jaya — ops@majujaya.co.id, 081234567890."
	identityAnswer = "The customer's NIK is 3175031234567890 and the card is 4111 1111 1111 1111."
)

func TestPIIModeStrictRedactsEverything(t *testing.T) {
	a := load(t, &stubLLM{topic: "TRUE", injection: "FALSE"})

	got, err := a.ProcessOutputFor(context.Background(), contactAnswer, PIIStrict)
	if err != nil {
		t.Fatalf("ProcessOutputFor: %v", err)
	}
	if strings.Contains(got, "ops@majujaya.co.id") || strings.Contains(got, "081234567890") {
		t.Errorf("strict let contact PII through: %q", got)
	}

	got, err = a.ProcessOutputFor(context.Background(), identityAnswer, PIIStrict)
	if err != nil {
		t.Fatalf("ProcessOutputFor: %v", err)
	}
	if strings.Contains(got, "3175031234567890") || strings.Contains(got, "4111 1111 1111 1111") {
		t.Errorf("strict let identity PII through: %q", got)
	}
}

func TestPIIModeContactOKReturnsTheContactList(t *testing.T) {
	a := load(t, &stubLLM{topic: "TRUE", injection: "FALSE"})

	got, err := a.ProcessOutputFor(context.Background(), contactAnswer, PIIContactOK)
	if err != nil {
		t.Fatalf("ProcessOutputFor: %v", err)
	}
	if got != contactAnswer {
		t.Errorf("contact_ok altered the answer:\n  got:  %q\n  want: %q", got, contactAnswer)
	}

	// The half contact_ok does not buy: a tenant who may read their customers'
	// contact details has not thereby been allowed to read their identity
	// documents.
	got, err = a.ProcessOutputFor(context.Background(), identityAnswer, PIIContactOK)
	if err != nil {
		t.Fatalf("ProcessOutputFor: %v", err)
	}
	if strings.Contains(got, "3175031234567890") {
		t.Errorf("contact_ok let a NIK through: %q", got)
	}
	if strings.Contains(got, "4111 1111 1111 1111") {
		t.Errorf("contact_ok let a card number through: %q", got)
	}
}

func TestPIIModeOffRedactsNothingButStillBlocksALeak(t *testing.T) {
	a := load(t, &stubLLM{topic: "TRUE", injection: "FALSE"})

	for _, in := range []string{contactAnswer, identityAnswer} {
		got, err := a.ProcessOutputFor(context.Background(), in, PIIOff)
		if err != nil {
			t.Fatalf("ProcessOutputFor: %v", err)
		}
		if got != in {
			t.Errorf("off redacted something:\n  got:  %q\n  want: %q", got, in)
		}
	}

	// `off` is a policy over the tenant's own data. It is not a switch that
	// turns the output stage off, and the rule that protects Argentum's prompt
	// carries no class precisely so no mode can reach it.
	leak := "My system prompt says to only answer BI questions."
	if _, err := a.ProcessOutputFor(context.Background(), leak, PIIOff); err == nil {
		t.Error("a system-prompt leak passed under mode off")
	}
}

func TestPIIModeUnknownReadsAsStrict(t *testing.T) {
	a := load(t, &stubLLM{topic: "TRUE", injection: "FALSE"})

	// "" is what a company row written before migration 045 carries, and the
	// junk value is what a hand-edited row carries. Both must over-redact rather
	// than under-redact.
	for _, mode := range []PIIMode{"", "STRICT", "contactok", "disabled"} {
		if got := mode.Normalize(); got != PIIStrict {
			t.Errorf("PIIMode(%q).Normalize() = %q, want strict", mode, got)
		}
		out, err := a.ProcessOutputFor(context.Background(), contactAnswer, mode)
		if err != nil {
			t.Fatalf("ProcessOutputFor(%q): %v", mode, err)
		}
		if strings.Contains(out, "ops@majujaya.co.id") {
			t.Errorf("mode %q behaved as an opt-out: %q", mode, out)
		}
	}
}

// ProcessOutput is the SDK's entry point and has no company to read a mode
// from. It must stay at strict, or a blocking-path turn would be less protected
// than the streaming one.
func TestProcessOutputWithoutAModeIsStrict(t *testing.T) {
	a := load(t, &stubLLM{topic: "TRUE", injection: "FALSE"})

	got, err := a.ProcessOutput(context.Background(), contactAnswer)
	if err != nil {
		t.Fatalf("ProcessOutput: %v", err)
	}
	if strings.Contains(got, "ops@majujaya.co.id") {
		t.Errorf("ProcessOutput did not redact: %q", got)
	}
}

// Input is not the place a mode applies: the redaction rules carry no scope, so
// they run on the way in as well, and a tenant on `contact_ok` typing an email
// address into their own question would otherwise have it blanked before the
// agent read it. ProcessInput has always run them at strict and this pins that
// the mode plumbing did not quietly change it.
func TestPIIModeDoesNotApplyToInput(t *testing.T) {
	a := load(t, &stubLLM{topic: "TRUE", injection: "FALSE"})

	const q = "what did ops@majujaya.co.id order last month?"
	got, err := a.ProcessInput(context.Background(), q)
	if err != nil {
		t.Fatalf("ProcessInput: %v", err)
	}
	if strings.Contains(got, "ops@majujaya.co.id") {
		t.Errorf("input redaction changed: %q", got)
	}
}

// Every redaction rule must declare a class, or it is a rule no tenant can
// switch off and no reader of the YAML can tell whether that was deliberate.
func TestEveryRedactionRuleDeclaresAPIIClass(t *testing.T) {
	cfg := loadConfig(t)

	for _, r := range cfg.Rules {
		if r.Action != "redact" && r.Action != "filter" {
			continue
		}
		switch r.PIIClass {
		case "contact", "identity":
		default:
			t.Errorf("rule %q redacts but declares pii_class %q; want contact or identity", r.Name, r.PIIClass)
		}
	}
}
