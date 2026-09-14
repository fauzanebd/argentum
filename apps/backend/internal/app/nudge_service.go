package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/taint"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tools"
)

// The lines a room writes about itself (T-N6): messages the product puts into a
// conversation about the conversation, beside the answers agents give in it.
//
// They ride `messages.metadata` under RoomEventKey, as next_steps and the
// grounding record do, so they need no migration and no new row type. The
// content of each is a whole sentence, so a renderer that has never heard of
// them still reads correctly; the dashboard draws the three that are not an
// agent's words as a quiet line rather than a bubble.
//
// **A settle and an unasked question are deliberately two things** (roadmap 09,
// decision 9, from Hermes' `group-rounds.ts:500-503`). A colleague with nothing
// to add is a conversation ending well; a limit refusing a question is one being
// cut short. A room that drew both the same way would tell the person nothing
// about which had happened.
//
// None of them is replayed into a model's history (hydrateMemory): each is the
// product's sentence, and an agent reading "→ Finance: …" as something it once
// wrote is an agent learning to type a hand-off instead of calling the tool.
const (
	RoomEventKey = "room_event"
	// RoomEventNudge is one agent's question to another, stored as the asker's
	// message. Decision 6: a nudge is a visible message, never a side channel.
	RoomEventNudge = "nudge"
	// RoomEventUnasked is a question the conversation budget refused, quoted so
	// the person can ask it themselves.
	RoomEventUnasked = "unasked"
	// RoomEventSettle is an asked agent with nothing to add.
	RoomEventSettle = "settle"
	// RoomEventWithdrawn is a question whose recipient left the room between
	// being asked and answering.
	RoomEventWithdrawn = "withdrawn"
)

// ChatEventRoom is the event a room line is published as. Not `final`: a
// `final` carrying the asker's agent id would close the asker's own bubble while
// its turn is still streaming, because the dashboard keys a live turn by job and
// agent.
const ChatEventRoom = "room_event"

// ParticipantLister is who is in a conversation, default speaker included
// (T-N2). *Room satisfies it.
//
// Declared at the consumer and read-only, like RoomReader: a nudge that could
// add a participant is a nudge that could enlarge a room, which decision 7
// forbids.
type ParticipantLister interface {
	ListParticipants(ctx context.Context, companyID, threadID string) ([]*domain.ThreadParticipant, error)
}

// RoomNoteWriter is how a line reaches a room. *ThreadService satisfies it, and
// stamps the message with the agent on the context (T-N1) — so the asker's
// question is recorded as the asker's without anybody passing an id.
type RoomNoteWriter interface {
	AppendAssistantMessage(ctx context.Context, threadID, content string,
		tokensIn, tokensOut int, latencyMs int64, metadata map[string]any) (*domain.Message, error)
}

// NudgeService is `nudge_agent` (T-N6), everything but the argument parsing: who
// may ask whom, what it costs, and what the room shows.
//
// **The order of the checks is the order of the answers a model should get.**
//  1. The gate, re-checked at dispatch (decision 8): the asking agent may nudge,
//     and the conversation holds more than one participant. The runner already
//     withholds the tool otherwise, and this is not redundant — a model that saw
//     the tool in one conversation will try it in another, which is why Hermes
//     checks at dispatch too (`bot_mode_dm.py:185-190`).
//  2. Who is asked: a current participant (decision 7), not the asker, enabled,
//     and this company's. Each refusal names who *can* be asked.
//  3. The credit balance, before the ledger, so a tenant at zero reads the credit
//     refusal and not the loop guard's (T-N8's notes: two failures, two sentences).
//  4. The conversation budget, which counts the turn if it admits it.
//
// Only then is anything written: the question into the room, as the asker's
// message, and one ordinary `chat:run` for the colleague — no new task type
// (decision 4). The asker is not resumed when the answer arrives; the person
// reads both, and a resume is another turn nobody addressed.
type NudgeService struct {
	roster   AgentLoader
	room     ParticipantLister
	notes    RoomNoteWriter
	enqueuer ChatRunEnqueuer
	// ledger is T-N8's conversation budget. A nil ledger refuses every ask,
	// because agentbudget.Conversation.Admit fails closed — a deployment that
	// wired nudging and not its loop guard gets no nudging.
	ledger *agentbudget.Conversation
	budget BudgetChecker
	bus    EventBus
}

