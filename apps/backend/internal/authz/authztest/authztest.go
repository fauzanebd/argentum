// Package authztest is roadmap 12's negative suite as data (T-Z9): for every kind
// of object a person can be refused, every door into it, and every combination
// of role, grant and mode, what the product does — written down once, and run by
// a probe in each package that owns a check.
//
// It is this repository's first test-support package, and it exists for one
// reason. The checks it holds to account live in four packages — internal/app
// (the enqueue path, rooms, conversations, jobs), internal/tools (the two tools
// that read a restricted object), internal/transport/http/handlers and cmd/api
// (the routes) — and a table in one package's _test file cannot be read by the
// other three. Four tables would be four places for the rule to drift apart. So
// the expectation lives here, every package's suite reads it, and this package's
// own test fails when a kind or a door has nothing written down, or when a
// package the table names never runs its surfaces.
//
// Nothing in production imports it.
package authztest

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Company is the company every probe asks in.
const Company = "co-1"

// The packages a surface's probe runs in, as paths under apps/backend.
const (
	PackageApp      = "internal/app"
	PackageTools    = "internal/tools"
	PackageHandlers = "internal/transport/http/handlers"
	PackageAPI      = "cmd/api"
)

// KindGeneratedDocument is a report or carousel this product generated
// (`documents`), not an uploaded source document. It is not a restrictable kind:
// it is hidden because the conversation that made it is (T-Z11), and a refusal
// of one is recorded as that conversation's.
const KindGeneratedDocument = "generated_document"

// KindPendingAction is an action an agent proposed and a person has not yet
// decided. Not a restrictable kind either: it is hidden because the conversation
// it was raised in is (T-Z14), and a refusal of one is that conversation's.
const KindPendingAction = "pending_action"

// Cell is one combination a surface is run under.
type Cell struct {
	Role domain.Role
	// Granted is whether the person asking holds a grant on the object.
	Granted bool
	// Restricted is whether the object is restricted rather than open.
	Restricted bool
}

// Cells is {admin, member} × {open, restricted} × {not granted, granted}: eight,
// in a fixed order, so a failure names the same cell on every run.
func Cells() []Cell {
	var out []Cell
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember} {
		for _, restricted := range []bool{false, true} {
			for _, granted := range []bool{false, true} {
				out = append(out, Cell{Role: role, Granted: granted, Restricted: restricted})
			}
		}
	}
	return out
}

func (c Cell) String() string {
	mode, grant := "open", "not-granted"
	if c.Restricted {
		mode = "restricted"
	}
	if c.Granted {
		grant = "granted"
	}
	return string(c.Role) + "/" + mode + "/" + grant
}

// Person is who a cell asks as. The admin and the member are two people, so a
// grant written for one cell is never read in another's.
func (c Cell) Person() string { return "u-" + string(c.Role) }

// Mode is the cell's access mode.
func (c Cell) Mode() domain.AccessMode {
	if c.Restricted {
		return domain.AccessModeRestricted
	}
	return domain.AccessModeOpen
}

// Outcome is what a surface does with an object, as four answers — one per mode
// and grant — typed out rather than computed, because a table derived from the
// rule under test agrees with the rule under test.
//
// Role is not an axis. Decision 4 is that a rank is not a grant, and Run proves
// it by holding the admin and the member to the same four answers.
type Outcome struct {
	Name              string
	Open              bool
	OpenGranted       bool
	Restricted        bool
	RestrictedGranted bool
}

// Admits is the outcome's answer for one cell.
func (o Outcome) Admits(c Cell) bool {
	switch {
	case !c.Restricted && !c.Granted:
		return o.Open
	case !c.Restricted:
		return o.OpenGranted
	case !c.Granted:
		return o.Restricted
	default:
		return o.RestrictedGranted
	}
}

// refuses reports whether any cell is refused.
func (o Outcome) refuses() bool {
	return !o.Open || !o.OpenGranted || !o.Restricted || !o.RestrictedGranted
}

