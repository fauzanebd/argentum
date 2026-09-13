package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/authz/authztest"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z9's negative suite, for the surfaces internal/app owns: every point the
// enqueue path, a room, a job and a conversation can refuse. authztest.Table
// says what each must do under all eight cells; this file only says how to try.
//
// Every probe leaves HR open or restricts it, grants it to the cell's person or
// does not, and puts the real authorizer over that grant store behind the seam,
// recording into the cell's log.
func TestTheNegativeSuite(t *testing.T) {
	agent := string(domain.ResourceKindAgent)
	key := authztest.Key
	ctx := context.Background()

	authztest.Run(t, authztest.PackageApp, map[string]authztest.Probe{
		key(agent, authz.DoorDashboard, "a pick for a new conversation"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-fin", false, c, rec)
			_, err := f.enq.Enqueue(ctx, ChatInput{
				Channel: domain.ChannelDashboard, CompanyID: "co-1", UserID: c.Person(), AgentID: "ag-hr", Message: "payroll for August?",
			})
			return enqueued(t, err)
		},
		key(agent, authz.DoorDashboard, "a new conversation with no pick, when it is the default and nothing else is open"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-hr", true, c, rec)
			_, err := f.enq.Enqueue(ctx, ChatInput{
				Channel: domain.ChannelDashboard, CompanyID: "co-1", UserID: c.Person(), Message: "payroll for August?",
			})
			return enqueued(t, err)
		},
		key(agent, authz.DoorDashboard, "a turn in a conversation that runs as it"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-fin", false, c, rec, dashboardThread("th-hr", "ag-hr"))
			_, err := f.enq.Enqueue(ctx, ChatInput{
				Channel: domain.ChannelDashboard, CompanyID: "co-1", UserID: c.Person(), ThreadID: "th-hr", Message: "and September?",
			})
			return enqueued(t, err)
		},
		key(agent, authz.DoorDashboard, "adding it to a room"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			svc, _ := participantFixture(t, "ag-fin")
			grants := newAgentGrants("ag-fin", "ag-ops", "ag-hr")
			grantHR(grants, c)
			_, err := svc.WithAgentAccess(rec.Authorizer(grants)).Add(ctx, "co-1", "th-1", "ag-hr", c.Person())
			return enqueued(t, err)
		},
		key(agent, authz.DoorAPIKey, "a pick by a key with no agent list"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-fin", false, c, rec)
			in := apiTurn(nil, "ag-hr", "")
			in.APIKeyID = "key-1"
			_, err := f.enq.Enqueue(ctx, in)
			return enqueued(t, err)
		},
		key(agent, authz.DoorAPIKey, "a pick by a key that lists only another agent"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-fin", false, c, rec)
			in := apiTurn([]string{"ag-fin"}, "ag-hr", "")
			in.APIKeyID = "key-1"
			_, err := f.enq.Enqueue(ctx, in)
			return enqueued(t, err)
		},
		// A visitor whose ref is the granted person's id: a visitor is nobody, and
		// a string that matches a user id makes them nobody in particular.
		key(agent, authz.DoorWidget, "a visitor's pick"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-fin", false, c, rec)
			in := widgetTurn("ag-hr", "")
			in.EmbedUserRef = c.Person()
			_, err := f.enq.Enqueue(ctx, in)
			return enqueued(t, err)
		},
		key(agent, authz.DoorWidget, "a visitor with no pick, when it is the default and nothing else is open"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-hr", true, c, rec)
			in := widgetTurn("", "")
			in.EmbedUserRef = c.Person()
			_, err := f.enq.Enqueue(ctx, in)
			return enqueued(t, err)
		},
		key(agent, authz.DoorWidget, "a turn in a visitor's conversation that runs as it"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			th := &domain.ConversationThread{
				ID: "th-w", CompanyID: "co-1", Channel: domain.ChannelWidget, EmbedUserRef: c.Person(), AgentID: "ag-hr",
			}
			f := suiteEnqueuer(t, "ag-fin", false, c, rec, th)
			in := widgetTurn("", "th-w")
			in.EmbedUserRef = c.Person()
			_, err := f.enq.Enqueue(ctx, in)
			return enqueued(t, err)
		},
		key(agent, authz.DoorChannel, "a message on an address bound to it that no admin acknowledged"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-fin", false, c, rec)
			f.enq.WithChannelBindings(&stubBinder{bound: map[string]string{"co-1/discord/chan-hr": "ag-hr"}})
			in := discordIn("chan-hr")
			in.DiscordUserID = c.Person()
			_, err := f.enq.Enqueue(ctx, in)
			return enqueued(t, err)
		},
		key(agent, authz.DoorChannel, "a message on an address bound to it and acknowledged"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			f := suiteEnqueuer(t, "ag-fin", false, c, rec)
			f.enq.WithChannelBindings(&stubBinder{
				bound: map[string]string{"co-1/discord/chan-hr": "ag-hr"},
				acked: map[string]bool{"co-1/discord/chan-hr": true},
			})
			in := discordIn("chan-hr")
			in.DiscordUserID = c.Person()
			_, err := f.enq.Enqueue(ctx, in)
			return enqueued(t, err)
		},
		key(agent, authz.DoorJob, "a schedule firing as the person who made it"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			grants := newAgentGrants("ag-hr", "ag-fin")
			grantHR(grants, c)
			check := NewCreatorAccess(rec.Authorizer(grants),
				creatorUsers{c.Person(): activeUser(c.Person(), "co-1")},
				&stubDefaultAgent{agent: &domain.Agent{ID: "ag-hr"}},
				jobThreads{"th-job": {ID: "th-job", CompanyID: "co-1"}})
			reason, err := check.Check(ctx, "co-1", c.Person(), "th-job")
			if err != nil {
				t.Fatalf("the creator check failed: %v", err)
			}
			return reason == ""
		},
		// HR answered in this conversation and Finance runs it: T-Z10's third
		// source, the one a removed participant would have reopened.
		key(authz.KindConversation, authz.DoorDashboard, "opening one an agent in it answered"): func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
			grants := newAgentGrants("ag-hr", "ag-fin")
			grantHR(grants, c)
			loader := &conversationAgents{byThread: map[string][]string{"th-said": {"ag-fin", "ag-hr"}}}
			ca := NewConversationAccess(loader, rec.Authorizer(grants), &stubDefaultAgent{agent: &domain.Agent{ID: "ag-fin"}})
			ok, err := ca.MayRead(ctx, "co-1", c.Person(), "th-said")
			if err != nil {
				t.Fatalf("MayRead: %v", err)
			}
			return ok
		},
	})
}

