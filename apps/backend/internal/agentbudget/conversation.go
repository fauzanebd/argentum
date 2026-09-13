package agentbudget

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/metrics"
)

// Ceilings is the conversation-level budget (T-N8): what one person's message
// may set off across every turn it leads to, where Budget bounds one turn.
//
// Zero on any field takes the shipped default, for Budget.Normalize's reason: a
// half-filled config must not silently switch a dimension off, and a loop guard
// that is off is the failure nobody notices until the invoice.
type Ceilings struct {
	// MaxAgentTurns caps agent turns queued from one person's message, the
	// person's own fan-out included.
	MaxAgentTurns int
	// MaxNudgeDepth is the deepest hop an ask may start. A person's message is
	// depth 0, and a turn it addressed asking a colleague starts depth 1.
	MaxNudgeDepth int
	// Wall caps the time from the message's first queued turn to the last ask
	// admitted on its behalf.
	Wall time.Duration
}

// DefaultCeilings is the shipped conversation budget, and **every number in it
// is arithmetic, not measurement.** Nobody has run a room and read what it cost
// per message; 09-multi-agent-conversations-roadmap.md §2c names the arm that
// would — one room, three agents, five real questions, usage_events read per
// agent. Until it runs, this is the honest state:
//
//   - Six turns: four participants addressed at once (THREAD_MAX_PARTICIPANTS'
//     default) is four, which leaves two asks.
//   - Depth two: A asks B, B asks C, C answers. A third hop is where a room stops
//     being legible to the person reading it. It is also the number Hermes
//     arrived at (GROUP_CHAT_MAX_CONTINUATIONS).
//   - Five minutes: two per-turn wall clocks (Default's 150s) end to end — an
//     asked turn that runs as long as a turn may, after the turn that asked.
//
// The arithmetic is written down so that whoever replaces these can see what
// they are replacing. A default nobody can revise is a magic number nobody
// dares touch.
func DefaultCeilings() Ceilings {
	return Ceilings{MaxAgentTurns: 6, MaxNudgeDepth: 2, Wall: 5 * time.Minute}
}

// Normalize replaces non-positive fields with the shipped defaults.
func (c Ceilings) Normalize() Ceilings {
	d := DefaultCeilings()
	if c.MaxAgentTurns <= 0 {
		c.MaxAgentTurns = d.MaxAgentTurns
	}
	if c.MaxNudgeDepth <= 0 {
		c.MaxNudgeDepth = d.MaxNudgeDepth
	}
	if c.Wall <= 0 {
		c.Wall = d.Wall
	}
	return c
}

// Dimension names what refused an ask.
//
// The first three are ceilings and are counted, so "how often does a room hit
// its limit" is a series rather than a grep. DimensionUnavailable is an outage
// and is not: a Redis blip is not a room reaching its limit, and a counter that
// moved with one would make a loop guard's rate unreadable.
type Dimension string

const (
	DimensionTurns       Dimension = "turns"
	DimensionDepth       Dimension = "depth"
	DimensionWall        Dimension = "wall"
	DimensionUnavailable Dimension = "unavailable"
)

// Ask is one turn nobody addressed, asking to be queued: a nudge (T-N6) or a
// hand-off (T-N7).
type Ask struct {
	CompanyID string
	// UserMsgID is the person's message the whole fan-out descends from, which
	// every turn in it already carries as queue.ChatRunPayload.UserMsgID because
	// T-N3 appends the message once.
	UserMsgID string
	// AgentID is who would be asked, and Question what. Together they are the
	// repeat check's key — see Verdict.Repeat.
	AgentID  string
	Question string
	// Depth is the hop the asked turn would run at: 1 when a turn the person
	// addressed asks a colleague. Below 1 is read as 1, because nothing that
	// asks is the person.
	Depth int
}

// Verdict is the ledger's answer to one Ask.
type Verdict struct {
	Admitted bool
	// Repeat is an ask this conversation has already queued: the same agent,
	// asked the same question, from the same person's message. Nothing is
	// queued and no turn is counted.
	//
	// This is the ticket's "watermark", and it is narrower than the ticket's
	// wording on purpose. Hermes skips a member whose context has not changed
	// since its last turn, which fires in a *round* — every member is re-polled
	// whether or not anyone spoke to it. Nothing here re-polls: every turn is
	// queued because a new message arrived for it, so "no new messages it has
	// not seen" is never true at the moment of queueing. What is true, and
	// costs a model call for nothing, is an agent asked again what it has
	// already been asked in this fan-out — by the person, or by a colleague.
	Repeat bool
	// Dimension is what refused the ask; empty when it was admitted or a repeat.
	Dimension Dimension
	// Reason is the sentence the refusal carries, in Tracker's register
	// ("… budget spent (6 of 6)").
	Reason string
	// Turns is the conversation's count after this call.
	Turns int
}

