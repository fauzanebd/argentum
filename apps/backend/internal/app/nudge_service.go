package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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
//
// A hand-off (T-N7) is not one of these, though it is written the same way: it
// is the handing agent's reply to the person, and an answer to a caller who is
// waiting for one. See HandedOffToKey.
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
//
// Append-only, and that is also T-N7's "`threads.agent_id` is unchanged after a
// hand-off": nothing this service holds can write a thread row. The default
// speaker is the tenant's setting, not a thing a model may edit.
type RoomNoteWriter interface {
	AppendAssistantMessage(ctx context.Context, threadID, content string,
		tokensIn, tokensOut int, latencyMs int64, metadata map[string]any) (*domain.Message, error)
}

// NudgeService is `nudge_agent` (T-N6) and `hand_off_to_agent` (T-N7),
// everything but the argument parsing: who may ask whom, what it costs, and what
// the room shows.
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
//
// The two tools share every one of those steps, and differ in what they send and
// in what becomes of the asking turn — see HandOff.
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

// askingTurn is the turn a room tool runs in: the payload it runs on, and the one
// thing it can have done that changes what it may still do — handed the question
// on (T-N7).
//
// A pointer on the context, so what a tool records the runner reads after the
// model returns. Guarded, because a model can return several tool calls in one
// response.
type askingTurn struct {
	payload queue.ChatRunPayload

	mu        sync.Mutex
	handedOff *handOff
}

// handOff is a question this turn passed on (T-N7), recorded only once it was
// delivered — the line written and the colleague's turn queued. The line is the
// turn's reply, and the runner closes the turn on it.
type handOff struct {
	to      string
	content string
	message *domain.Message
}

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
	return context.WithValue(ctx, askingTurnKey{}, &askingTurn{payload: p})
}

func askingTurnFrom(ctx context.Context) (queue.ChatRunPayload, bool) {
	st := turnOf(ctx)
	if st == nil {
		return queue.ChatRunPayload{}, false
	}
	return st.payload, true
}

func turnOf(ctx context.Context) *askingTurn {
	st, _ := ctx.Value(askingTurnKey{}).(*askingTurn)
	return st
}

// handedOffIn is the hand-off the turn on ctx delivered, or nil.
func handedOffIn(ctx context.Context) *handOff {
	st := turnOf(ctx)
	if st == nil {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.handedOff
}

const (
	nudgeUnavailable   = "Asking another agent is not available in this conversation. Answer from your own tools."
	handOffUnavailable = "Handing a question to another agent is not available in this conversation. Answer from your own tools."
)

// askFrom is a turn that has passed the gate: which turn, in which company, as
// which agent, in which room.
type askFrom struct {
	turn      *askingTurn
	companyID string
	asker     *domain.Agent
	room      []*domain.ThreadParticipant
}

// gate is decision 8's two gates, re-checked at dispatch, for either tool. The
// refusal is "" when the turn may go on.
func (s *NudgeService) gate(ctx context.Context, unavailable string) (askFrom, string) {
	st := turnOf(ctx)
	companyID, askerID := tenantctx.CompanyID(ctx), agentscope.AgentID(ctx)
	if st == nil || companyID == "" || askerID == "" || st.payload.ThreadID == "" {
		return askFrom{}, tools.NudgeRefusal("not_available", unavailable)
	}
	asker, err := s.roster.GetByID(ctx, companyID, askerID)
	if err != nil || !asker.CanNudge {
		return askFrom{}, tools.NudgeRefusal("not_available", unavailable)
	}
	room, err := s.room.ListParticipants(ctx, companyID, st.payload.ThreadID)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "thread_id": st.payload.ThreadID,
		}).Warn("nudge: room lookup failed; nobody was asked")
		return askFrom{}, tools.NudgeRefusal("room_unreadable",
			"Who is in this conversation could not be read, so nobody was asked. Answer from your own tools.")
	}
	if len(room) < 2 {
		return askFrom{}, tools.NudgeRefusal("not_available", unavailable)
	}
	return askFrom{turn: st, companyID: companyID, asker: asker, room: room}, ""
}

