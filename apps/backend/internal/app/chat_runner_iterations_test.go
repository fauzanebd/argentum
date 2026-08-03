package app

import (
	"context"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// The turn's budget and the ceiling the SDK enforces have to be the same
// number. They were not: the factory took its iteration cap once at boot, so
// ForDocument's headroom raised the tracker's ceiling to twelve while the
// provider still stopped the turn at eight. The tracker's iteration dimension
// could then never trip, which is what it is for — it stops one iteration early
// so the model writes its answer knowing it ran out. Instead the SDK stopped
// first, and its way of stopping is one more model call with no instruction
// attached: finding C-1, reintroduced on the one door whose deliverable is a
// file.
func TestTheAgentRunsUnderTheTurnsOwnIterationCeiling(t *testing.T) {
	chat := agentbudget.Default()
	document := chat.ForDocument()

	tests := []struct {
		name    string
		payload queue.ChatRunPayload
		want    int
	}{
		{
			name: "an ordinary turn",
			payload: queue.ChatRunPayload{
				CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
				Message: "What were our total sales last month?", UserMsgID: "msg-1", UserID: "user-1",
			},
			want: chat.MaxIterations,
		},
		{
			name: "a turn whose deliverable is a file",
			payload: queue.ChatRunPayload{
				CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelAPI,
				Message: "Total sales by month for the last six months.", UserMsgID: "msg-1",
				Directive:   ReportDirective(ReportDirectiveInput{Format: domain.DocumentFormatPDF}),
				APIReportID: "rep-1",
			},
			want: document.MaxIterations,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, spec := runnerForTurn(t, &directiveLLM{})
			if err := r.Run(context.Background(), tt.payload); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if spec.MaxIterations != tt.want {
				t.Errorf("the agent was built with %d iterations, want the turn's %d",
					spec.MaxIterations, tt.want)
			}
		})
	}

	if document.MaxIterations <= chat.MaxIterations {
		t.Fatal("setup: a document turn no longer has more iterations than a chat turn, " +
			"which is the difference this test exists to carry through to the SDK")
	}
}

// A tenant who tuned their budget gets that ceiling too — the resolver is the
// only place the number comes from, on both ends.
func TestATunedBudgetReachesTheAgent(t *testing.T) {
	r, spec := runnerForTurn(t, &directiveLLM{})
	r = r.WithBudget(func(context.Context, string) agentbudget.Budget {
		return agentbudget.Budget{MaxIterations: 5, MaxToolCalls: 6}
	})

	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
		Message: "What were our total sales last month?", UserMsgID: "msg-1", UserID: "user-1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if spec.MaxIterations != 5 {
		t.Errorf("the agent was built with %d iterations, want the tenant's 5", spec.MaxIterations)
	}
}
