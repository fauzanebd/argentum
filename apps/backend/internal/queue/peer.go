package queue

import "github.com/fauzanebd/argentum/internal/taint"

// PeerOrigin is where a peer turn's message came from (T-N5): which agent wrote
// it, and what that agent's turn had read by then.
//
// **It is deliberately four fields, and a test holds it there**
// (`app.TestThePeerCarrierHoldsNothingThatDecidesATurn`). What a turn may reach
// — its sources, tools, MCP servers, skills, persona, budget — comes from the
// row ChatRunPayload.AgentID names, and from nothing a peer wrote. A field here
// that fed any of those would be one agent widening another. That the scope is
// resolved from the recipient's row is today an emergent property of
// ChatRunner.Run, and an emergent property is one refactor from not being one,
// which is why it is asserted rather than described.
//
// T-N5 wrote the first two. T-N6 added what a nudge needs in order to be
// *planned* — the membership it was planned against, and its depth. Those two
// decide whether the turn runs and whether it may ask in turn; neither decides
// what the recipient can reach.
//
// There is no Directive here, and a peer turn that arrives with one on the
// payload has it dropped by the runner: that field is composed into the system
// prompt (T-A2b), and an agent that could write into another agent's system
// prompt could rewrite its persona and its rules.
type PeerOrigin struct {
	// AgentID is the roster agent that wrote the message. The worker resolves
	// the author's name from it, within the turn's company, rather than reading
	// a name the payload asserts: the name becomes the fence's label, and a
	// label its sender can type is not provenance.
	AgentID string `json:"agent_id"`
	// Taint is the author's turn's taint at the moment it wrote the message,
	// from taint.Tracker.Carry. The recipient inherits it before its first tool
	// call, so a document the author read gates the recipient's actions under
	// T-H9 as if it had read the file itself.
	Taint map[taint.Kind][]string `json:"taint,omitempty"`
	// ParticipantID is the room membership the question was planned against
	// (T-N6, decision 7): the thread_participants row that put the recipient in
	// the room, or "" when the recipient is the room's default speaker, who has
	// no row. The worker does not run the turn if that membership is gone or now
	// names a different agent — a room edited while a question sits in the queue
	// must not hand the question to whoever holds the seat by then.
	ParticipantID string `json:"participant_id,omitempty"`
	// Depth is the hop this turn runs at: 1 for a question asked by a turn the
	// person addressed, 2 for one its colleague asked in turn
	// (agentbudget.Ask.Depth). It rides the payload rather than the ledger so a
	// ledger that expires cannot reset it (multi-agent.md §10a), and the runner
	// reads it to stop offering nudge_agent to a turn whose next ask could only
	// be refused.
	Depth int `json:"depth,omitempty"`
	// HandOff is set when the message is not a colleague's question but the
	// person's own, passed on because it was not the author's to answer (T-N7).
	// Message then holds the person's words exactly as the handing turn received
	// them — carried from that turn's payload, never re-typed by its model — and
	// HandOff holds the one thing the model did write: its reason.
	//
	// It decides how the recipient's input is framed and nothing else. The
	// reason is fenced under the author's name like any peer's words; the scope,
	// the taint and the depth are what the three fields above already say.
	HandOff *HandOff `json:"hand_off,omitempty"`
}

// HandOff is the handing agent's note on a question it passed on (T-N7).
type HandOff struct {
	// Reason is why the question belongs to the recipient, in the handing
	// model's words. Untrusted input: the recipient reads it fenced.
	Reason string `json:"reason"`
}