// Conversation is the ledger behind Ceilings, keyed on the person's message.
//
// Why a second level at all. Until rooms, one user message was one turn, and
// Tracker's four dimensions bounded everything a message could spend. A room
// breaks that twice: `@all` makes one message N turns (T-N3), and a nudge
// (T-N6) makes a turn queue another turn nobody addressed — so A→B→A is three
// turns from one sentence, each opening a fresh Tracker that believes it is
// alone. The ceiling that matters moves up a level, and this is that level.
// T-16's per-turn budget is untouched: two ceilings, at two levels.
//
// **Redis, not memory.** Turns run in cmd/worker, which is horizontally scaled,
// and an in-process counter is a limit that stops working the day a second
// replica starts. Every decision is one Lua script, so two replicas asking at
// the same instant cannot both take the last turn.
//
// **Counted when a turn is queued, not when it runs**, and never by messages
// appended. A refusal that arrives after the work was queued has refused
// nothing, and a message counter does not bound model calls: Hermes' ten-message
// cap sits over a possible eighteen member turns, because a pass costs a call
// and appends no message (roadmap 09, decision 9).
//
// **The credit check is not this.** ChatEnqueuer.WithBudget refuses a tenant at
// zero before anything here is asked. This ceiling is about runaway, not money,
// and the two refusals must read differently to the person who meets them.
//
// Every method is nil-safe, as Tracker's are.
type Conversation struct {
	rdb      redis.Scripter
	ceilings Ceilings
	now      func() time.Time
}

// NewConversation returns a ledger over rdb. A nil rdb is a ledger that cannot
// count: Open counts nothing and Admit refuses — see each for why.
func NewConversation(rdb redis.Scripter, c Ceilings) *Conversation {
	return &Conversation{rdb: rdb, ceilings: c.Normalize(), now: time.Now}
}

// Ceilings returns the budget this ledger enforces, so a caller can say it.
func (c *Conversation) Ceilings() Ceilings {
	if c == nil {
		return DefaultCeilings()
	}
	return c.ceilings
}

// Open counts the turns a person's own message fans out to, marks each agent as
// asked the message, and starts the conversation's clock. Called by the enqueue
// path before the first of those turns is queued, so a turn that asks a
// colleague the moment it starts finds its own fan-out already counted.
//
// **It never refuses, and that is the half of this ledger that fails open.** A
// person who addresses four agents has made a decision about their own message,
// the same decision T-N3 carries out, and the credit check has already had its
// say. A loop guard is for turns nobody addressed. So a fan-out wider than
// MaxAgentTurns is queued whole — it simply leaves no asks — and a Redis error
// is returned for the caller to log, never to refuse on.
func (c *Conversation) Open(ctx context.Context, companyID, userMsgID, question string, agentIDs []string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	args := make([]any, 0, 3+len(agentIDs))
	args = append(args, c.now().UnixMilli(), c.ttl().Milliseconds(), len(agentIDs))
	for _, id := range agentIDs {
		args = append(args, askedField(id, question))
	}
	if err := openScript.Run(ctx, c.rdb, []string{ledgerKey(companyID, userMsgID)}, args...).Err(); err != nil {
		return fmt.Errorf("open conversation budget: %w", err)
	}
	return nil
}

