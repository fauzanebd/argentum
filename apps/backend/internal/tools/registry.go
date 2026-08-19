package tools

import (
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The tool registry, in one place (T-S1).
//
// Two processes need to know what tools exist. The worker builds them and runs
// them; the API only needs their names, so an admin scoping an agent ticks a
// list of what this deployment actually has. Those were about to become two
// lists — and the second one would have been wrong the first time a tool was
// added, which is the failure mode a per-agent allowlist can least afford: a
// tool missing from the checkboxes is a capability no agent can ever be given.
//
// So there is one construction site. The worker wraps what comes back in the
// budget guard and the audit recorder (T-16, T-05); the API reads Names off it
// and builds nothing else. A tool added below appears in both without a second
// edit.

// DashboardStore is the whole of app.DashboardService the dashboard tools need
// between them — building one and revising one. The two halves stay separate
// interfaces beside their tools, so each tool names only what it calls.
type DashboardStore interface {
	DashboardCreator
	DashboardReviser
}

// RegistryDeps is everything the registry is built from. A nil Docs is the one
// meaningful variation: `generate_document` needs object storage, so the
// correct tool list is per-deployment rather than per-release.
type RegistryDeps struct {
	Pool        *db.TenantConnPool
	Connections domain.ConnectionRepository
	// Redis backs the schema cache. Nil is legal — get_schema then reads
	// through on every call.
	Redis *redis.Client
	// Schema is the get_schema tool, supplied when the caller also needs it
	// directly. Nil builds one here, which is what AllNames and the API's
	// name-only listing do.
	//
	// It is a field rather than a second construction at each call site because
	// business inference (T-B2) reads the schema through this exact instance:
	// two instances is two L1 caches, and the fingerprint that decides whether
	// to spend an LLM call has to be computed from the same answer the agent
	// gets.
	Schema *GetSchemaTool
	// Usage may be nil; each tool substitutes a no-op recorder.
	Usage UsageRecorder
	// Companies supplies the tenant's PII redaction mode to run_sql's
	// empty-result probe (T-H10). Nil is legal — the probe then discloses
	// neither contact nor identity columns, which is the strict reading and the
	// right default for a build that has no company repository to hand.
	Companies PIIPolicyLookup
	// Dashboards backs create_dashboard (T-D11) and update_dashboard (T-D22).
	// Nil is legal and is what the API's name-only build and cmd/mcp pass: both
	// tools still register, so they appear in the agent allowlist and the
	// template vocabulary, and report "not configured" if ever executed without a
	// service.
	//
	// One field for both, rather than one per tool: they are two halves of the
	// same service and a deployment that can build a dashboard and not edit one
	// is not a configuration anybody wants — it is the state T-D22 was written to
	// end.
	Dashboards DashboardStore
	Scheduled  ScheduledTaskCreator
	// Docs nil means this deployment has no object storage, and
	// generate_document is left out rather than registered and broken.
	Docs *docgen.Service
	// Metrics backs list_metrics and query_metric (T-07). Nil is legal and is
	// what the API's name-only build passes: the two tools still register (so
	// they appear in the agent allowlist and the template vocabulary) and report
	// "not configured" if ever executed without a store. The worker passes a real
	// store, which is where they run.
	Metrics             MetricStore
	MaxQueryRows        int
	MaxQueryResultBytes int
	// Actions backs propose_action (T-10). Nil is legal and is what the API's
	// name-only build passes: the tool still registers (so it appears in the
	// agent allowlist and the template vocabulary) and reports "not configured"
	// if ever executed without a proposer. The worker passes the real service.
	Actions ActionProposer
	// Renders lets generate_document offer `mp4` (T-V3). Nil — the API's
	// name-only build, the eval harness, cmd/mcp — leaves the format out of the
	// enum and out of the description entirely, because a video is finished by
	// a worker and a process with no queue has no way to finish one.
	//
	// Unlike Metrics and Actions above, this is not a "registers and reports
	// not configured" dependency: those affect a tool's *behaviour* and this
	// affects its *vocabulary*, and a format the model is offered but nothing
	// can produce is a promise to a customer rather than an error to an admin.
	Renders VideoEnqueuer
	// Documents backs search_documents (T-P9): the prose of the PDFs a tenant
	// uploaded. Nil is legal and is what the API's name-only build passes — the
	// tool still registers, so it appears in the agent allowlist and the
	// template vocabulary, and reports "not configured" if executed. The same
	// pattern the metric tools state, and it matters more here: a deployment
	// that has not indexed anything yet should not have a different tool list
	// from one that has.
	Documents DocumentSearch
}

// Registry returns the tools an agent may call on this deployment, unwrapped.
// Callers that run them are expected to add their own guards; callers that
// only list them are expected to add nothing.
func Registry(d RegistryDeps) []interfaces.Tool {
	schema := d.Schema
	if schema == nil {
		schema = NewGetSchemaToolWithRedis(d.Pool, d.Connections, d.Redis)
	}
	ts := []interfaces.Tool{
		NewListSourcesTool(d.Connections),
		schema,
		// The metric tools rank ahead of run_sql in the list because the system
		// prompt tells the agent to prefer them: an authoritative number over a
		// re-derived one (T-07). They register unconditionally — nil Metrics
		// still yields their names for the allowlist and the vocabulary.
		NewListMetricsTool(d.Metrics),
		NewQueryMetricTool(d.Metrics, d.Usage),
		// The schema tool is handed to run_sql as well as registered: a query that
		// fails on a name the source does not have answers with the names it does,
		// off the cache above rather than another introspection.
		NewRunSQLTool(d.Pool, d.Connections, d.Usage, d.MaxQueryRows, d.MaxQueryResultBytes).
			WithSchema(schema).
			WithPIIPolicy(d.Companies),
		// One call, every panel (T-D11). The pair it replaces —
		// create_visualization then create_dashboard — spent four tool calls on a
		// three-panel answer and carried a thread-scoped in-memory map to make
		// the second call optional.
		NewCreateDashboardTool(d.Dashboards, d.Connections, d.Usage),
		// Revise rather than rebuild (T-D22). It sits directly after the tool it
		// pairs with, because the prompt's argument is a comparison between the
		// two: a second dashboard leaves the wrong one in the list and breaks a
		// link already sent, and the model reads them in this order.
		NewUpdateDashboardTool(d.Dashboards, d.Connections, d.Usage),
		NewScheduleTaskTool(d.Scheduled),
		// Asking, as an action (T-Q4). It has no dependencies at all, which is
		// the point: the alternative to asking is always a tool call, and a
		// sentence in the system prompt was losing to one. Registered
		// unconditionally for that reason — a deployment where the agent cannot
		// ask is a deployment where it guesses.
		NewAskClarificationTool(),
		// propose_action registers unconditionally, like the metric tools: a nil
		// proposer still yields the name for the allowlist and the vocabulary, and
		// reports "not configured" if executed. The one write-capable tool the
		// agent has, and the only one it can propose but not perform (T-10).
		NewProposeActionTool(d.Actions),
		// What a document says, as a tool call and never as an injection
		// (T-P9). It sits after the data tools because that is the order the
		// prompt argues for: a figure that exists in a published table is
		// better answered by querying it, and a passage is what you fall back
		// to when the answer is prose.
		NewSearchDocumentsTool(d.Documents),
	}
	if d.Docs != nil {
		ts = append(ts, NewGenerateDocumentTool(d.Docs).WithVideoQueue(d.Renders))
	}
	return ts
}

// AllNames is every tool name this *release* can register, including the ones
// a given deployment leaves out. Built from the same Registry with the one
// optional dependency present, so it cannot drift from what is above it.
//
// It exists for validating things written by us rather than by a deployment:
// config/agent_templates.yaml names tools, and a typo there must fail the build
// or the boot everywhere — not only on the deployments that happen to run the
// tool it misspelled. Checking a template against Names would refuse to start
// any deployment without object storage. Anything a *tenant* submits is checked
// against Names instead, because a tenant cannot be offered what is not there.
func AllNames() []string {
	return Names(Registry(RegistryDeps{Docs: &docgen.Service{}}))
}

// Names is what the agents API serves as checkboxes. Order follows the
// registry, which runs cheapest-and-most-general first — a list an admin reads
// top to bottom in roughly the order an agent would use it.
func Names(ts []interfaces.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}