// NewNudgeService wires the service. The bus arrives later, through SetBus.
func NewNudgeService(
	roster AgentLoader, room ParticipantLister, notes RoomNoteWriter,
	enqueuer ChatRunEnqueuer, ledger *agentbudget.Conversation,
) *NudgeService {
	return &NudgeService{roster: roster, room: room, notes: notes, enqueuer: enqueuer, ledger: ledger}
}

// WithBudget refuses a question a tenant cannot pay for, before the conversation
// budget is asked. The same checker ChatEnqueuer.WithBudget holds, so a nudged
// turn and an addressed one are refused by the same rule.
func (s *NudgeService) WithBudget(b BudgetChecker) *NudgeService {
	s.budget = b
	return s
}

// SetBus completes the service with the event bus, which the worker builds after
// the stack — ActionMessenger.SetWhatsApp's arrangement, for the same reason.
// Without one a room line is written and appears on the next load.
func (s *NudgeService) SetBus(b EventBus) { s.bus = b }

type askingTurnKey struct{}

// withAskingTurn puts the payload a turn runs on where nudge_agent can read it.
//
// A nudged turn is the asking turn's payload with a handful of things changed —
// who runs it, what it is asked, who asked and at what depth — and everything
// else the same: the company's name and currency convention, the person it runs
// on behalf of, the channel and its reply refs, the request id. A tool reaches
// none of that through its arguments and tenantctx carries only some of it, so
// the runner hands over the payload whole rather than a list of fields the next
// field added to the payload would be missing from.
func withAskingTurn(ctx context.Context, p queue.ChatRunPayload) context.Context {
	return context.WithValue(ctx, askingTurnKey{}, p)
}

func askingTurnFrom(ctx context.Context) (queue.ChatRunPayload, bool) {
	p, ok := ctx.Value(askingTurnKey{}).(queue.ChatRunPayload)
	return p, ok
}

const nudgeUnavailable = "Asking another agent is not available in this conversation. Answer from your own tools."

