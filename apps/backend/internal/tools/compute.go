package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/shopspring/decimal"

	"github.com/fauzanebd/argentum/internal/compute"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// ComputeTool is exact arithmetic over figures this turn already retrieved
// (T-W1).
//
// **The defect it closes is not a crash.** Revenue comes back from one query
// and cost from another, the user asks for the margin, and the model divides
// two numbers inside a sentence. Every instrument this product owns vouches for
// the result: run_sql ran, rows came back, CheckFabrication is satisfied by the
// evidence existing, and CheckGrounding cannot match a derived figure against a
// returned one because no tool returned it. T-Q14 recorded what that costs — a
// figure 0.078% wrong passed the lot, and one percent of a billion is ten
// million.
//
// **Two things have to be true for the fix to work, and they are separate.**
// The arithmetic has to be exact, which is internal/compute's job and the
// reason there is no float64 in it. And the *inputs* have to be figures this
// product returned rather than figures the model retyped, which is this file's
// job: an input is a reference — `r1.revenue` — resolved against the turn's own
// results, never a literal the model copied out of a payload. Getting only the
// first half would produce exact arithmetic over a transcription error, which
// is the defect wearing a better hat.
type ComputeTool struct {
	// companies supplies the tenant's currency (T-W2). Optional: nil quantises
	// nothing, which is exactly what this tool did before the convention
	// existed.
	companies PIIPolicyLookup
	// profiles supplies the fiscal year start, for `period.*` references
	// (T-W2). Optional in the same way: nil resolves periods against a
	// January year, which is what every caller with no profile has always
	// done.
	profiles CompanyProfileLookup
	// now is the clock, injectable so a period test is not a coin flip on the
	// day it runs.
	now func() time.Time
}

func NewComputeTool() *ComputeTool { return &ComputeTool{now: time.Now} }

// WithConventions gives compute the tenant's money and calendar conventions
// (T-W2). Both halves are optional and degrade separately: without a company
// nothing is quantised, and without a profile a fiscal year starts in January.
func (t *ComputeTool) WithConventions(companies PIIPolicyLookup, profiles CompanyProfileLookup) *ComputeTool {
	t.companies = companies
	t.profiles = profiles
	return t
}

// WithClock replaces the clock. Tests only.
func (t *ComputeTool) WithClock(now func() time.Time) *ComputeTool {
	t.now = now
	return t
}

// CompanyProfileLookup is the one question compute asks of the profile
// repository: when does this tenant's fiscal year start. Narrowed here for
// PIIPolicyLookup's reason — a tool that could read the whole profile is a
// tool that could put the tenant's business description somewhere it does not
// belong.
type CompanyProfileLookup interface {
	GetByCompany(ctx context.Context, companyID string) (*domain.CompanyProfile, error)
}

// currency resolves the tenant's money convention, or the zero value — which
// quantises nothing.
func (t *ComputeTool) currency(ctx context.Context) domain.Currency {
	if t.companies == nil {
		return domain.Currency{}
	}
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return domain.Currency{}
	}
	co, err := t.companies.GetByID(ctx, companyID)
	if err != nil {
		// A lookup that failed is not a licence to guess two decimal places.
		return domain.Currency{}
	}
	return co.Currency()
}

// fiscalYearStart is the month this tenant's year begins in, and January for a
// tenant with no profile.
func (t *ComputeTool) fiscalYearStart(ctx context.Context) int {
	if t.profiles == nil {
		return 1
	}
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return 1
	}
	p, err := t.profiles.GetByCompany(ctx, companyID)
	if err != nil || p == nil {
		return 1
	}
	return p.FiscalYearStartMonth
}

func (t *ComputeTool) clock() time.Time {
	if t.now == nil {
		return time.Now()
	}
	return t.now()
}

func (t *ComputeTool) Name() string { return "compute" }

func (t *ComputeTool) Description() string {
	return "Calculate a derived figure — a ratio, a margin, a growth rate, a difference, a share, a " +
		"per-unit amount — from numbers an earlier tool call in THIS turn returned. Use it INSTEAD of " +
		"doing the arithmetic yourself: a figure you calculate in your head is a figure no tool " +
		"produced, and it cannot be checked. Every result carries a `result_id` (r1, r2, …); refer to " +
		"its numbers as `<result_id>.<column>`, e.g. `r1.total_revenue`. A column with several rows " +
		"can be reduced with sum(), min() or max(). The arithmetic is exact decimal, so money is safe. " +
		"Available: + - * / ( ), comparison, abs, round(x, places), sum, min, max. It has no loops, no " +
		"variables and no other functions — if a question needs those, say so rather than approximating. " +
		"Set `unit` to money for a currency amount: it is then rounded to this workspace's own currency " +
		"precision, which is not always two decimal places."
}

