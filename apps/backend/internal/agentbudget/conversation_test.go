package agentbudget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/metrics"
)

// T-N8, the conversation budget. Every test runs against miniredis behind the
// real go-redis client: the ledger is two Lua scripts, and a script that has
// only ever been read has never been run.

type ledger struct {
	conv *Conversation
	mr   *miniredis.Miniredis
	now  time.Time
}

func newLedger(t *testing.T, c Ceilings) *ledger {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	l := &ledger{mr: mr, now: time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)}
	l.conv = NewConversation(rdb, c)
	l.conv.now = func() time.Time { return l.now }
	return l
}

func (l *ledger) open(t *testing.T, question string, agents ...string) {
	t.Helper()
	if err := l.conv.Open(context.Background(), "co-1", "msg-1", question, agents); err != nil {
		t.Fatalf("Open: %v", err)
	}
}

func (l *ledger) ask(t *testing.T, agent, question string, depth int) Verdict {
	t.Helper()
	v, err := l.conv.Admit(context.Background(), Ask{
		CompanyID: "co-1", UserMsgID: "msg-1", AgentID: agent, Question: question, Depth: depth,
	})
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	return v
}

func (l *ledger) turns() string { return l.mr.HGet(ledgerKey("co-1", "msg-1"), "turns") }

// deadConversation is a ledger over an address nothing listens on any more.
func deadConversation(t *testing.T) *Conversation {
	t.Helper()
	mr := miniredis.NewMiniRedis()
	if err := mr.Start(); err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	addr := mr.Addr()
	mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewConversation(rdb, Ceilings{})
}

// The numbers are placeholders, and the comment saying so also says what
// arithmetic produced them. Both are pinned, so the day one moves the comment is
// re-read rather than left describing a number that no longer exists.
func TestTheDefaultCeilingsAreTheirStatedArithmetic(t *testing.T) {
	d := DefaultCeilings()
	if d.MaxAgentTurns != 6 || d.MaxNudgeDepth != 2 || d.Wall != 5*time.Minute {
		t.Errorf("DefaultCeilings = %+v, want 6 turns, depth 2, 5m", d)
	}
	if d.Wall != 2*Default().Wall {
		t.Errorf("Wall %s is no longer two per-turn wall clocks (%s), which DefaultCeilings says it is",
			d.Wall, Default().Wall)
	}
	if got := (Ceilings{}).Normalize(); got != d {
		t.Errorf("an unset config normalised to %+v, want the defaults — a zero must not switch a ceiling off", got)
	}
}

// A person's own message is counted and never refused, even when it is wider
// than the whole budget. What it spends is what is left for asks.
func TestAPersonsFanOutIsCountedAndNeverRefused(t *testing.T) {
	l := newLedger(t, Ceilings{MaxAgentTurns: 3})
	l.open(t, "thoughts?", "ag-a", "ag-b", "ag-c", "ag-d")

	if got := l.turns(); got != "4" {
		t.Errorf("ledger turns = %s, want 4 — the fan-out is counted whole", got)
	}
	v := l.ask(t, "ag-a", "can you check the stock figure?", 1)
	if v.Admitted || v.Dimension != DimensionTurns {
		t.Fatalf("an ask after a fan-out wider than the budget = %+v, want refused on turns", v)
	}
	if !strings.Contains(v.Reason, "4 of 3") {
		t.Errorf("reason = %q, want the count and the ceiling", v.Reason)
	}
}

func TestDepthTwoRunsAndDepthThreeIsRefused(t *testing.T) {
	l := newLedger(t, DefaultCeilings())
	l.open(t, "what happened?", "ag-fin")

	if v := l.ask(t, "ag-ops", "stock for SKU 4471?", 2); !v.Admitted {
		t.Fatalf("depth 2 = %+v, want admitted", v)
	}
	v := l.ask(t, "ag-hr", "headcount at store 12?", 3)
	if v.Admitted || v.Dimension != DimensionDepth {
		t.Fatalf("depth 3 = %+v, want refused on depth", v)
	}
	if got := l.turns(); got != "2" {
		t.Errorf("ledger turns = %s after a refused hop, want 2 — a refusal counts nothing", got)
	}

	// Too deep is decided before Redis is asked, so it creates no ledger either.
	if _, err := l.conv.Admit(context.Background(), Ask{
		CompanyID: "co-1", UserMsgID: "msg-2", AgentID: "ag-hr", Question: "x", Depth: 3,
	}); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if l.mr.Exists(ledgerKey("co-1", "msg-2")) {
		t.Error("a hop refused on depth created a ledger")
	}
}

