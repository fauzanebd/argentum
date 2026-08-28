package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// The roster row folds three bindings into itself, and the failure this file
// exists for is the one that has already happened once: `agent_skills` was
// written by `SkillRepo.SetAgentBinding` and read by nobody, so every agent was
// offered every enabled skill and a narrowed binding did nothing at all.
//
// That defect is invisible to every other test in this tree. The domain's
// `AllowsSkill` is correct, the tool's refusal is correct, the service's write
// is correct — the field they all agree about was simply never filled in.

// countingScanner stands in for *sql.Row. It records how many destinations it
// was handed and fills each one, which is the only way to catch a SELECT and a
// Scan that have drifted apart without a database.
type countingScanner struct{ targets int }

func (c *countingScanner) Scan(dest ...any) error {
	c.targets = len(dest)
	for _, d := range dest {
		switch v := d.(type) {
		case *string:
			*v = ""
		case *bool:
			*v = false
		case *time.Time:
			*v = time.Time{}
		case *pq.StringArray:
			*v = nil
		}
	}
	return nil
}

// selectedColumns counts the top-level commas in the column list. Paren-aware,
// because three of the entries are `ARRAY(SELECT … ORDER BY …)` subqueries with
// commas nowhere but inside them today and no guarantee of that tomorrow.
func selectedColumns(list string) int {
	depth, n := 0, 1
	for _, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return n
}

// **A column added to the SELECT without a destination in the Scan is a
// runtime error on every roster read**, and a destination added without a
// column is the same. Neither compiles differently, and both are one line.
func TestTheAgentSelectAndItsScanAgree(t *testing.T) {
	s := &countingScanner{}
	if _, err := scanAgent(s); err != nil {
		t.Fatalf("scanAgent: %v", err)
	}
	if got, want := s.targets, selectedColumns(agentColumns); got != want {
		t.Errorf("scanAgent takes %d destinations for %d selected columns", got, want)
	}
}

// The regression itself, named: the binding T-K1 built and T-K6 puts a control
// on has to be read back onto the row, or `AllowsSkill` answers "everything"
// for an agent an admin deliberately narrowed.
func TestTheRosterRowCarriesItsSkillBinding(t *testing.T) {
	if !strings.Contains(agentColumns, "agent_skills") {
		t.Fatal("the roster row does not read agent_skills; every agent is offered every skill")
	}
	if !strings.Contains(agentColumns, "skill_ids") {
		t.Error("the binding is selected without the alias the scan reads it as")
	}
}

// An agent with no rows in any of the three binding tables must come back with
// empty slices rather than nils: `[]string(nil)` and `[]string{}` both marshal
// to `[]`, and only one of them survives a round trip through a JSON decoder as
// an empty allowlist rather than a missing field.
func TestAnUnboundAgentGetsEmptySlicesNotNils(t *testing.T) {
	a, err := scanAgent(&countingScanner{})
	if err != nil {
		t.Fatalf("scanAgent: %v", err)
	}
	if a.AllowedTools == nil || a.SourceIDs == nil || a.MCPServerIDs == nil || a.SkillIDs == nil {
		t.Errorf("a binding came back nil: tools=%v sources=%v mcp=%v skills=%v",
			a.AllowedTools, a.SourceIDs, a.MCPServerIDs, a.SkillIDs)
	}
}