// Nudge asks one colleague one question on behalf of the turn on ctx, and
// returns what the asking model reads.
func (s *NudgeService) Nudge(ctx context.Context, agent, question string) string {
	p, ok := askingTurnFrom(ctx)
	companyID, askerID := tenantctx.CompanyID(ctx), agentscope.AgentID(ctx)
	if !ok || companyID == "" || askerID == "" || p.ThreadID == "" {
		return tools.NudgeRefusal("not_available", nudgeUnavailable)
	}
	asker, err := s.roster.GetByID(ctx, companyID, askerID)
	if err != nil || !asker.CanNudge {
		return tools.NudgeRefusal("not_available", nudgeUnavailable)
	}
	room, err := s.room.ListParticipants(ctx, companyID, p.ThreadID)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "thread_id": p.ThreadID,
		}).Warn("nudge: room lookup failed; nobody was asked")
		return tools.NudgeRefusal("room_unreadable",
			"Who is in this conversation could not be read, so nobody was asked. Answer from your own tools.")
	}
	if len(room) < 2 {
		return tools.NudgeRefusal("not_available", nudgeUnavailable)
	}

	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(agent), "@"))
	question = strings.TrimSpace(question)
	switch {
	case name == "":
		return tools.NudgeRefusal("missing_agent", "Name the colleague to ask. "+canAsk(room, askerID))
	case question == "":
		return tools.NudgeRefusal("missing_question", "Say what you want to ask them, as one question.")
	case utf8.RuneCountInString(question) > tools.NudgeQuestionMax:
		return tools.NudgeRefusal("question_too_long", fmt.Sprintf(
			"Ask one question of at most %d characters. Your colleague reads this conversation for the "+
				"context; the question only has to say what you need.", tools.NudgeQuestionMax))
	}

	target := participantNamed(room, name)
	if target == nil {
		return notInRoom(name, room, askerID)
	}
	if target.AgentID == askerID {
		return tools.NudgeRefusal("cannot_ask_yourself", "You cannot ask yourself. "+canAsk(room, askerID))
	}
	// The row, not the participant list, decides the last two: a participant row
	// names an agent by id, and only the company-scoped roster read can say that
	// the agent is this company's and still in service. AgentForChannel's rule —
	// disabling is how an admin takes an agent out of service — applies here too.
	targetRow, err := s.roster.GetByID(ctx, companyID, target.AgentID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return notInRoom(name, room, askerID, target.AgentID)
	case err != nil:
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "agent_id": target.AgentID,
		}).Warn("nudge: colleague lookup failed; nobody was asked")
		return tools.NudgeRefusal("room_unreadable",
			"That colleague could not be looked up, so nobody was asked. Answer from your own tools.")
	case !targetRow.Enabled:
		return tools.NudgeRefusal("agent_disabled", fmt.Sprintf(
			"%s has been disabled by an admin and cannot be asked. %s",
			targetRow.Name, canAsk(room, askerID, target.AgentID)))
	}

	if s.budget != nil {
		st, err := s.budget.CheckBudget(ctx, companyID)
		if err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("nudge: credit check failed; the question was not asked")
			return tools.NudgeRefusal("credits_unchecked", fmt.Sprintf(
				"This workspace's credit balance could not be checked, so %s was not asked. Answer from what you have.",
				targetRow.Name))
		}
		if st.Blocked() {
			return tools.NudgeRefusal("credits_exhausted", fmt.Sprintf(
				"%s was not asked. %s Answer from what you have, and do not try to ask again.",
				targetRow.Name, CreditsExhaustedMessage))
		}
	}

	depth := askDepth(p)
	v, err := s.ledger.Admit(ctx, agentbudget.Ask{
		CompanyID: companyID, UserMsgID: p.UserMsgID,
		AgentID: targetRow.ID, Question: question, Depth: depth,
	})
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		}).Warn("nudge: conversation budget unavailable; the question was refused")
	}
	if v.Repeat {
		return v.ToolResult(targetRow.Name, question)
	}
	if !v.Admitted {
		s.noteUnasked(ctx, p, asker, targetRow, question, v)
		return v.ToolResult(targetRow.Name, question)
	}

	notDelivered := tools.NudgeRefusal("not_delivered", fmt.Sprintf(
		"%s could not be asked — the question was not delivered. Answer without it, and say what is left unanswered.",
		targetRow.Name))
	// Written before the turn is queued, never after. The other order can end with
	// an answer in the room to a question nobody can see — the side channel
	// decision 6 rules out. This order can end, on a queue failure, with a
	// question nobody answers, which the asking model is told about and which
	// reads as exactly what happened.
	meta := map[string]any{
		RoomEventKey: RoomEventNudge, "to_agent_id": targetRow.ID, "to_agent_name": targetRow.Name, "depth": depth,
	}
	if err := s.announce(ctx, p, fmt.Sprintf("→ %s: %s", targetRow.Name, question), meta); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "thread_id": p.ThreadID,
		}).Error("nudge: the question could not be written to the room; nothing was queued")
		return notDelivered
	}
	// The taint is read now, at the moment the tool runs, so everything the asker
	// had read by then crosses (T-N5): a supplier PDF the asker summarised gates
	// the colleague's actions exactly as it gates the asker's.
	asked := nudgedTurn(p, askerID, target.ID, targetRow.ID, question, depth, taint.FromContext(ctx).Carry())
	taskID, err := s.enqueuer.EnqueueChatRun(ctx, asked)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "thread_id": p.ThreadID, "agent_id": targetRow.ID,
		}).Error("nudge: the question is in the room but its turn was not queued")
		return notDelivered
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		"asker_agent_id": askerID, "agent_id": targetRow.ID,
		"depth": depth, "conversation_turns": v.Turns, "task_id": taskID,
	}).Info("agent asked a colleague")
	return askedResult(targetRow.Name)
}

// noteUnasked writes a refused question into the room — **once per agent per
// person's message**, which is the decision T-N8 left to this ticket.
//
// Once per refused question would let a model that keeps asking after being told
// not to fill the room with limit notices, one per attempt. Once per message
// would tell the person about the first agent cut short and say nothing about a
// second one, whose unasked question is just as much theirs to ask. One line per
// asker is the smallest room that still names every agent that went unanswered,
// and it bounds a looping agent at one line.
//
// **An outage writes it anyway.** When the claim cannot be recorded the notice
// is written unrecorded, so a Redis failure cannot hide a refusal from the person;
// what bounds the repeats then is the per-turn tool-call ceiling (T-16), because
// every one of these arrives through a tool call.
func (s *NudgeService) noteUnasked(
	ctx context.Context, p queue.ChatRunPayload, asker, target *domain.Agent, question string, v agentbudget.Verdict,
) {
	first, err := s.ledger.ClaimNotice(ctx, p.CompanyID, p.UserMsgID, asker.ID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", p.CompanyID).
			Warn("nudge: could not record the room notice; writing it anyway")
	} else if !first {
		return
	}
	meta := map[string]any{
		RoomEventKey: RoomEventUnasked, "to_agent_id": target.ID, "to_agent_name": target.Name,
		"dimension": string(v.Dimension),
	}
	if err := s.announce(ctx, p, v.Notice(asker.Name, target.Name, question), meta); err != nil {
		logrus.WithError(err).WithField("thread_id", p.ThreadID).
			Warn("nudge: an unasked question could not be written to the room")
	}
}