// "A loop guard whose only test is a well-behaved model is a loop guard tested
// by the absence of the problem." Every turn here asks every other agent in the
// room something new — the stub that always nudges. Each ceiling is made to
// bind in turn, and the room must stop on it with a notice naming what went
// unasked.
func TestARoomThatAlwaysNudgesStops(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ceilings Ceilings
		binds    Dimension
		wantRun  int // turns that ran, the person's fan-out included
	}{
		// Three addressed, then three asks: fin asks ops and hr, ops asks fin.
		{"turns bind", DefaultCeilings(), DimensionTurns, 6},
		// With turns out of the way depth alone ends it: three at depth 0, each
		// asking two (six at depth 1), each of those asking two (twelve at depth
		// 2), and nothing deeper.
		{"depth binds", Ceilings{MaxAgentTurns: 1000}, DimensionDepth, 3 + 6 + 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLedger(t, tc.ceilings)
			room := []string{"ag-fin", "ag-ops", "ag-hr"}
			l.open(t, "what happened to margin?", room...)

			type turn struct {
				agent string
				depth int
			}
			queue := []turn{{"ag-fin", 0}, {"ag-ops", 0}, {"ag-hr", 0}}
			ran, asked := 0, 0
			var bound *Verdict
			var notice, unaskedQ string
			for len(queue) > 0 {
				if ran > 1000 {
					t.Fatal("the room never stopped")
				}
				cur := queue[0]
				queue = queue[1:]
				ran++
				if cur.depth > tc.ceilings.Normalize().MaxNudgeDepth {
					t.Fatalf("a turn ran at depth %d", cur.depth)
				}
				for _, other := range room {
					if other == cur.agent {
						continue
					}
					asked++
					q := fmt.Sprintf("%s asks %s, ask %d", cur.agent, other, asked)
					v := l.ask(t, other, q, cur.depth+1)
					if v.Admitted {
						queue = append(queue, turn{other, cur.depth + 1})
						continue
					}
					if v.Dimension == tc.binds && bound == nil {
						bound, unaskedQ = &v, q
						notice = v.Notice(cur.agent, other, q)
					}
				}
			}

			if ran != tc.wantRun {
				t.Errorf("%d turns ran, want %d", ran, tc.wantRun)
			}
			if bound == nil {
				t.Fatalf("no ask was refused on %s", tc.binds)
			}
			if !strings.Contains(notice, unaskedQ) {
				t.Errorf("the room's notice %q does not name the question that went unasked (%q)", notice, unaskedQ)
			}
		})
	}
}

func TestTheClockEndsAConversation(t *testing.T) {
	l := newLedger(t, DefaultCeilings())
	l.open(t, "what happened?", "ag-fin")

	l.now = l.now.Add(5*time.Minute - time.Millisecond)
	if v := l.ask(t, "ag-ops", "stock?", 1); !v.Admitted {
		t.Fatalf("an ask a millisecond inside the window = %+v, want admitted", v)
	}
	l.now = l.now.Add(time.Millisecond)
	v := l.ask(t, "ag-hr", "headcount?", 1)
	if v.Admitted || v.Dimension != DimensionWall {
		t.Fatalf("an ask at the window's edge = %+v, want refused on wall", v)
	}
	if !strings.Contains(v.Reason, "5m0s of 5m0s") {
		t.Errorf("reason = %q, want the elapsed time and the ceiling", v.Reason)
	}
}

// "A key that never expires is a leak; one that expires early is a limit that
// resets mid-loop." The window an ask can be admitted in, plus one Wall, set
// once — and an ask inside it must not push it out.
func TestTheLedgerExpiresAndIsNeverExtended(t *testing.T) {
	l := newLedger(t, DefaultCeilings())
	key := ledgerKey("co-1", "msg-1")
	l.open(t, "what happened?", "ag-fin")

	if got := l.mr.TTL(key); got != 10*time.Minute {
		t.Fatalf("TTL = %s, want 10m", got)
	}
	l.mr.FastForward(4 * time.Minute)
	l.now = l.now.Add(4 * time.Minute)
	if v := l.ask(t, "ag-ops", "stock?", 1); !v.Admitted {
		t.Fatalf("ask = %+v, want admitted", v)
	}
	if got := l.mr.TTL(key); got != 6*time.Minute {
		t.Errorf("TTL after an ask = %s, want 6m — an ask must not push the expiry out", got)
	}
	l.mr.FastForward(6 * time.Minute)
	if l.mr.Exists(key) {
		t.Error("the ledger outlived its window")
	}
}