var (
	// ByGrant is roadmap 12's rule itself, on a door with a person to hold a
	// grant: open to everyone, restricted to the granted. The dashboard, and a
	// job, which runs as the person who made it (decision 11).
	ByGrant = Outcome{Name: "by grant", Open: true, OpenGranted: true, Restricted: false, RestrictedGranted: true}
	// OpenOnly is a door on which nobody can hold a grant, so a restricted object
	// is closed there whoever a grant names: the widget (decision 10), and a
	// channel no admin acknowledged (decision 8).
	OpenOnly = Outcome{Name: "open only", Open: true, OpenGranted: true, Restricted: false, RestrictedGranted: false}
	// Unasked is a surface that consults no grant for this object. Some are the
	// rule — a key with no agent list (decision 9), an acknowledged channel
	// (decision 8), an agent querying its own source (T-Z6) — and some are gaps.
	// Every one carries its reason in Surface.Why.
	Unasked = Outcome{Name: "unasked", Open: true, OpenGranted: true, Restricted: true, RestrictedGranted: true}
	// Never is a surface closed to the object whatever its mode and grant: a key
	// that lists only other agents (decision 9).
	Never = Outcome{Name: "never", Open: false, OpenGranted: false, Restricted: false, RestrictedGranted: false}
)

// Surface is one place a door reaches an object of its row's kind.
type Surface struct {
	Door authz.Door
	// Name says which place, unique within its kind and door; with them it is
	// the key a probe is registered under.
	Name string
	// Package is where the probe runs.
	Package string
	Outcome Outcome
	// Reason is what a refusal here is recorded as. Empty exactly when Outcome
	// refuses nothing.
	Reason authz.Reason
	// AdminOnly is a surface the role table gives admins alone (apiPolicy). A
	// member is refused one link before any grant is read, and that refusal is
	// the role table's: Run expects it refused and recording nothing.
	AdminOnly bool
	// Why is required when Outcome is Unasked: the reason this surface asks
	// nothing — the rule it follows, or the gap it is and where that is written.
	Why string
}

// Row is one kind of object, and what every door does with one.
type Row struct {
	// Kind is a domain.ResourceKind, authz.KindConversation, or
	// KindGeneratedDocument.
	Kind string
	// RecordedAs is the kind a refusal on this row is counted and audited under,
	// when that is another row's. Empty means Kind.
	RecordedAs string
	Surfaces   []Surface
	// Unprobed is a door with no surface for this kind, and why — at least five
	// words, for resourceExempt's reason: a door nobody wrote a sentence about is
	// a door nobody decided about.
	Unprobed map[authz.Door]string
}

func (r Row) recordedAs() string {
	if r.RecordedAs != "" {
		return r.RecordedAs
	}
	return r.Kind
}

// noPerson is the Why every tool surface on a door without a person shares: the
// tool reads the person off the turn, and these doors put none there.
const noPerson = "the tool asks for the person on the turn, and this door puts none there (decision 7)"

