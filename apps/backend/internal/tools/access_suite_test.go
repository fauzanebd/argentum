package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/authz/authztest"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// T-Z9's negative suite, for the surfaces internal/tools owns: the two tools that
// read one restricted object by id, and the source resolver that must not ask.
//
// A tool cannot tell one door without a person from another — it reads the
// person off the turn — so the same probe runs under each, and the table's
// sentence for those surfaces is why they share an outcome.
func TestTheNegativeSuite(t *testing.T) {
	dashboard := string(domain.ResourceKindDashboard)
	document := string(domain.ResourceKindDocument)
	connection := string(domain.ResourceKindConnection)
	key := authztest.Key
	probes := map[string]authztest.Probe{
		key(dashboard, authz.DoorDashboard, "update_dashboard naming it"):                   editPayrollAt(authz.DoorDashboard),
		key(dashboard, authz.DoorAPIKey, "update_dashboard naming it"):                      editPayrollAt(authz.DoorAPIKey),
		key(dashboard, authz.DoorWidget, "update_dashboard naming it"):                      editPayrollAt(authz.DoorWidget),
		key(dashboard, authz.DoorChannel, "update_dashboard naming it"):                     editPayrollAt(authz.DoorChannel),
		key(dashboard, authz.DoorJob, "update_dashboard naming it in a watcher's briefing"): editPayrollAt(authz.DoorJob),
		key(document, authz.DoorDashboard, "search_documents naming it"):                    searchPayrollAt(authz.DoorDashboard),
		key(document, authz.DoorAPIKey, "search_documents naming it"):                       searchPayrollAt(authz.DoorAPIKey),
		key(document, authz.DoorWidget, "search_documents naming it"):                       searchPayrollAt(authz.DoorWidget),
		key(document, authz.DoorChannel, "search_documents naming it"):                      searchPayrollAt(authz.DoorChannel),
		key(document, authz.DoorJob, "search_documents naming it in a watcher's briefing"):  searchPayrollAt(authz.DoorJob),
		key(connection, authz.DoorDashboard, "an agent scoped to it resolving it"):          resolveHRSourceAt(authz.DoorDashboard),
		key(connection, authz.DoorAPIKey, "an agent scoped to it resolving it"):             resolveHRSourceAt(authz.DoorAPIKey),
		key(connection, authz.DoorWidget, "an agent scoped to it resolving it"):             resolveHRSourceAt(authz.DoorWidget),
		key(connection, authz.DoorChannel, "an agent scoped to it resolving it"):            resolveHRSourceAt(authz.DoorChannel),
		key(connection, authz.DoorJob, "an agent scoped to it resolving it"):                resolveHRSourceAt(authz.DoorJob),
	}
	authztest.Run(t, authztest.PackageTools, probes)
}

// turnAt is a turn's context at a door. Only the dashboard's carries a person.
func turnAt(door authz.Door, c authztest.Cell) context.Context {
	ctx := tenantctx.WithCompanyID(context.Background(), authztest.Company)
	channel := map[authz.Door]domain.Channel{
		authz.DoorDashboard: domain.ChannelDashboard,
		authz.DoorAPIKey:    domain.ChannelAPI,
		authz.DoorWidget:    domain.ChannelWidget,
		authz.DoorChannel:   domain.ChannelDiscord,
	}[door]
	if channel != "" {
		ctx = tenantctx.WithChannel(ctx, string(channel))
	}
	if door == authz.DoorDashboard {
		ctx = tenantctx.WithUserID(ctx, c.Person())
	}
	return ctx
}

func editPayrollAt(door authz.Door) authztest.Probe {
	return func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
		svc := &fakeReviser{stored: []*domain.Dashboard{payroll()}}
		az := rec.Authorizer(authztest.World{Kind: domain.ResourceKindDashboard, Target: "dash-payroll", Cell: c})
		out, err := NewUpdateDashboardTool(svc, nil, nil).WithAccess(az).
			Execute(turnAt(door, c), `{"dashboard_id": "dash-payroll", "title": "Gaji"}`)
		if err != nil {
			t.Fatalf("update_dashboard: %v", err)
		}
		if refused := strings.Contains(out, `"restricted":true`); refused == svc.updated {
			t.Fatalf("refused=%v and edited=%v: a refusal edits nothing, and an admission edits", refused, svc.updated)
		}
		return svc.updated
	}
}

func searchPayrollAt(door authz.Door) authztest.Probe {
	return func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
		search := &rankedSearch{ranking: payrollFirst()}
		az := rec.Authorizer(authztest.World{Kind: domain.ResourceKindDocument, Target: "doc-pay", Cell: c})
		out, err := NewSearchDocumentsTool(search).WithAccess(az).
			Execute(turnAt(door, c), `{"query": "gaji direktur", "document_id": "doc-pay"}`)
		if err != nil {
			t.Fatalf("search_documents: %v", err)
		}
		searched := len(search.calls) > 0
		if quoted := slices.Contains(passageDocuments(t, out), "doc-pay"); quoted != searched {
			t.Fatalf("searched=%v and quoted Payroll=%v: a refusal searches nothing and quotes nothing", searched, quoted)
		}
		return searched
	}
}

// resolveHRSourceAt hands ResolveSource no authorizer, because it takes none —
// which is the rule: a source grant is not a data boundary (T-Z6). The cell's
// grant store exists and nothing reads it, so every cell records nothing.
func resolveHRSourceAt(door authz.Door) authztest.Probe {
	return func(t *testing.T, c authztest.Cell, _ *authztest.Recorder) bool {
		repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{
			"co-1": {conn("src-main", "co-1", "Toko Maju warehouse", "postgres"), conn("src-hr", "co-1", "HR warehouse", "postgres")},
		}}
		ctx := agentscope.WithScope(turnAt(door, c), agentscope.Scope{AgentID: "ag-hr", SourceIDs: []string{"src-hr"}})
		got, err := ResolveSource(ctx, repo, "co-1", "src-hr")
		return err == nil && got.ID == "src-hr"
	}
}