// The ticket's watermark, in the form this product can reach: the same agent
// asked the same question twice in one conversation is queued once. See
// Verdict.Repeat for why the ticket's own wording could never fire.
func TestAnAgentAskedTheSameQuestionTwiceIsQueuedOnce(t *testing.T) {
	l := newLedger(t, DefaultCeilings())
	l.open(t, "What was margin in Q3?", "ag-fin", "ag-ops")

	// The person already asked Ops this. A colleague asking it again, in another
	// case and spacing, is the same question.
	v := l.ask(t, "ag-ops", "what was  margin in q3?", 1)
	if !v.Repeat || v.Admitted {
		t.Fatalf("a re-ask of the person's question = %+v, want a repeat", v)
	}
	if got := l.turns(); got != "2" {
		t.Errorf("ledger turns = %s, want 2 — a repeat costs nothing", got)
	}
	res := v.ToolResult("Ops", "what was margin in q3?")
	if IsRefusal(res) {
		t.Errorf("a repeat reads as a refusal, but the colleague was asked: %s", res)
	}
	if !strings.Contains(res, `"already_asked":true`) {
		t.Errorf("repeat result = %s", res)
	}
	if n := v.Notice("Finance", "Ops", "what was margin in q3?"); n != "" {
		t.Errorf("a repeat put a notice in the room: %q", n)
	}

	if v := l.ask(t, "ag-ops", "and in Q4?", 1); !v.Admitted {
		t.Errorf("a new question = %+v, want admitted", v)
	}
	if v := l.ask(t, "ag-ops", "and in Q4?", 1); !v.Repeat {
		t.Errorf("the same new question again = %+v, want a repeat", v)
	}
	if v := l.ask(t, "ag-hr", "What was margin in Q3?", 1); !v.Admitted {
		t.Errorf("the same question to a different agent = %+v, want admitted", v)
	}
}

const (
	helperAddrEnv   = "AGENTBUDGET_LEDGER_HELPER_ADDR"
	helperNameEnv   = "AGENTBUDGET_LEDGER_HELPER_NAME"
	sharedCeiling   = 40
	triesPerProcess = 30
)

// "The turn counter is shared across two worker replicas — proven with two
// processes, not one." Two goroutines over one client share a connection pool
// and say nothing about a second replica. Two copies of this test binary, each
// with its own client, asking one Redis at the same time, do.
//
// Each process tries 30 asks against a ceiling of 40. Two counters that each
// counted alone would admit 60.
func TestTwoProcessesShareOneCounter(t *testing.T) {
	mr := miniredis.RunT(t)

	type proc struct {
		cmd         *exec.Cmd
		stdout, err bytes.Buffer
	}
	procs := make([]*proc, 2)
	for i := range procs {
		p := &proc{cmd: exec.Command(os.Args[0], "-test.run=^TestLedgerHelperProcess$")}
		p.cmd.Env = append(os.Environ(), helperAddrEnv+"="+mr.Addr(), fmt.Sprintf("%s=p%d", helperNameEnv, i))
		p.cmd.Stdout, p.cmd.Stderr = &p.stdout, &p.err
		if err := p.cmd.Start(); err != nil {
			t.Fatalf("start process %d: %v", i, err)
		}
		procs[i] = p
	}

	re := regexp.MustCompile(`admitted=(\d+)`)
	total := 0
	for i, p := range procs {
		if err := p.cmd.Wait(); err != nil {
			t.Fatalf("process %d: %v\nstdout:\n%s\nstderr:\n%s", i, err, p.stdout.String(), p.err.String())
		}
		m := re.FindStringSubmatch(p.stdout.String())
		if m == nil {
			t.Fatalf("process %d reported nothing:\n%s", i, p.stdout.String())
		}
		n, _ := strconv.Atoi(m[1])
		t.Logf("process %d (pid %d) was admitted %d of %d asks", i, p.cmd.ProcessState.Pid(), n, triesPerProcess)
		total += n
	}
	if total != sharedCeiling {
		t.Errorf("two processes were admitted %d asks between them, want exactly %d", total, sharedCeiling)
	}
	if got := mr.HGet(ledgerKey("co-1", "msg-shared"), "turns"); got != strconv.Itoa(sharedCeiling) {
		t.Errorf("ledger turns = %s, want %d", got, sharedCeiling)
	}
}