func (t *ComputeTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"expression": {
			Type: "string",
			Description: "The calculation, in terms of the names you bind in `inputs`. Example: " +
				"\"(revenue - cost) / revenue * 100\". Numbers may appear as literals (100, 12, 0.5); " +
				"every other name must be bound.",
			Required: true,
		},
		"inputs": {
			Type: "object",
			Description: "Maps each name in the expression to a figure an earlier tool call returned " +
				"in this turn, as {\"revenue\": \"r1.total_revenue\", \"cost\": \"r2.total_cost\"}. The " +
				"value is a reference, NEVER a number you typed: `<result_id>.<column>` for the whole " +
				"column, or `<result_id>.<column>[0]` for one row, counted from 0. A result with one row " +
				"binds as a single figure. For a run rate you can also bind the length of a date range " +
				"in this workspace's own fiscal calendar: `period.last_quarter.days`, and likewise " +
				"this_month, last_month, this_quarter, ytd, last_year.",
			Required: true,
		},
		"unit": {
			Type: "string",
			Description: "What kind of figure this is. This is not cosmetic: `money` is rounded to " +
				"this workspace's currency precision (rupiah has none, dollars have two) using its " +
				"stated rounding convention, and the others are left exact. Use `ratio` for a fraction " +
				"between 0 and 1, `percent` for the same thing out of 100, `count` for a number of " +
				"things. Marking a ratio as money would destroy it.",
			Required: false,
			Enum:     []interface{}{"money", "ratio", "percent", "count", "other"},
		},
	}
}

func (t *ComputeTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

type computeArgs struct {
	Expression string            `json:"expression"`
	Inputs     map[string]string `json:"inputs"`
	Unit       string            `json:"unit,omitempty"`
}

// maxBoundValues bounds how much of a column is written back onto the payload
// and therefore onto the audit row. run_sql's own default row cap is 100, so
// this does not bite in practice; it exists so a tenant who raised that cap
// cannot turn one compute call into a megabyte of agent_actions.
const maxBoundValues = 200

func (t *ComputeTool) Execute(ctx context.Context, args string) (string, error) {
	var in computeArgs
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return "", fmt.Errorf("compute takes a JSON object with `expression` and `inputs`: %w", err)
	}
	in.Expression = strings.TrimSpace(in.Expression)
	if in.Expression == "" {
		return "", fmt.Errorf("expression is required: state the calculation, e.g. \"(revenue - cost) / revenue\"")
	}

	names, err := compute.Names(in.Expression)
	if err != nil {
		return "", err
	}

	vars := make(map[string]compute.Value, len(names))
	bindings := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		ref, ok := in.Inputs[name]
		if !ok || strings.TrimSpace(ref) == "" {
			return "", fmt.Errorf("%q appears in the expression and is not in `inputs`. Bind it to a figure a tool returned in this turn, as \"%s\": \"r1.<column>\". %s",
				name, name, describeTurnValues(ctx))
		}
		bound, err := t.bind(ctx, ref)
		if err != nil {
			return "", fmt.Errorf("cannot bind %q: %w", name, err)
		}
		if bound.Column {
			vars[name] = compute.Series(bound.Values)
		} else {
			vars[name] = compute.Num(bound.Values[0])
		}
		bindings = append(bindings, bindingRecord(name, ref, bound))
	}

	// A literal the model typed is not refused — 100 in a percentage and 12 in
	// an annualisation are arithmetic, not data — but an expression that binds
	// nothing at all is the model doing the sum in its head with extra steps,
	// and it is exactly what this tool exists to replace.
	if len(names) == 0 {
		return "", fmt.Errorf("this expression has no inputs, so it restates numbers rather than computing from retrieved ones. Bind at least one figure a tool returned in this turn. %s", describeTurnValues(ctx))
	}

	got, err := compute.Eval(in.Expression, vars)
	if err != nil {
		return "", err
	}

	// A money figure is written with the precision its currency has, and
	// rounded the way the tenant's ledger rounds (T-W2). Exact arithmetic over
	// the wrong convention is exactly wrong: a rupiah amount carrying two
	// decimals is a dollar assumption, and a half-cent that goes the wrong way
	// is the difference a reconciliation exists to find.
	//
	// **Only money.** A ratio quantised to a currency's scale is a ratio
	// destroyed — 0.4564 at rupiah's zero places is 0 — so the decision is made
	// on what the figure IS, which is why `unit` is on the call rather than
	// inferred from how the number looks.
	exact := got
	cur := domain.Currency{}
	if in.Unit == unitMoney {
		cur = t.currency(ctx)
		if cur.IsSet() {
			got = compute.Quantize(got, cur.MinorUnits, roundingOf(cur.Rounding))
		}
	}

	payload := map[string]interface{}{
		// The field the rest of the system reads as "this call produced a
		// figure": agentbudget counts it as evidence and the grounding check
		// collects it, the same way both read run_sql's rows.
		"computed":   got.String(),
		"expression": in.Expression,
		// The working, on the payload and therefore on the audit row. A derived
		// figure whose derivation is not recorded is a figure nobody can check
		// six months later, which is the state this tool found the product in.
		"working": working(bindings, exact, got, cur),
	}
	if in.Unit != "" {
		payload["unit"] = in.Unit
	}
	if cur.IsSet() {
		payload["currency"] = cur.Code
	}
	out, _ := json.Marshal(payload)
	return string(out), nil
}