// colleague resolves the participant a model named: in the room, not the asker,
// this company's and enabled. The refusal is "" when it resolved.
func (s *NudgeService) colleague(
	ctx context.Context, from askFrom, name string,
) (*domain.ThreadParticipant, *domain.Agent, string) {
	target := participantNamed(from.room, name)
	if target == nil {
		return nil, nil, notInRoom(name, from.room, from.asker.ID)
	}
	if target.AgentID == from.asker.ID {
		return nil, nil, tools.NudgeRefusal("cannot_ask_yourself",
			"You cannot ask yourself. "+canAsk(from.room, from.asker.ID))
	}
	// The row, not the participant list, decides the last two: a participant row
	// names an agent by id, and only the company-scoped roster read can say that
	// the agent is this company's and still in service. AgentForChannel's rule —
	// disabling is how an admin takes an agent out of service — applies here too.
	targetRow, err := s.roster.GetByID(ctx, from.companyID, target.AgentID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, nil, notInRoom(name, from.room, from.asker.ID, target.AgentID)
	case err != nil:
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": from.companyID, "agent_id": target.AgentID,
		}).Warn("nudge: colleague lookup failed; nobody was asked")
		return nil, nil, tools.NudgeRefusal("room_unreadable",
			"That colleague could not be looked up, so nobody was asked. Answer from your own tools.")
	case !targetRow.Enabled:
		return nil, nil, tools.NudgeRefusal("agent_disabled", fmt.Sprintf(
			"%s has been disabled by an admin and cannot be asked. %s",
			targetRow.Name, canAsk(from.room, from.asker.ID, target.AgentID)))
	}
	return target, targetRow, ""
}

// credits is the credit check, ahead of the ledger. "" when the tenant can pay.
func (s *NudgeService) credits(ctx context.Context, companyID, name string) string {
	if s.budget == nil {
		return ""
	}
	st, err := s.budget.CheckBudget(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("nudge: credit check failed; the question was not asked")
		return tools.NudgeRefusal("credits_unchecked", fmt.Sprintf(
			"This workspace's credit balance could not be checked, so %s was not asked. Answer from what you have.",
			name))
	}
	if st.Blocked() {
		return tools.NudgeRefusal("credits_exhausted", fmt.Sprintf(
			"%s was not asked. %s Answer from what you have, and do not try to ask again.",
			name, CreditsExhaustedMessage))
	}
	return ""
}

// Nudge asks one colleague one question on behalf of the turn on ctx, and
// returns what the asking model reads.
func (s *NudgeService) Nudge(ctx context.Context, agent, question string) string {
	from, refused := s.gate(ctx, nudgeUnavailable)
	if refused != "" {
		return refused
	}
	p := from.turn.payload

	name := addressedName(agent)
	question = strings.TrimSpace(question)
	switch {
	case name == "":
		return tools.NudgeRefusal("missing_agent", "Name the colleague to ask. "+canAsk(from.room, from.asker.ID))
	case question == "":
		return tools.NudgeRefusal("missing_question", "Say what you want to ask them, as one question.")
	case utf8.RuneCountInString(question) > tools.NudgeQuestionMax:
		return tools.NudgeRefusal("question_too_long", fmt.Sprintf(
			"Ask one question of at most %d characters. Your colleague reads this conversation for the "+
				"context; the question only has to say what you need.", tools.NudgeQuestionMax))
	}
	// A turn that handed the question on is done with it (T-N7). Asking somebody
	// about a question it has just said is not its own spends a colleague's turn
	// on work the colleague it was handed to is already doing.
	if h := handedOffIn(ctx); h != nil {
		return tools.NudgeRefusal("handed_off", fmt.Sprintf(
			"You handed this question to %s, so it is theirs now and nobody was asked. Your turn is over: "+
				"call no more tools.", h.to))
	}

	target, targetRow, refused := s.colleague(ctx, from, name)
	if refused != "" {
		return refused
	}
	if refused := s.credits(ctx, from.companyID, targetRow.Name); refused != "" {
		return refused
	}

	depth := askDepth(p)
	v, err := s.ledger.Admit(ctx, agentbudget.Ask{
		CompanyID: from.companyID, UserMsgID: p.UserMsgID,
		AgentID: targetRow.ID, Question: question, Depth: depth,
	})
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": from.companyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		}).Warn("nudge: conversation budget unavailable; the question was refused")
	}
	if v.Repeat {
		return v.ToolResult(targetRow.Name, question)
	}
	if !v.Admitted {
		s.noteUnasked(ctx, p, from.asker, targetRow, v.Notice(from.asker.Name, targetRow.Name, question), v)
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
	if _, err := s.announce(ctx, p, fmt.Sprintf("→ %s: %s", targetRow.Name, question), meta); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": from.companyID, "thread_id": p.ThreadID,
		}).Error("nudge: the question could not be written to the room; nothing was queued")
		return notDelivered
	}
	// The taint is read now, at the moment the tool runs, so everything the asker
	// had read by then crosses (T-N5): a supplier PDF the asker summarised gates
	// the colleague's actions exactly as it gates the asker's.
	asked := nudgedTurn(p, from.asker.ID, target.ID, targetRow.ID, question, depth, taint.FromContext(ctx).Carry())
	taskID, err := s.enqueuer.EnqueueChatRun(ctx, asked)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": from.companyID, "thread_id": p.ThreadID, "agent_id": targetRow.ID,
		}).Error("nudge: the question is in the room but its turn was not queued")
		return notDelivered
	}
	logrus.WithFields(logrus.Fields{
		"company_id": from.companyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		"asker_agent_id": from.asker.ID, "agent_id": targetRow.ID,
		"depth": depth, "conversation_turns": v.Turns, "task_id": taskID,
	}).Info("agent asked a colleague")
	return askedResult(targetRow.Name)
}

