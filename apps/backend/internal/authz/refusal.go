package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// Refusals (T-Z9, roadmap 12's decision 12): "a refusal nobody counts is a
// misconfiguration nobody finds."
//
// Deciding is Evaluate's; this file is the record of a decision that refused
// somebody. Every seam that refuses a person — or a door — one object calls
// Record with the authorizer it decided with, and that authorizer counts the
// refusal on /metrics and writes it to the audit log.
//
// **What is a refusal.** A request for one named object that was turned away
// because of who was asking: a route by id, a pick, a turn, an `@`, a room add, a
// conversation opened by id, a tool naming a dashboard or a document, a job
// firing. Not an object left out of a list — every render of a restricted
// workspace leaves some out, and a counter of that moves with page views. Not an
// object that does not exist, which is a mistyped id. And not a check that could
// not be made, which is an outage, logged at Warn where it happens and answered
// with a retry.

// Door is how a request reached the object it was refused: roadmap 12's §2 —
// the dashboard, `/v1`, the widget, the chat channels — plus the job a watcher
// or schedule fires as its creator (decision 11). Carried on a refusal so the
// audit row can say which door was closed, because "HR refused 40 times" means
// something different from a website and from a person.
type Door string

const (
	DoorDashboard Door = "dashboard"
	DoorAPIKey    Door = "api_key"
	DoorWidget    Door = "widget"
	DoorChannel   Door = "channel"
	DoorJob       Door = "job"
)

// AllDoors is every door, in §2's order.
var AllDoors = []Door{DoorDashboard, DoorAPIKey, DoorWidget, DoorChannel, DoorJob}

// DoorOf is the door a turn's channel came through, for the seams that know only
// the context — the tools.
//
// A channel the context does not carry reads as the dashboard, which is the only
// door on which a tool has a person to refuse. A scheduled task's turn carries
// its creator (access-grants §15c) and its thread's channel, so a tool refusing
// one is recorded on the dashboard door with the schedule as the actor.
func DoorOf(channel domain.Channel) Door {
	switch channel {
	case domain.ChannelDashboard, "":
		return DoorDashboard
	case domain.ChannelAPI:
		return DoorAPIKey
	case domain.ChannelWidget:
		return DoorWidget
	case domain.ChannelWhatsApp, domain.ChannelDiscord, domain.ChannelLark, domain.ChannelSlack:
		return DoorChannel
	default:
		return DoorJob
	}
}

const (
	// ReasonNotCleared — refused: restricted, on a door where nobody can hold a
	// grant. The widget, always (decision 10); a channel whose binding no admin
	// acknowledged (decision 8).
	ReasonNotCleared Reason = "not_cleared"
	// ReasonNotOnKey — refused: an API key limited to named agents, and this is
	// not one of them (decision 9). The agent's mode is not an input.
	ReasonNotOnKey Reason = "not_on_key"
	// ReasonCreatorRemoved — refused: a job's creator is no longer in the
	// workspace, on a restricted agent (decision 11).
	ReasonCreatorRemoved Reason = "creator_removed"
)

// refusalReasons are the reasons a refusal may be recorded with. Evaluate
// produces one of them; the other three are a door's own. ReasonOpen and
// ReasonGranted allowed something, and ReasonNotFound is a missing object —
// none of the three is a refusal of anybody.
var refusalReasons = []Reason{ReasonNotGranted, ReasonNotCleared, ReasonNotOnKey, ReasonCreatorRemoved}

// KindConversation is what a refusal names when the object refused was a
// conversation (T-Z10). A conversation is not a restrictable kind — it is hidden
// because an agent in it is — but a person opening one by id was refused a
// conversation, not an agent they never named, and the count should say so.
const KindConversation = "conversation"

// RefusalAuditTool is the audit row's tool name for a refusal. A pseudo tool,
// in the shape the acknowledgement row (`agent_binding.acknowledge_restricted`)
// and the action decisions use: nothing ran, and the row records who was turned
// away from what.
const RefusalAuditTool = "access.refused"

// Refusal is one request refused one object.
type Refusal struct {
	// Subject is the company and the person refused. UserID is empty on a door
	// with no person.
	Subject Subject
	// Kind is a domain.ResourceKind, or KindConversation.
	Kind       string
	ResourceID string
	Reason     Reason
	Door       Door
	// Channel is the channel the request arrived on, for the audit row.
	Channel domain.Channel
	// ActorKind and ActorRef are who the audit row names as accountable. Empty
	// means the person refused, as a user — every door with a person. A door
	// without one says who it was instead: a key, a visitor, a platform id.
	ActorKind domain.ActorKind
	ActorRef  string
}

// RefusalCounter is where a refusal is counted. *metrics.Collector is the
// production one.
type RefusalCounter interface {
	RecordAccessRefusal(kind, reason string)
}

