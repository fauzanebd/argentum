package app

// The shapes a credit check produces, in a file of their own because
// `packages/api-types` is generated from it (T-02b).
//
// Everything else about enforcement — the policy the operator sets, the cache,
// the provisioning of a first grant — lives in credits.go and stays out of the
// generated TypeScript. The rule is the one handlers/wire.go follows: a file
// the generator reads holds wire shapes and nothing else, so what a browser
// can see is decided by where a type is written rather than by a filter
// somewhere downstream.

// BudgetVerdict is what a budget check concluded. The three values are the
// whole vocabulary: everything downstream branches on these, not on the
// balance.
type BudgetVerdict string

const (
	// BudgetOK — the turn may run.
	BudgetOK BudgetVerdict = "ok"
	// BudgetWarning — the turn may run, and the tenant should be told they
	// are close to the end of their credit.
	BudgetWarning BudgetVerdict = "warning"
	// BudgetExhausted — the turn is refused before any money is spent.
	BudgetExhausted BudgetVerdict = "exhausted"
)

// BudgetState is one company's spend position at the moment it was checked.
// It carries the balance and grant as well as the verdict because the
// dashboard's warning banner has to say how much is left, and re-reading the
// balance to render it would defeat the cache this check exists behind.
type BudgetState struct {
	Verdict         BudgetVerdict `json:"verdict"`
	BalanceMicroUSD int64         `json:"balance_micro_usd"`
	GrantMicroUSD   int64         `json:"grant_micro_usd"`
	// RemainingPct is the balance as a percentage of the grant, 0–100. Zero
	// when there is no grant to measure against.
	RemainingPct int `json:"remaining_pct"`
	// BYOLLM marks a tenant running on their own LLM credentials. They pay
	// their provider directly, so no balance was consulted and none applies.
	BYOLLM bool `json:"byo_llm"`
	// Enforced distinguishes "the balance is zero" from "no balance was
	// consulted" (T-A1). Without it `GET /v1/me` would report a $0.00 credit
	// balance on a deployment with enforcement switched off, which reads as
	// "you are out of credit" — the opposite of the truth. It is false on the
	// fail-open paths too, and that is accurate rather than convenient:
	// nothing was enforced for that call either.
	Enforced bool `json:"enforced"`
}

// Blocked reports whether this state must refuse the turn.
func (b BudgetState) Blocked() bool { return b.Verdict == BudgetExhausted }

// CreditsExhaustedMessage is the single refusal every channel sends. It is
// one string rather than one per channel because a WhatsApp user and a
// dashboard user hitting the same wall should be told the same thing, and
// because the alternative is four wordings that drift. It names the fix and
// avoids the word "error": nothing has gone wrong.
const CreditsExhaustedMessage = "This workspace has used all of its Argentum credits, " +
	"so I can't run that right now. Ask an admin to top up the balance — " +
	"current usage is on the Usage page in the dashboard."