// HandOff passes the person's question, on behalf of the turn on ctx, to the one
// colleague it belongs to, and returns what the handing model reads (T-N7).
//
// nudge_agent's gate, colleague, credits and ledger, in nudge_agent's order —
// the ticket's "same flag, same participant rule, same budget" — and three
// rules of its own:
//   - **Only a turn holding the person's question may hand it on**: one the
//     person addressed, or one a colleague handed it to. A colleague's question
//     is the colleague's words, and passing them on would put them in front of a
//     third agent as though the person had asked. A turn asked one has PASS for
//     "not mine" and nudge_agent for "somebody else knows". The runner does not
//     offer the tool there; this is the dispatch re-check.
//   - **What is sent is the handing turn's own message, never the model's
//     paraphrase.** The model names a colleague and gives a reason; the question
//     travels from the payload. So the ledger's repeat check sees the very words
//     the person's message opened the conversation with, and a question handed
//     straight back to an agent that already had it is a repeat — refused, with
//     nothing queued, by the ledger rather than by a rule about going back.
//   - **Delivered, the turn is over.** The room line is the turn's reply, and the
//     runner closes the turn on it rather than on anything the model writes
//     afterwards. "Attempts no answer" is enforced there, not asked for.
func (s *NudgeService) HandOff(ctx context.Context, agent, reason string) string {
	from, refused := s.gate(ctx, handOffUnavailable)
	if refused != "" {
		return refused
	}
	p := from.turn.payload
	if !holdsPersonsQuestion(p) {
		return tools.NudgeRefusal("not_available",
			"You were asked a colleague's question, not handed the person's, so there is nothing of theirs to "+
				"hand on. If you have nothing to add, reply "+peerPass+"; if another colleague holds the answer, "+
				"ask them with nudge_agent.")
	}

	name := addressedName(agent)
	reason = strings.TrimSpace(reason)
	switch {
	case name == "":
		return tools.NudgeRefusal("missing_agent",
			"Name the colleague to hand the question to. "+canAsk(from.room, from.asker.ID))
	case reason == "":
		return tools.NudgeRefusal("missing_reason", "Say in one sentence why the question is theirs.")
	case utf8.RuneCountInString(reason) > tools.HandOffReasonMax:
		return tools.NudgeRefusal("reason_too_long", fmt.Sprintf(
			"Give the reason in at most %d characters. Your colleague receives the person's own words; the "+
				"reason only has to say why the question is theirs.", tools.HandOffReasonMax))
	}

	// Held to the end, so two hand-offs in one model response cannot both be
	// delivered: one question, one colleague.
	from.turn.mu.Lock()
	defer from.turn.mu.Unlock()
	if h := from.turn.handedOff; h != nil {
		return tools.NudgeRefusal("already_handed_off", fmt.Sprintf(
			"You have already handed this question to %s, and nothing more was sent. Your turn is over: do not "+
				"answer the question, and call no more tools.", h.to))
	}

	target, targetRow, refused := s.colleague(ctx, from, name)
	if refused != "" {
		return refused
	}
	if refused := s.credits(ctx, from.companyID, targetRow.Name); refused != "" {
		return refused
	}

	question, depth := p.Message, askDepth(p)
	v, err := s.ledger.Admit(ctx, agentbudget.Ask{
		CompanyID: from.companyID, UserMsgID: p.UserMsgID,
		AgentID: targetRow.ID, Question: question, Depth: depth,
	})
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": from.companyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		}).Warn("hand-off: conversation budget unavailable; the question was not handed over")
	}
	if v.Repeat {
		return handOffRepeat(targetRow.Name)
	}
	if !v.Admitted {
		s.noteUnasked(ctx, p, from.asker, targetRow, fmt.Sprintf("%s could not hand the question to %s — %s: %q",
			from.asker.Name, targetRow.Name, v.Reason, question), v)
		return v.ToolResult(targetRow.Name, question)
	}

	notDelivered := tools.NudgeRefusal("not_delivered", fmt.Sprintf(
		"The question could not be handed to %s — it was not delivered. Answer it yourself from your own "+
			"tools, and say what is left unanswered.", targetRow.Name))
	// Nudge's order, for Nudge's reason: the line the person reads is written
	// before the colleague's turn exists, so an answer can never arrive for a
	// hand-off nobody can see.
	content := fmt.Sprintf("Passed to %s: %s", targetRow.Name, reason)
	meta := map[string]any{HandedOffToKey: targetRow.ID, "to_agent_name": targetRow.Name, "depth": depth}
	msg, err := s.announce(ctx, p, content, meta)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": from.companyID, "thread_id": p.ThreadID,
		}).Error("hand-off: the line could not be written to the room; nothing was queued")
		return notDelivered
	}
	handed := nudgedTurn(p, from.asker.ID, target.ID, targetRow.ID, question, depth, taint.FromContext(ctx).Carry())
	handed.Peer.HandOff = &queue.HandOff{Reason: reason}
	taskID, err := s.enqueuer.EnqueueChatRun(ctx, handed)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": from.companyID, "thread_id": p.ThreadID, "agent_id": targetRow.ID,
		}).Error("hand-off: the line is in the room but the colleague's turn was not queued")
		return notDelivered
	}
	from.turn.handedOff = &handOff{to: targetRow.Name, content: content, message: msg}
	logrus.WithFields(logrus.Fields{
		"company_id": from.companyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		"asker_agent_id": from.asker.ID, "agent_id": targetRow.ID,
		"depth": depth, "conversation_turns": v.Turns, "task_id": taskID,
	}).Info("agent handed a question to a colleague")
	return handedOffResult(targetRow.Name)
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
//
// notice is the sentence: Verdict.Notice for a question, and the hand-off's own
// for a hand-off, which passed on nobody's question but the person's.
func (s *NudgeService) noteUnasked(
	ctx context.Context, p queue.ChatRunPayload, asker, target *domain.Agent, notice string, v agentbudget.Verdict,
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
	if _, err := s.announce(ctx, p, notice, meta); err != nil {
		logrus.WithError(err).WithField("thread_id", p.ThreadID).
			Warn("nudge: an unasked question could not be written to the room")
	}
}