// announce writes one room line as the agent on ctx and publishes it.
func (s *NudgeService) announce(ctx context.Context, p queue.ChatRunPayload, content string, meta map[string]any) error {
	if _, err := s.notes.AppendAssistantMessage(ctx, p.ThreadID, content, 0, 0, 0, meta); err != nil {
		return err
	}
	if s.bus == nil {
		return nil
	}
	sc := agentscope.FromContext(ctx)
	if err := s.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: ChatEventRoom,
		Content: content, Metadata: meta,
		AgentID: sc.AgentID, AgentName: sc.Name, Timestamp: time.Now(),
	}); err != nil {
		logrus.WithError(err).WithField("thread_id", p.ThreadID).
			Warn("nudge: a room line was written but not published; it appears on the next load")
	}
	return nil
}

// nudgedTurn is the colleague's turn: the asking turn's payload, re-aimed.
//
// What the asking turn was *for* is cleared, because the asked turn is not for
// it. A report job, a scheduled run and a watcher briefing are each closed out
// by the turn they started — a colleague's answer must not complete the report
// or be delivered as the briefing. The directive is cleared for T-N5's reason,
// and the runner drops it again on arrival regardless. The trace and the enqueue
// time are the queue's to stamp.
func nudgedTurn(
	p queue.ChatRunPayload, askerID, participantID, targetID, question string,
	depth int, carried map[taint.Kind][]string,
) queue.ChatRunPayload {
	n := p
	n.AgentID = targetID
	n.Message = question
	n.Peer = &queue.PeerOrigin{
		AgentID: askerID, Taint: carried, ParticipantID: participantID, Depth: depth,
	}
	n.Directive = ""
	n.APIReportID = ""
	n.ScheduledTaskID, n.ScheduledRunID = "", ""
	n.WatcherEventID = ""
	n.Trace = nil
	n.EnqueuedAt = time.Time{}
	return n
}

// askDepth is the hop an ask from this turn starts: 1 from a turn the person
// addressed, one deeper than the asking turn otherwise. A peer turn recorded
// with no depth — one queued before the field existed, or built by hand — is
// read as the first hop, so nothing that asks is ever mistaken for the person.
func askDepth(p queue.ChatRunPayload) int {
	if p.Peer == nil {
		return 1
	}
	return max(p.Peer.Depth, 1) + 1
}

// participantNamed finds a participant by roster name, case-insensitively — the
// addressing grammar's rule (Appendix A), because the model has names, not ids.
func participantNamed(room []*domain.ThreadParticipant, name string) *domain.ThreadParticipant {
	for _, p := range room {
		if strings.EqualFold(strings.TrimSpace(p.AgentName), name) {
			return p
		}
	}
	return nil
}

func notInRoom(name string, room []*domain.ThreadParticipant, exclude ...string) string {
	return tools.NudgeRefusal("not_in_conversation", fmt.Sprintf(
		"Nobody called %q is in this conversation, so nobody was asked. %s", name, canAsk(room, exclude...)))
}

// canAsk names who can be asked, load_skill's habit: a refusal that lists the
// alternatives is one the model corrects from in a single call.
func canAsk(room []*domain.ThreadParticipant, exclude ...string) string {
	var names []string
	for _, p := range room {
		if slices.Contains(exclude, p.AgentID) || strings.TrimSpace(p.AgentName) == "" {
			continue
		}
		names = append(names, p.AgentName)
	}
	if len(names) == 0 {
		return "Nobody else in this conversation can be asked."
	}
	return "You can ask: " + strings.Join(names, ", ") + "."
}

func askedResult(name string) string {
	out, err := json.Marshal(map[string]any{
		"asked": true,
		"agent": name,
		"note": fmt.Sprintf("Asked %s. The question is posted in this conversation and their answer will "+
			"appear after your reply — not in this turn. Do not wait for it, do not guess what they will say, "+
			"and do not state any figure you asked them for. Finish your own answer now, and say that you "+
			"asked %s and what.", name, name),
	})
	if err != nil {
		return `{"asked":true}`
	}
	return string(out)
}