func bindingRecord(name, ref string, bound boundValue) map[string]interface{} {
	rec := map[string]interface{}{"name": name, "from": ref}
	if !bound.Column {
		rec["value"] = bound.Values[0].String()
		return rec
	}
	vals := bound.Values
	rec["count"] = len(vals)
	if len(vals) > maxBoundValues {
		vals = vals[:maxBoundValues]
		rec["truncated"] = true
	}
	strs := make([]string, len(vals))
	for i, d := range vals {
		strs[i] = d.String()
	}
	rec["values"] = strs
	return rec
}

// describeTurnValues is the menu that goes with every refusal, so the model's
// next call is a repair rather than a second guess.
func describeTurnValues(ctx context.Context) string {
	t := turnValuesFrom(ctx)
	if t == nil {
		return "This caller has no earlier tool results to bind to."
	}
	return t.describe()
}

// unitMoney is the one `unit` value that changes what the result is, rather
// than only how it is described.
const unitMoney = "money"

// roundingOf maps the tenant's stated convention onto the arithmetic package's.
// Two vocabularies rather than one shared constant because the domain states a
// policy and internal/compute performs an operation, and the day a third
// convention appears it will be added to exactly one of them.
func roundingOf(m domain.RoundingMode) compute.Rounding {
	if m.OrDefault() == domain.RoundingHalfEven {
		return compute.HalfToEven
	}
	return compute.HalfAwayFromZero
}

// working is the derivation, for the payload and therefore for the audit row.
//
// The pre-rounding figure is recorded whenever rounding changed it. A derived
// number whose derivation is not recorded is a number nobody can check six
// months later — and "why is this one rupiah off the invoice" is precisely the
// question a quantised figure will be asked.
func working(bindings []map[string]interface{}, exact, final decimal.Decimal, cur domain.Currency) map[string]interface{} {
	out := map[string]interface{}{"bindings": bindings}
	if !cur.IsSet() || exact.Equal(final) {
		return out
	}
	out["before_rounding"] = exact.String()
	out["rounded_to"] = cur.MinorUnits
	out["rounding"] = string(cur.Rounding.OrDefault())
	return out
}

// bind resolves one input reference, which is either a figure a tool returned
// in this turn or a fact about a date range (T-W2).
//
// The period namespace is what makes a run rate computable rather than
// narrated. "Revenue per day last quarter" needs a day count, and the day
// count is a property of the tenant's fiscal calendar — 90 days, or 91 in a
// leap year, or a different three months entirely if their year starts in May.
// Before this the model supplied that number from its own arithmetic, which is
// the class T-W1 exists to close, one step upstream.
func (t *ComputeTool) bind(ctx context.Context, ref string) (boundValue, error) {
	trimmed := strings.TrimSpace(ref)
	if !strings.HasPrefix(strings.ToLower(trimmed), periodRefPrefix) {
		return resolveRef(ctx, trimmed)
	}
	rest := trimmed[len(periodRefPrefix):]
	name, field, ok := cutLast(rest, ".")
	if !ok {
		return boundValue{}, fmt.Errorf("%q is not a period reference: write it as period.<name>.days, for example period.last_quarter.days", ref)
	}
	if !strings.EqualFold(field, "days") {
		return boundValue{}, fmt.Errorf("a period has no %q. The only figure a period offers is `days`, as period.%s.days", field, name)
	}
	p, ok := domain.FiscalPeriod(strings.ReplaceAll(name, "_", " "), t.clock(), t.fiscalYearStart(ctx))
	if !ok {
		return boundValue{}, fmt.Errorf("%q is not a period this workspace resolves. It knows: %s. For anything else, compute the day count from dates you already have",
			name, strings.Join(domain.FiscalPeriodNames, ", "))
	}
	return boundValue{Values: []decimal.Decimal{decimal.NewFromInt(int64(p.Days()))}}, nil
}

// periodRefPrefix namespaces a calendar fact so it can never collide with a
// result id: `r1` is minted by a counter and `period` is a word, so the two
// vocabularies cannot meet.
const periodRefPrefix = "period."

// cutLast splits on the LAST separator, because a period name contains dots in
// none of its spellings but its field is always the final segment.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}