// AuditWriter is where a refusal is written down: T-05's audit log.
// domain.AgentActionRepository satisfies it. Declared narrow so an authorizer
// cannot read the log it writes to.
type AuditWriter interface {
	Create(ctx context.Context, a *domain.AgentAction) error
}

// WithAudit writes every refusal this authorizer is told of to the audit log.
// Without it a refusal is counted and not written down.
func (a *Authorizer) WithAudit(w AuditWriter) *Authorizer {
	a.audit = w
	return a
}

// WithCounter replaces where refusals are counted. New counts on the process's
// collector, so no process that builds an authorizer can forget to; a test
// that wants to read its own count hands one in.
func (a *Authorizer) WithCounter(c RefusalCounter) *Authorizer {
	a.counter = c
	return a
}

// Record records a refusal through whatever decided it, when that can record.
//
// Every seam that refuses already holds the authorizer it decided with, behind
// an interface declared where it is consumed (AgentAccess, DashboardAccess,
// ResourceAuthorizer…) and satisfied in unit tests by fakes that decide and
// nothing more. Asking that value to record — rather than handing each of a
// dozen seams a second dependency — means a refusal is recorded by the object
// whose answer was acted on, with no second wiring for any of the three
// processes that build one to forget. A decider that cannot record is not
// recorded, and the negative suite (authztest) is what notices a seam that
// refused without calling this.
func Record(ctx context.Context, decider any, r Refusal) {
	if rec, ok := decider.(interface {
		Refused(context.Context, Refusal)
	}); ok {
		rec.Refused(ctx, r)
	}
}

// Refused counts a refusal and writes its audit row.
//
// The refusal has already happened by the time this runs, and nothing here can
// change it: a counter that is missing or an audit write that fails is logged,
// and the request is refused exactly as it would have been. Refusing to refuse
// because the record could not be written would open the object on a database
// blip — the one failure an access record must not cause.
//
// A refusal naming a kind or a reason outside the vocabulary is dropped, with a
// warning. Both are labels on a scraped series, and a label set that grows by
// whatever a caller passes is how an exporter becomes unbounded; a seam passing
// one is a bug the negative suite catches.
func (a *Authorizer) Refused(ctx context.Context, r Refusal) {
	if a == nil {
		return
	}
	if !refusable(r) {
		logrus.WithFields(logrus.Fields{
			"company_id": r.Subject.CompanyID, "resource_kind": r.Kind, "reason": r.Reason,
		}).Warn("a refusal named a kind or reason outside the vocabulary; not recorded")
		return
	}
	if a.counter != nil {
		a.counter.RecordAccessRefusal(r.Kind, string(r.Reason))
	}
	if a.audit == nil {
		return
	}
	// Detached, with its own deadline, for auditDecision's reason: a client that
	// hangs up on its 403 must not be how the record of it is lost.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := a.audit.Create(writeCtx, refusalRow(ctx, r)); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": r.Subject.CompanyID, "user_id": r.Subject.UserID,
			"resource_kind": r.Kind, "resource_id": r.ResourceID, "reason": r.Reason, "door": r.Door,
		}).Warn("access refusal not audited; the refusal stood")
	}
}

func refusable(r Refusal) bool {
	kindOK := r.Kind == KindConversation || domain.ResourceKind(r.Kind).Valid()
	return kindOK && slices.Contains(refusalReasons, r.Reason)
}

// refusalRow is the audit row: which object, why, through which door, and who.
// The agent and the conversation go in their own columns as well as the
// arguments, because "what was HR refused" is a WHERE clause the audit route
// already answers.
func refusalRow(ctx context.Context, r Refusal) *domain.AgentAction {
	args, _ := json.Marshal(map[string]string{
		"resource_kind": r.Kind,
		"resource_id":   r.ResourceID,
		"reason":        string(r.Reason),
		"door":          string(r.Door),
	})
	sum := sha256.Sum256(append([]byte(RefusalAuditTool+"|"), args...))
	actorKind, actorRef := r.ActorKind, r.ActorRef
	if actorKind == "" {
		actorKind, actorRef = domain.ActorKindUser, r.Subject.UserID
	}
	row := &domain.AgentAction{
		CompanyID:    r.Subject.CompanyID,
		ActorKind:    actorKind,
		ActorRef:     actorRef,
		Channel:      r.Channel,
		ToolName:     RefusalAuditTool,
		ArgsRedacted: args,
		ArgsHash:     hex.EncodeToString(sum[:]),
		ResultStatus: domain.ActionStatusBlocked,
		RequestID:    tenantctx.RequestID(ctx),
	}
	switch r.Kind {
	case string(domain.ResourceKindAgent):
		row.AgentID = r.ResourceID
	case KindConversation:
		row.ThreadID = r.ResourceID
	}
	return row
}

// processCounter is where New counts: the collector /metrics serves.
func processCounter() RefusalCounter { return metrics.Default() }