// TestLedgerHelperProcess is not a test. It is the replica
// TestTwoProcessesShareOneCounter starts twice, and it does nothing on its own.
func TestLedgerHelperProcess(t *testing.T) {
	addr := os.Getenv(helperAddrEnv)
	if addr == "" {
		return
	}
	name := os.Getenv(helperNameEnv)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = rdb.Close() }()
	conv := NewConversation(rdb, Ceilings{MaxAgentTurns: sharedCeiling})

	var mu sync.Mutex
	admitted := 0
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < triesPerProcess/3; i++ {
				v, err := conv.Admit(context.Background(), Ask{
					CompanyID: "co-1", UserMsgID: "msg-shared", AgentID: "ag-" + name,
					Question: fmt.Sprintf("%s/%d/%d", name, g, i), Depth: 1,
				})
				if err != nil {
					t.Errorf("Admit: %v", err)
					return
				}
				if v.Admitted {
					mu.Lock()
					admitted++
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	fmt.Printf("admitted=%d\n", admitted)
}

// Fails closed for an ask, open for a person — the asymmetry
// Conversation.Admit and Conversation.Open each argue.
func TestALedgerThatCannotBeReachedRefusesAnAskAndNeverAPerson(t *testing.T) {
	ctx := context.Background()
	dead := deadConversation(t)

	if err := dead.Open(ctx, "co-1", "msg-1", "q", []string{"ag-fin"}); err == nil {
		t.Error("Open over a dead Redis returned nil; the caller has nothing to log")
	}
	v, err := dead.Admit(ctx, Ask{CompanyID: "co-1", UserMsgID: "msg-1", AgentID: "ag-ops", Question: "q", Depth: 1})
	if !errors.Is(err, ErrLedgerUnavailable) {
		t.Errorf("Admit err = %v, want ErrLedgerUnavailable", err)
	}
	if v.Admitted || v.Dimension != DimensionUnavailable {
		t.Errorf("Admit over a dead Redis = %+v, want refused as unavailable", v)
	}
	if !IsRefusal(v.ToolResult("Ops", "q")) {
		t.Error("an unavailable ledger's answer does not read as a refusal")
	}

	for name, c := range map[string]*Conversation{"nil ledger": nil, "no redis": NewConversation(nil, Ceilings{})} {
		if err := c.Open(ctx, "co-1", "msg-1", "q", []string{"ag-fin"}); err != nil {
			t.Errorf("%s: Open = %v, want nil — counting nothing is not a failure", name, err)
		}
		if v, err := c.Admit(ctx, Ask{Depth: 1}); v.Admitted || !errors.Is(err, ErrLedgerUnavailable) {
			t.Errorf("%s: Admit = %+v, %v; want refused as unavailable", name, v, err)
		}
	}
}

// A refusal reaches three readers that already exist, and each must read it as
// a call that did not do what it was for.
func TestARefusalIsReadAsARefusalByTheReadersThatAlreadyExist(t *testing.T) {
	l := newLedger(t, Ceilings{MaxAgentTurns: 1})
	l.open(t, "what happened?", "ag-fin")
	v := l.ask(t, "ag-ops", "stock for SKU 4471?", 1)
	if v.Admitted {
		t.Fatalf("ask = %+v, want refused", v)
	}

	res := v.ToolResult("Ops", "stock for SKU 4471?")
	if !IsRefusal(res) {
		t.Errorf("the audit decorator (T-05) would record this call as run: %s", res)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(res), &parsed); err != nil {
		t.Fatalf("tool result is not JSON: %v", err)
	}
	if got := RefusalReason(parsed); got != v.Reason {
		t.Errorf("the tool digest (T-Q12) would remember the reason as %q, want %q", got, v.Reason)
	}
	if unasked, _ := parsed["unasked"].(map[string]interface{}); unasked["question"] != "stock for SKU 4471?" {
		t.Errorf("unasked = %v, want the question named", parsed["unasked"])
	}

	tr := New(Default())
	tr.Observe("nudge_agent", res, nil)
	for _, ok := range tr.Snapshot().Succeeded {
		if ok == "nudge_agent" {
			t.Error("the refused ask counts as succeeded, so \"I asked Ops\" would pass T-Q13")
		}
	}

	n := v.Notice("Finance", "Ops", "stock for SKU 4471?")
	for _, want := range []string{"Finance", "Ops", "stock for SKU 4471?", v.Reason} {
		if !strings.Contains(n, want) {
			t.Errorf("notice %q is missing %q", n, want)
		}
	}
}

// "A counter per dimension." Only a ceiling moves it: a repeat is not a limit,
// and an outage is not a room reaching one.
func TestOnlyACeilingIsCounted(t *testing.T) {
	counts := func() map[string]int64 { return metrics.Default().GetSnapshot().Domain.ConversationCeilings }
	before := counts()

	l := newLedger(t, Ceilings{MaxAgentTurns: 1})
	l.open(t, "what happened?", "ag-fin")
	l.ask(t, "ag-ops", "too deep", 3)
	l.ask(t, "ag-ops", "stock?", 1)
	l.ask(t, "ag-fin", "what happened?", 1)
	_, _ = deadConversation(t).Admit(context.Background(), Ask{Depth: 1})

	after := counts()
	if after["depth"]-before["depth"] != 1 || after["turns"]-before["turns"] != 1 {
		t.Errorf("counts moved from %v to %v, want depth and turns up by one each", before, after)
	}
	for dim := range after {
		if dim != "turns" && dim != "depth" && dim != "wall" {
			t.Errorf("a %q series was counted; only ceilings are", dim)
		}
	}
}
