package queue

import "github.com/fauzanebd/argentum/internal/taint"

// PeerOrigin is where a peer turn's message came from (T-N5): which agent wrote
// it, and what that agent's turn had read by then.
//
// **It is deliberately two fields, and a test holds it there**
// (`app.TestThePeerCarrierHoldsNothingThatDecidesATurn`). What a turn may reach
// — its sources, tools, MCP servers, skills, persona, budget — comes from the
// row ChatRunPayload.AgentID names, and from nothing a peer wrote. A field here
// that fed any of those would be one agent widening another. That the scope is
// resolved from the recipient's row is today an emergent property of
// ChatRunner.Run, and an emergent property is one refactor from not being one,
// which is why it is asserted rather than described.
//
// T-N6 adds what a nudge needs in order to be *planned* — the participant row it
// was planned against, and its depth — and extends that test's list with its
// reasons. Neither decides what the recipient can do.
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
}