// suiteEnqueuer is newAccessFixture with the cell put onto HR and the recording
// authorizer behind the enqueuer. othersClosed restricts every other agent to
// everyone, for the surfaces about a default with nothing to fall through to.
func suiteEnqueuer(
	t *testing.T, defaultID string, othersClosed bool, c authztest.Cell, rec *authztest.Recorder,
	existing ...*domain.ConversationThread,
) *accessFixture {
	t.Helper()
	f := newAccessFixture(t, defaultID, nil, existing...)
	grantHR(f.grants, c)
	if othersClosed {
		for _, id := range []string{"ag-fin", "ag-ops", "ag-legal"} {
			f.grants.restricted[id] = true
		}
	}
	// The roster in the order newAccessFixture lists it: the default first.
	ordered := rosterList{f.agents[defaultID]}
	for _, id := range []string{"ag-hr", "ag-fin", "ag-ops", "ag-legal"} {
		if id != defaultID {
			ordered = append(ordered, f.agents[id])
		}
	}
	f.enq.WithAgentAccess(rec.Authorizer(f.grants), ordered)
	return f
}

// grantHR puts a cell onto HR.
func grantHR(g *agentGrants, c authztest.Cell) {
	g.restricted["ag-hr"] = c.Restricted
	if c.Granted {
		g.granted[c.Person()+"/ag-hr"] = true
	}
}

// enqueued is whether a request got through. A check that could not be made is
// a broken probe rather than a refusal: every grant store here answers.
func enqueued(t *testing.T, err error) bool {
	t.Helper()
	if errors.Is(err, ErrAccessCheckFailed) {
		t.Fatalf("the access check failed: %v", err)
	}
	return err == nil
}