// announce writes one room line as the agent on ctx and publishes it.
func (s *NudgeService) announce(
	ctx context.Context, p queue.ChatRunPayload, content string, meta map[string]any,
) (*domain.Message, error) {
	msg, err := s.notes.AppendAssistantMessage(ctx, p.ThreadID, content, 0, 0, 0, meta)
	if err != nil {
		return nil, err
	}
	if s.bus == nil {
		return msg, nil
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
	return msg, nil
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

// addressedName is a colleague's name as a model sent it, without the `@` a
// model borrows from the person's own addressing.
func addressedName(agent string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(agent), "@"))
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

// handedOffResult tells the handing model its turn is over (T-N7). It says so
// because it is true, not because it is relied on: whatever the model writes
// after this is not published (ChatRunner.handedOver), and asking it to stop
// only saves the iterations it would spend being ignored.
func handedOffResult(name string) string {
	out, err := json.Marshal(map[string]any{
		"handed_off": true,
		"agent":      name,
		"note": fmt.Sprintf("Handed to %s. The conversation shows that you passed the question on, and %s "+
			"answers it in a turn of their own. Your turn is over: do not answer the question, call no more "+
			"tools, and end with one short sentence.", name, name),
	})
	if err != nil {
		return `{"handed_off":true}`
	}
	return string(out)
}

// handOffRepeat is a hand-off the ledger has already seen from this message:
// the colleague was given this exact question — addressed it by the person, or
// handed it here itself (T-N7).
//
// It carries `error`, unlike a nudge's repeat. A repeated nudge is a question
// already on its way; a repeated hand-off handed nothing over, and a reply
// saying "I passed this to Finance" after it would be unevidenced (T-Q13). The
// ledger cannot say which of the two cases it is, so the sentence covers both.
func handOffRepeat(name string) string {
	return tools.NudgeRefusal("already_asked", fmt.Sprintf(
		"%s has already been given this question in this conversation, so it was not handed over again. If "+
			"%s handed it to you, it is yours now: answer it with your own tools, or tell the person plainly "+
			"that it is not yours either — do not pass it back. Otherwise %s is already answering it, and you "+
			"should answer only what is yours.", name, name, name))
}