// Table is the suite. **A new kind is one new row here**, and each surface it
// lists fails in its package until that package has a probe for it.
var Table = []Row{
	{
		Kind: string(domain.ResourceKindAgent),
		Surfaces: []Surface{
			{Door: authz.DoorDashboard, Name: "a pick for a new conversation", Package: PackageApp, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "a new conversation with no pick, when it is the default and nothing else is open", Package: PackageApp, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "a turn in a conversation that runs as it", Package: PackageApp, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "adding it to a room", Package: PackageApp, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorAPIKey, Name: "a pick by a key with no agent list", Package: PackageApp, Outcome: Unasked,
				Why: "decision 9: a key is a machine, and one with no list reaches every agent, restricted or not"},
			{Door: authz.DoorAPIKey, Name: "a pick by a key that lists only another agent", Package: PackageApp, Outcome: Never, Reason: authz.ReasonNotOnKey},
			{Door: authz.DoorWidget, Name: "a visitor's pick", Package: PackageApp, Outcome: OpenOnly, Reason: authz.ReasonNotCleared},
			{Door: authz.DoorWidget, Name: "a visitor with no pick, when it is the default and nothing else is open", Package: PackageApp, Outcome: OpenOnly, Reason: authz.ReasonNotCleared},
			{Door: authz.DoorWidget, Name: "a turn in a visitor's conversation that runs as it", Package: PackageApp, Outcome: OpenOnly, Reason: authz.ReasonNotCleared},
			{Door: authz.DoorChannel, Name: "a message on an address bound to it that no admin acknowledged", Package: PackageApp, Outcome: OpenOnly, Reason: authz.ReasonNotCleared},
			{Door: authz.DoorChannel, Name: "a message on an address bound to it and acknowledged", Package: PackageApp, Outcome: Unasked,
				Why: "decision 8: the channel is the grant, once an admin acknowledged that anyone who can post there can use it"},
			{Door: authz.DoorJob, Name: "a schedule firing as the person who made it", Package: PackageApp, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
		},
	},
	{
		Kind: authz.KindConversation,
		Surfaces: []Surface{
			{Door: authz.DoorDashboard, Name: "opening one an agent in it answered", Package: PackageApp, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
		},
		Unprobed: map[authz.Door]string{
			authz.DoorAPIKey:  "a key's conversation belongs to its user_ref, and the agent row's key surfaces decide what runs in it",
			authz.DoorWidget:  "a visitor's conversation is theirs alone, and the agent row's widget surfaces decide what runs in it",
			authz.DoorChannel: "a channel's conversation is the room's, and the agent row's channel surfaces decide who answers in it",
			authz.DoorJob:     "a job's conversation is its own thread, and the agent row's job surface decides whether it fires",
		},
	},
	{
		Kind:       KindGeneratedDocument,
		RecordedAs: authz.KindConversation,
		Surfaces: []Surface{
			{Door: authz.DoorDashboard, Name: "opening a carousel made in a conversation an agent in it answered", Package: PackageHandlers, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
		},
		Unprobed: map[authz.Door]string{
			authz.DoorAPIKey:  "/v1/documents has no person to ask about, and serves a key what its own turns made (access-grants §13d)",
			authz.DoorWidget:  "the widget has no route that opens a generated document by id",
			authz.DoorChannel: "a channel is sent a document as an attachment, and never serves one by id",
			authz.DoorJob:     "a job produces documents and has no route that opens one",
		},
	},
	{
		Kind:       KindPendingAction,
		RecordedAs: authz.KindConversation,
		Surfaces: []Surface{
			{Door: authz.DoorDashboard, Name: "GET /api/actions/:id, raised in a conversation an agent in it answered", Package: PackageHandlers, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "POST /api/actions/:id/approve, raised in a conversation an agent in it answered", Package: PackageHandlers, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "POST /api/actions/:id/reject, raised in a conversation an agent in it answered", Package: PackageHandlers, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
		},
		Unprobed: map[authz.Door]string{
			authz.DoorAPIKey:  "no /v1 route lists, opens or decides a proposed action",
			authz.DoorWidget:  "the widget has no route that lists, opens or decides a proposal",
			authz.DoorChannel: "a channel is never asked to decide a proposal; only the dashboard decides one",
			authz.DoorJob:     "a job may raise a proposal and never lists, opens or decides one",
		},
	},
	{
		Kind: string(domain.ResourceKindDashboard),
		Surfaces: []Surface{
			{Door: authz.DoorDashboard, Name: "GET /api/dashboards/:id", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "GET /api/dashboards/:id/data", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "update_dashboard naming it", Package: PackageTools, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorAPIKey, Name: "update_dashboard naming it", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; a gap, access-grants §15d"},
			{Door: authz.DoorWidget, Name: "update_dashboard naming it", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; a gap against decision 10, access-grants §17c"},
			{Door: authz.DoorChannel, Name: "update_dashboard naming it", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; a gap, access-grants §16d"},
			{Door: authz.DoorJob, Name: "update_dashboard naming it in a watcher's briefing", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; a watcher carries no person, access-grants §16d"},
		},
	},
	{
		Kind: string(domain.ResourceKindConnection),
		Surfaces: []Surface{
			{Door: authz.DoorDashboard, Name: "POST /api/connections/:id/test-rag", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted, AdminOnly: true},
			{Door: authz.DoorDashboard, Name: "POST /api/connections/:id/regenerate-description", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted, AdminOnly: true},
			{Door: authz.DoorDashboard, Name: "POST /api/connections/:id/freshness/test", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted, AdminOnly: true},
			{Door: authz.DoorDashboard, Name: "an agent scoped to it resolving it", Package: PackageTools, Outcome: Unasked,
				Why: "T-Z6's rule: a grant on a source gates what a person is shown, and what an agent may query is agent_sources"},
			{Door: authz.DoorAPIKey, Name: "an agent scoped to it resolving it", Package: PackageTools, Outcome: Unasked, Why: "T-Z6's rule, on a door with no person as on one with a person"},
			{Door: authz.DoorWidget, Name: "an agent scoped to it resolving it", Package: PackageTools, Outcome: Unasked, Why: "T-Z6's rule, on a door with no person as on one with a person"},
			{Door: authz.DoorChannel, Name: "an agent scoped to it resolving it", Package: PackageTools, Outcome: Unasked, Why: "T-Z6's rule, on a door with no person as on one with a person"},
			{Door: authz.DoorJob, Name: "an agent scoped to it resolving it", Package: PackageTools, Outcome: Unasked, Why: "T-Z6's rule, on a door with no person as on one with a person"},
		},
	},
	{
		Kind: string(domain.ResourceKindDocument),
		Surfaces: []Surface{
			{Door: authz.DoorDashboard, Name: "GET /api/knowledge/documents/:id", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "GET /api/knowledge/documents/:id/tables", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "GET /api/knowledge/documents/:id/pages/:page", Package: PackageAPI, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "GET /api/knowledge/tables/:tableId, served under the table's own id", Package: PackageHandlers, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorDashboard, Name: "search_documents naming it", Package: PackageTools, Outcome: ByGrant, Reason: authz.ReasonNotGranted},
			{Door: authz.DoorAPIKey, Name: "search_documents naming it", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; access-grants §17c"},
			{Door: authz.DoorWidget, Name: "search_documents naming it", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; a gap against decision 10, access-grants §17c"},
			{Door: authz.DoorChannel, Name: "search_documents naming it", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; access-grants §17c"},
			{Door: authz.DoorJob, Name: "search_documents naming it in a watcher's briefing", Package: PackageTools, Outcome: Unasked, Why: noPerson + "; a watcher carries no person, access-grants §17c"},
		},
	},
}

// Probe tries to reach one surface's object under one cell and reports whether
// it got through. A probe builds its authorizer with rec.Authorizer, so a
// refusal it causes is recorded where Run reads it.
type Probe func(t *testing.T, c Cell, rec *Recorder) (admitted bool)

// Key names a surface: its kind, its door and its name.
func Key(kind string, door authz.Door, name string) string {
	return kind + " · " + string(door) + " · " + name
}

// Run runs every surface Table gives pkg, under every cell, and holds each to
// its outcome — and each refusal to exactly one count and one audit row, and
// each admission to none.
//
// A surface pkg has no probe for fails, and so does a probe for a surface the
// table does not list: an expectation nobody runs and a check nobody wrote an
// expectation for are the same hole from two sides.
func Run(t *testing.T, pkg string, probes map[string]Probe) {
	t.Helper()
	type owned struct {
		row     Row
		surface Surface
	}
	surfaces := map[string]owned{}
	for _, row := range Table {
		for _, s := range row.Surfaces {
			if s.Package == pkg {
				surfaces[Key(row.Kind, s.Door, s.Name)] = owned{row, s}
			}
		}
	}
	if len(surfaces) == 0 {
		t.Fatalf("authztest.Table gives %s no surface to run", pkg)
	}
	for _, key := range slices.Sorted(maps.Keys(probes)) {
		if _, ok := surfaces[key]; !ok {
			t.Errorf("%s probes %q, which authztest.Table does not give it: write its expectation down before probing it", pkg, key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(surfaces)) {
		o := surfaces[key]
		probe, ok := probes[key]
		if !ok {
			t.Errorf("authztest.Table gives %s the surface %q, and no probe runs it", pkg, key)
			continue
		}
		t.Run(key, func(t *testing.T) {
			for _, c := range Cells() {
				t.Run(c.String(), func(t *testing.T) {
					rec := &Recorder{}
					admitted := probe(t, c, rec)
					checkCell(t, o.row, o.surface, c, admitted, rec)
				})
			}
		})
	}
}

func checkCell(t *testing.T, row Row, s Surface, c Cell, admitted bool, rec *Recorder) {
	t.Helper()
	byRole := s.AdminOnly && c.Role != domain.RoleAdmin
	want := s.Outcome.Admits(c) && !byRole
	verdict := map[bool]string{true: "admitted", false: "refused"}
	if admitted != want {
		why := s.Outcome.Name
		if byRole {
			why = "the role table gives it to admins alone"
		}
		t.Errorf("%s, want %s (%s)", verdict[admitted], verdict[want], why)
	}

	counts, rows := rec.recorded()
	if admitted || byRole {
		if len(counts) != 0 || len(rows) != 0 {
			t.Errorf("%s, and recorded %v with %d audit row(s); only an access refusal is recorded", verdict[admitted], counts, len(rows))
		}
		return
	}

	kind := row.recordedAs()
	wantCount := kind + "/" + string(s.Reason)
	if len(counts) != 1 || counts[wantCount] != 1 {
		t.Errorf("counted %v, want %s exactly once", counts, wantCount)
	}
	if len(rows) != 1 {
		t.Errorf("wrote %d audit row(s) for one refusal, want 1", len(rows))
		return
	}
	got := rows[0]
	if got.ToolName != authz.RefusalAuditTool || got.ResultStatus != domain.ActionStatusBlocked || got.CompanyID != Company {
		t.Errorf("audit row is %s/%s in %q, want %s/%s in %s", got.ToolName, got.ResultStatus, got.CompanyID,
			authz.RefusalAuditTool, domain.ActionStatusBlocked, Company)
	}
	var args map[string]string
	if err := json.Unmarshal(got.ArgsRedacted, &args); err != nil {
		t.Fatalf("audit row arguments are not an object: %v: %s", err, got.ArgsRedacted)
	}
	if args["resource_kind"] != kind || args["reason"] != string(s.Reason) || args["door"] != string(s.Door) || args["resource_id"] == "" {
		t.Errorf("audit row arguments %v, want kind %s, reason %s, door %s and the object's id", args, kind, s.Reason, s.Door)
	}
	if s.Door == authz.DoorDashboard || s.Door == authz.DoorJob {
		if got.ActorKind != domain.ActorKindUser || got.ActorRef != c.Person() {
			t.Errorf("audit row names %s %q, want the person refused, user %q", got.ActorKind, got.ActorRef, c.Person())
		}
	}
}

// Recorder is one cell's refusal log: the counter and the audit log an
// authorizer built with Authorizer records into.
type Recorder struct {
	mu     sync.Mutex
	counts map[string]int
	rows   []*domain.AgentAction
}

// RecordAccessRefusal satisfies authz.RefusalCounter.
func (r *Recorder) RecordAccessRefusal(kind, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = map[string]int{}
	}
	r.counts[kind+"/"+reason]++
}

// Create satisfies authz.AuditWriter.
func (r *Recorder) Create(_ context.Context, a *domain.AgentAction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := *a
	r.rows = append(r.rows, &row)
	return nil
}

// Authorizer is the production decision function over l, recording into r.
func (r *Recorder) Authorizer(l authz.Loader) *authz.Authorizer {
	return authz.New(l).WithCounter(r).WithAudit(r)
}

func (r *Recorder) recorded() (map[string]int, []*domain.AgentAction) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.counts), slices.Clone(r.rows)
}

// World is a grant store for one cell: Target, of Kind, has the cell's mode and
// is granted to the cell's person when the cell says it is; every other id of
// every kind is an open object of Company; another company has nothing.
type World struct {
	Kind   domain.ResourceKind
	Target string
	Cell   Cell
}

// LoadAccess satisfies authz.Loader.
func (w World) LoadAccess(_ context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	out := map[string]domain.ResourceAccess{}
	if companyID != Company {
		return out, nil
	}
	for _, id := range ids {
		if kind == w.Kind && strings.EqualFold(id, w.Target) {
			out[id] = domain.ResourceAccess{Mode: w.Cell.Mode(), Granted: w.Cell.Granted && userID == w.Cell.Person()}
			continue
		}
		out[id] = domain.ResourceAccess{Mode: domain.AccessModeOpen}
	}
	return out, nil
}
