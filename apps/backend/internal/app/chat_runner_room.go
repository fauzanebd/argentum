package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// T-N6 at the runner: what a turn in a room needs besides being a turn. Which
// turns are offered nudge_agent, what a colleague's question checks before it
// runs, and the two ways a colleague's turn can end without an answer.
//
// Kept beside chat_runner.go rather than in it for the reason the room is kept
// out of every single-agent turn: nothing here does anything in a conversation
// of one.

// WithRoom lets a turn in a room ask a colleague, and makes a colleague's
// question check the room before it runs (T-N6).
//
// maxDepth is the conversation budget's MaxNudgeDepth, read for one decision: a
// turn whose next ask would be past it is not offered the tool, because a tool
// that is present and always refused is a tool the model spends iterations on
// (T-N6's Do). Zero takes agentbudget's default.
//
// Optional in WithRoster's way. Without it no turn is offered nudge_agent, and a
// peer turn runs without the membership check — safe only because nothing but a
// runner built with it can have queued one.
func (r *ChatRunner) WithRoom(p ParticipantLister, maxDepth int) *ChatRunner {
	if maxDepth <= 0 {
		maxDepth = agentbudget.DefaultCeilings().MaxNudgeDepth
	}
	r.participants = p
	r.nudgeDepth = maxDepth
	return r
}

// offersNudge decides AgentSpec.Nudge: decision 8's two gates, and the depth.
//
// **The flag is read first and the room second**, so an agent nobody allowed to
// nudge — every agent, until an admin ticks one — costs this turn no read at
// all. A room that cannot be read offers nothing: the tool is withheld rather
// than offered on a guess, and the turn answers as a single agent would.
func (r *ChatRunner) offersNudge(ctx context.Context, p queue.ChatRunPayload, agent *domain.Agent) bool {
	if agent == nil || !agent.CanNudge || r.participants == nil {
		return false
	}
	if askDepth(p) > r.nudgeDepth {
		return false
	}
	room, err := r.participants.ListParticipants(ctx, p.CompanyID, p.ThreadID)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": p.CompanyID, "thread_id": p.ThreadID,
		}).Warn("room lookup failed; this turn is not offered nudge_agent")
		return false
	}
	return len(room) > 1
}

// recipientLeft reports whether a colleague's question has lost its recipient
// since it was asked (decision 7): the membership it was planned against is
// gone, or now names another agent.
//
// A lookup error is returned rather than guessed about, so asynq retries the
// turn; a thread that no longer exists is an answer, not an error.
func (r *ChatRunner) recipientLeft(ctx context.Context, p queue.ChatRunPayload) (bool, error) {
	if p.Peer == nil || r.participants == nil {
		return false, nil
	}
	room, err := r.participants.ListParticipants(ctx, p.CompanyID, p.ThreadID)
	if errors.Is(err, domain.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("check the asked agent is still in the room: %w", err)
	}
	for _, m := range room {
		if m.ID == p.Peer.ParticipantID && m.AgentID == p.AgentID {
			return false, nil
		}
	}
	return true, nil
}

// AskedByKey is where a colleague's answer records who asked for it, on
// `messages.metadata` (T-N10). The row is otherwise an ordinary answer, and "the
// answer to the question a caller sent" has to be able to leave it out.
const AskedByKey = "asked_by"

type askedByCtxKey struct{}

// withAskedBy marks every event and assistant row the turn on ctx writes as a
// colleague's, answering agentID's question (T-N10). Installed at the top of Run,
// from the payload, before anything can publish.
func withAskedBy(ctx context.Context, agentID string) context.Context {
	if agentID == "" {
		return ctx
	}
	return context.WithValue(ctx, askedByCtxKey{}, agentID)
}

// askedBy is the agent whose question the turn on ctx answers, or "" for a turn
// a person started.
func askedBy(ctx context.Context) string {
	id, _ := ctx.Value(askedByCtxKey{}).(string)
	return id
}