// Admit decides whether one ask may be queued, and counts it when it may.
//
// **This half fails closed.** An ask is a model spending another agent's budget
// against another agent's sources on its own initiative, and when the ledger
// cannot be read, admitting it is exactly the unbounded spend this type exists
// to stop. Refusing costs the asking model one tool result it can act on — the
// same refusal a spent ceiling returns, with DimensionUnavailable and the error.
//
// A ledger Open never created — a scheduled task's turn, a watcher's briefing —
// is created by the first ask, with the clock starting there. Depth is not
// stored: it rides the asked turn's payload, so a ledger that expires or is
// lost cannot reset it, and depth alone still ends every chain.
func (c *Conversation) Admit(ctx context.Context, a Ask) (Verdict, error) {
	ceil := c.Ceilings()
	if a.Depth < 1 {
		a.Depth = 1
	}
	if a.Depth > ceil.MaxNudgeDepth {
		// Decided before Redis is asked, so a chain too deep never touches the
		// ledger — no key created, no turn counted, nothing to fail.
		metrics.ConversationCeiling(string(DimensionDepth))
		return Verdict{
			Dimension: DimensionDepth,
			Reason:    fmt.Sprintf("ask depth limit reached (hop %d; a chain stops at %d)", a.Depth, ceil.MaxNudgeDepth),
		}, nil
	}
	if c == nil || c.rdb == nil {
		return unavailable(), ErrLedgerUnavailable
	}

	vals, err := admitScript.Run(ctx, c.rdb, []string{ledgerKey(a.CompanyID, a.UserMsgID)},
		c.now().UnixMilli(), c.ttl().Milliseconds(), ceil.Wall.Milliseconds(),
		ceil.MaxAgentTurns, askedField(a.AgentID, a.Question),
	).Slice()
	if err != nil {
		return unavailable(), fmt.Errorf("%w: %w", ErrLedgerUnavailable, err)
	}
	if len(vals) != 4 {
		return unavailable(), fmt.Errorf("%w: script returned %d values", ErrLedgerUnavailable, len(vals))
	}
	admitted, _ := vals[0].(int64)
	outcome, _ := vals[1].(string)
	turns, _ := vals[2].(int64)
	elapsedMS, _ := vals[3].(int64)

	v := Verdict{Admitted: admitted == 1, Turns: int(turns)}
	switch outcome {
	case "":
	case "repeat":
		v.Repeat = true
	case string(DimensionTurns):
		v.Dimension = DimensionTurns
		v.Reason = fmt.Sprintf("conversation turn budget spent (%d of %d agent turns from one message)",
			turns, ceil.MaxAgentTurns)
	case string(DimensionWall):
		v.Dimension = DimensionWall
		v.Reason = fmt.Sprintf("conversation time budget spent (%s of %s)",
			(time.Duration(elapsedMS) * time.Millisecond).Round(time.Second), ceil.Wall)
	default:
		return unavailable(), fmt.Errorf("%w: unknown outcome %q", ErrLedgerUnavailable, outcome)
	}
	if v.Dimension != "" {
		metrics.ConversationCeiling(string(v.Dimension))
	}
	return v, nil
}

// ErrLedgerUnavailable is a conversation ledger that could not be read or
// written. Admit returns it beside a refusal, never instead of one.
var ErrLedgerUnavailable = errors.New("conversation budget unavailable")

func unavailable() Verdict {
	return Verdict{Dimension: DimensionUnavailable, Reason: "conversation budget could not be checked"}
}

