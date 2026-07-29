package tools

import (
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metabase"
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

// RegistryDeps is everything the registry is built from. A nil Docs is the one
// meaningful variation: `generate_document` needs object storage, so the
// correct tool list is per-deployment rather than per-release.
type RegistryDeps struct {
	Pool        *db.TenantConnPool
	Connections domain.ConnectionRepository
	// Redis backs the schema cache. Nil is legal — get_schema then reads
	// through on every call.
	Redis *redis.Client
	// Usage may be nil; each tool substitutes a no-op recorder.
	Usage    UsageRecorder
	Metabase *metabase.Client
	// MetabaseSource resolves a tenant source id to a Metabase database id. It
	// is the connection repository in both processes, named separately because
	// create_visualization asks it a different question than the others do.
	MetabaseSource MetabaseSourceDB
	Dashboards     DashboardSaver
	Scheduled      ScheduledTaskCreator
	// Docs nil means this deployment has no object storage, and
	// generate_document is left out rather than registered and broken.
	Docs                *docgen.Service
	MaxQueryRows        int
	MaxQueryResultBytes int
}

// Registry returns the tools an agent may call on this deployment, unwrapped.
// Callers that run them are expected to add their own guards; callers that
// only list them are expected to add nothing.
func Registry(d RegistryDeps) []interfaces.Tool {
	ts := []interfaces.Tool{
		NewListSourcesTool(d.Connections),
		NewGetSchemaToolWithRedis(d.Pool, d.Connections, d.Redis),
		NewRunSQLTool(d.Pool, d.Connections, d.Usage, d.MaxQueryRows, d.MaxQueryResultBytes),
		NewCreateVisualizationTool(d.Pool, d.Connections, d.Metabase, d.MetabaseSource, d.Usage),
		NewCreateDashboardTool(d.Metabase, d.Usage, d.Dashboards),
		NewScheduleTaskTool(d.Scheduled),
	}
	if d.Docs != nil {
		ts = append(ts, NewGenerateDocumentTool(d.Docs))
	}
	return ts
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