// noteWithdrawn says in the room that a question went unanswered because its
// recipient left, and runs nothing.
//
// A room line rather than silence, for decision 6's reason: the question is
// already on the screen as the asker's message, and a question that is never
// answered and never explained reads as a colleague ignoring it.
func (r *ChatRunner) noteWithdrawn(ctx context.Context, p queue.ChatRunPayload) {
	recipient := r.peerAuthorName(ctx, p.CompanyID, p.AgentID)
	if recipient == "" {
		recipient = "The agent that was asked"
	}
	asker := r.peerAuthorName(ctx, p.CompanyID, p.Peer.AgentID)
	if asker == "" {
		asker = "a colleague"
	}
	content := fmt.Sprintf("%s left this conversation before answering the question from %s: %q",
		recipient, asker, p.Message)
	meta := map[string]any{
		RoomEventKey: RoomEventWithdrawn, "to_agent_id": p.AgentID, "from_agent_id": p.Peer.AgentID,
	}
	logrus.WithFields(logrus.Fields{
		"company_id": p.CompanyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		"agent_id": p.AgentID, "peer_agent_id": p.Peer.AgentID,
	}).Info("a colleague's question lost its recipient; not run")
	if _, err := r.threads.AppendAssistantMessage(ctx, p.ThreadID, content, 0, 0, 0, meta); err != nil {
		logrus.WithError(err).WithField("thread_id", p.ThreadID).
			Warn("a withdrawn question could not be written to the room")
		return
	}
	_ = r.publish(ctx, p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: ChatEventRoom,
		Content: content, Metadata: meta, Timestamp: time.Now(),
	})
}

// peerPass is the whole of the reply a colleague gives when it has nothing to
// add (T-N6).
//
// **A sentinel in the reply, not a tool.** A pass tool would sit in the schema of
// every turn in a room, answering questions nobody asked, and it would change
// the prompt prefix of the asking turns too. The sentence that offers PASS rides
// the user turn of a colleague's question and nowhere else (withPassOption), so a
// person's turn — and every golden case — is unchanged.
const peerPass = "PASS"

// withPassOption tells a colleague's turn what it is reading and that it may
// pass. A person's turn gets msg back unchanged.
//
// Composed into the model's input only, never into peermemory.WithQuestion: the
// words a room remembers are the question, not our framing of it.
func withPassOption(p queue.ChatRunPayload, msg string) string {
	if p.Peer == nil {
		return msg
	}
	return "[System context: The message below is a question from another agent in this conversation, " +
		"and your reply is posted for everyone in it to read. Answer it with your own tools. If you have " +
		"nothing to add — it is outside what your sources hold, or this conversation already answers it — " +
		"reply with exactly " + peerPass + " and nothing else.]\n\n" + msg
}

// isPass reports whether a reply is the pass sentinel, allowing for the
// decoration a model puts around a one-word answer.
func isPass(reply string) bool {
	return strings.EqualFold(strings.Trim(strings.TrimSpace(reply), "*_`.!\"' "), peerPass)
}

// settle ends a colleague's turn that passed (T-N6): the room shows a quiet line
// instead of the sentinel, and the chain ends there — nothing is asked, nothing
// is answered, and it is not a limit.
//
// Through finish, the same path an answer takes, so the turn's `final` event
// closes the colleague's bubble on the dashboard and the row carries the agent
// that passed. The settle is not measured for grounding and gets no suggestions:
// it states nothing.
func (r *ChatRunner) settle(ctx context.Context, p queue.ChatRunPayload, asker string, latency time.Duration) {
	who := agentscope.FromContext(ctx).Name
	if who == "" {
		who = "The agent that was asked"
	}
	if asker == "" {
		asker = "a colleague"
	}
	meta := map[string]any{RoomEventKey: RoomEventSettle, "from_agent_id": p.Peer.AgentID}
	r.finish(ctx, p, fmt.Sprintf("%s had nothing to add to the question from %s.", who, asker),
		0, 0, latency, nil, "", meta)
	logrus.WithFields(logrus.Fields{
		"company_id": p.CompanyID, "thread_id": p.ThreadID, "message_id": p.UserMsgID,
		"latency_ms": latency.Milliseconds(), "peer_agent_id": p.Peer.AgentID,
	}).Info("turn settled")
}

// isRoomEvent reports whether a stored message is one of a room's own lines.
func isRoomEvent(meta map[string]any) bool {
	_, ok := meta[RoomEventKey]
	return ok
}