// ToolResult is what a refused or repeated ask returns to the model that made
// it, as its tool's result; "" when the ask was admitted.
//
// A refusal carries budget_exhausted, which is IsRefusal's key, and that is not
// borrowed for convenience. The call did not do what it was for, and three
// readers already treat that key as meaning so: the audit decorator records the
// call as refused (T-05), the tool digest remembers it as refused rather than
// done (T-Q12), and Observe keeps it out of the calls that succeeded — so a
// reply saying "I asked Finance" is unevidenced (T-Q13).
//
// A repeat is not a refusal. The colleague *was* asked, earlier in this
// conversation, and its answer is coming; telling the model otherwise would
// have it apologise for a question that is already on its way.
func (v Verdict) ToolResult(agent, question string) string {
	var payload map[string]any
	switch {
	case v.Admitted:
		return ""
	case v.Repeat:
		payload = map[string]any{
			"already_asked": true,
			"agent":         agent,
			"note": fmt.Sprintf("%s has already been asked this in this conversation, and the answer will "+
				"appear here. Do not ask again; carry on with your own answer.", agent),
		}
	default:
		payload = map[string]any{
			"budget_exhausted": true,
			"scope":            "conversation",
			"reason":           v.Reason,
			"unasked":          map[string]string{"agent": agent, "question": question},
			"instruction": fmt.Sprintf("%s was not asked, and no colleague can be asked again from this "+
				"message — do not try. Answer the person yourself from what you already have: say plainly "+
				"that you could not ask %s and what that leaves unanswered. Do NOT state any figure you "+
				"would have needed %s for.", agent, agent, agent),
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return v.Reason
	}
	return string(out)
}

// Notice is the sentence the room shows for a refused ask; "" for an admitted
// ask and for a repeat, which left nothing unasked.
//
// The person reading the transcript needs telling as well as the model
// (agentbudget's package comment makes the model half of that argument): the
// question that went unasked is one they may want to ask themselves, and a room
// that silently drops it reads as a colleague who ignored it.
func (v Verdict) Notice(asker, agent, question string) string {
	if v.Admitted || v.Repeat {
		return ""
	}
	return fmt.Sprintf("%s's question to %s went unasked — %s: %q", asker, agent, v.Reason, question)
}

// ttl is how long a ledger lives: the whole window in which an ask can be
// admitted, and then exactly Wall more. Set once, when the ledger is created,
// and never extended — a ledger whose every write pushed its expiry out would
// live as long as a loop kept asking, which is the one thing it must not do.
//
// **Why Wall more, rather than none.** A turn admitted in the window's last
// second still runs, and may ask; that ask must find the ledger and be refused
// by the clock. A ledger gone by then would be created fresh, with a fresh
// clock — a limit that resets mid-loop. The per-turn wall (150s) plus queue
// wait fits inside Wall; a backlog deeper than that can still outlive it, and
// depth, which the ledger does not hold, is what bounds that case.
func (c *Conversation) ttl() time.Duration { return 2 * c.Ceilings().Wall }

// ledgerKey scopes the ledger by company as well as by message. Message ids are
// uuids and would not collide, but a Redis key that names a tenant's object
// without naming the tenant is a key a later reader has to trust rather than read.
func ledgerKey(companyID, userMsgID string) string {
	return "conv:budget:" + companyID + ":" + userMsgID
}

// askedField is the ledger hash field recording that agentID was asked question.
// The question is folded for case and whitespace before hashing: a model that
// re-sends a question with different capitalisation is asking the same thing.
func askedField(agentID, question string) string {
	folded := strings.Join(strings.Fields(strings.ToLower(question)), " ")
	sum := sha256.Sum256([]byte(folded))
	return "asked:" + agentID + ":" + hex.EncodeToString(sum[:16])
}

// openLua creates the ledger if it is not there, counts the fan-out and marks
// who was asked what.
//
// KEYS[1] = ledger hash
// ARGV[1] = now (unix ms), ARGV[2] = ttl (ms), ARGV[3] = turns to count
// ARGV[4..] = asked fields
const openLua = `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('HSET', KEYS[1], 'started', ARGV[1], 'turns', 0)
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
local turns = redis.call('HINCRBY', KEYS[1], 'turns', ARGV[3])
for i = 4, #ARGV do
  redis.call('HSET', KEYS[1], ARGV[i], 1)
end
return turns
`

// admitLua decides one ask. The order of the checks is the order of the
// answers a model should get: past the clock nothing more happens at all; a
// repeat is not a refusal and costs nothing; and only then is the count asked.
//
// The clock is the caller's (ARGV[1]) rather than Redis's TIME, as the rate
// limiter's token bucket does: two replicas' clocks differ by NTP's
// milliseconds, against a five-minute window.
//
// KEYS[1] = ledger hash
// ARGV[1] = now (unix ms), ARGV[2] = ttl (ms), ARGV[3] = wall (ms)
// ARGV[4] = max turns, ARGV[5] = asked field
// Returns {admitted 0|1, outcome, turns, elapsed ms}.
const admitLua = `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('HSET', KEYS[1], 'started', ARGV[1], 'turns', 0)
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
local now = tonumber(ARGV[1])
local started = tonumber(redis.call('HGET', KEYS[1], 'started'))
local turns = tonumber(redis.call('HGET', KEYS[1], 'turns'))
local elapsed = now - started
if elapsed >= tonumber(ARGV[3]) then
  return {0, 'wall', turns, elapsed}
end
if redis.call('HEXISTS', KEYS[1], ARGV[5]) == 1 then
  return {0, 'repeat', turns, elapsed}
end
if turns >= tonumber(ARGV[4]) then
  return {0, 'turns', turns, elapsed}
end
turns = redis.call('HINCRBY', KEYS[1], 'turns', 1)
redis.call('HSET', KEYS[1], ARGV[5], 1)
return {1, '', turns, elapsed}
`

var (
	openScript  = redis.NewScript(openLua)
	admitScript = redis.NewScript(admitLua)
)
