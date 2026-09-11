package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/compute"
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
type ComputeTool struct{}

func NewComputeTool() *ComputeTool { return &ComputeTool{} }

func (t *ComputeTool) Name() string { return "compute" }

func (t *ComputeTool) Description() string {
	return "Calculate a derived figure — a ratio, a margin, a growth rate, a difference, a share, a " +
		"per-unit amount — from numbers an earlier tool call in THIS turn returned. Use it INSTEAD of " +
		"doing the arithmetic yourself: a figure you calculate in your head is a figure no tool " +
		"produced, and it cannot be checked. Every result carries a `result_id` (r1, r2, …); refer to " +
		"its numbers as `<result_id>.<column>`, e.g. `r1.total_revenue`. A column with several rows " +
		"can be reduced with sum(), min() or max(). The arithmetic is exact decimal, so money is safe. " +
		"Available: + - * / ( ), comparison, abs, round(x, places), sum, min, max. It has no loops, no " +
		"variables and no other functions — if a question needs those, say so rather than approximating."
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
				"binds as a single figure.",
			Required: true,
		},
		"unit": {
			Type: "string",
			Description: "What kind of figure this is, so it can be presented correctly. `money` for " +
				"an amount of currency, `ratio` for a fraction between 0 and 1, `percent` for the same " +
				"thing out of 100, `count` for a number of things.",
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
		bound, err := resolveRef(ctx, ref)
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

	payload := map[string]interface{}{
		// The field the rest of the system reads as "this call produced a
		// figure": agentbudget counts it as evidence and the grounding check
		// collects it, the same way both read run_sql's rows.
		"computed":   got.String(),
		"expression": in.Expression,
		// The working, on the payload and therefore on the audit row. A derived
		// figure whose derivation is not recorded is a figure nobody can check
		// six months later, which is the state this tool found the product in.
		"working": map[string]interface{}{
			"bindings": bindings,
		},
	}
	if in.Unit != "" {
		payload["unit"] = in.Unit
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
