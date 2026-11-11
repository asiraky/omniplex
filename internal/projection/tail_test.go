package projection

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
)

func lines(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	return b.String()
}

// The cases below are mirrored in web/src/lib/tail.test.ts: both sides must
// cut in the same place.
func TestAppendTailKeepsTheLastLines(t *testing.T) {
	cases := []struct {
		name, tail, delta, want string
	}{
		{"short output is kept whole", "", "a\nb\n", "a\nb\n"},
		{"deltas join mid-line", "build: 4", "2%\n", "build: 42%\n"},
		{"exactly the cap is kept whole", "", lines(1, 20), lines(1, 20)},
		{"one terminated line over drops the oldest", "", lines(1, 21), lines(2, 21)},
		{"an unterminated partial line counts as one", "", lines(1, 20) + "partial", lines(2, 20) + "partial"},
		{"a blank line counts as one", lines(1, 19), "\n\n", lines(2, 19) + "\n\n"},
		{"many short lines", "", strings.Repeat("x\n", 500), strings.Repeat("x\n", 20)},
		{"carriage returns are not newlines", "", "10%\r20%\r30%", "10%\r20%\r30%"},
	}
	for _, tc := range cases {
		if got := appendTail(tc.tail, tc.delta); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAppendTailBoundsOneRunawayLine(t *testing.T) {
	// A progress bar that only ever redraws with "\r" is one line forever.
	bar := strings.Repeat("\r[=====>    ] é漢🙂", 2000)
	got := appendTail("", bar)
	if n := utf8.RuneCountInString(got); n != tailChars {
		t.Fatalf("kept %d code points of a runaway line, want %d", n, tailChars)
	}
	if !strings.HasSuffix(bar, got) || !utf8.ValidString(got) {
		t.Fatalf("kept something other than a clean suffix of the line: %q…", got[:40])
	}
	// Multibyte text short enough in code points is not cut by its bytes.
	wide := strings.Repeat("漢", tailChars)
	if got := appendTail("", wide); got != wide {
		t.Fatalf("cut %d code points of multibyte text to %d", tailChars, utf8.RuneCountInString(got))
	}
}

// The coalescer joins consecutive deltas, so folding them joined must land
// on the same tail as folding them one by one.
func TestAppendTailInPiecesAgreesWithAtOnce(t *testing.T) {
	text := []string{"a", "b", "é", "漢", "🙂", "\r", " "}
	rng := rand.New(rand.NewSource(301))
	// Each piece is lines of some length, from none at all (a bare newline)
	// to past the char cap (a runaway progress bar), so both caps get hit,
	// and the joins land mid-line, mid-run of newlines, and after a newline.
	piece := func() string {
		var b strings.Builder
		width := []int{0, 3, 40, 1500}[rng.Intn(4)]
		for n := rng.Intn(30); n > 0; n-- {
			for w := rng.Intn(width + 1); w > 0; w-- {
				b.WriteString(text[rng.Intn(len(text))])
			}
			if rng.Intn(4) > 0 {
				b.WriteString("\n")
			}
		}
		return b.String()
	}
	for i := 0; i < 2000; i++ {
		tail := appendTail("", piece())
		a, b := piece(), piece()
		split := appendTail(appendTail(tail, a), b)
		joined := appendTail(tail, a+b)
		if split != joined {
			t.Fatalf("tail %q + %q + %q:\nsplit:  %q\njoined: %q", tail, a, b, split, joined)
		}
	}
}

// runningCall opens a turn with one running Bash call, c1, and returns the
// next free seq.
func runningCall(t *testing.T, s *State) int64 {
	t.Helper()
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: proto.KindExecute, Title: "make", Status: proto.StatusInProgress,
	}))
	return 3
}

func output(t *testing.T, seq int64, delta string) proto.Event {
	t.Helper()
	return event(t, seq, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: "c1", OutputDelta: delta})
}

func call(t *testing.T, s *State) Item {
	t.Helper()
	it, ok := s.ItemByID("c1")
	if !ok {
		t.Fatal("no item c1")
	}
	return it
}

// A running call's output deltas fold into a tail bounded to its last lines,
// and they are only a view of it: Content is the call's own result, and
// stays empty until that arrives. Mirrored by web/src/apply.test.ts.
func TestOutputDeltasFoldIntoABoundedTail(t *testing.T) {
	s := New("s1")
	seq := runningCall(t, s)
	s.Apply(output(t, seq, "compiling a"))
	s.Apply(output(t, seq+1, "\ncompiling b\n"))
	if it := call(t, s); it.Tail != "compiling a\ncompiling b\n" || len(it.Content) != 0 {
		t.Fatalf("after two deltas: tail %q content %+v", it.Tail, it.Content)
	}

	seq += 2
	for i := 1; i <= 100; i++ {
		s.Apply(output(t, seq, "line "+strconv.Itoa(i)+"\n"))
		seq++
	}
	if got := call(t, s).Tail; got != lines(81, 100) {
		t.Fatalf("tail after 100 more lines = %q, want the last 20", got)
	}
}

// Output alone says nothing about the turn: unlike a call going active, it
// does not reopen an idle thread. Mirrored by web/src/apply.test.ts.
func TestOutputDeltaWhileIdleLeavesThePhase(t *testing.T) {
	s := New("s1")
	s.Apply(output(t, 1, "late\n"))
	if s.Phase != "idle" {
		t.Fatalf("phase after an output delta while idle = %q, want idle", s.Phase)
	}
}

// Finishing drops the tail, whatever the finish: the result lands whole in
// Content, and output straggling in after it is not shown as if the call were
// still running. Mirrored by web/src/apply.test.ts.
func TestFinishingDropsTheTail(t *testing.T) {
	s := New("s1")
	seq := runningCall(t, s)
	s.Apply(output(t, seq, "partial\n"))
	s.Apply(event(t, seq+1, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: "c1", Status: proto.StatusCompleted, Content: []proto.ToolContent{{Type: "text", Text: "partial\ndone\n"}},
	}))
	it := call(t, s)
	if it.Tail != "" || len(it.Content) != 1 || it.Content[0].Text != "partial\ndone\n" {
		t.Fatalf("after completion: tail %q content %+v", it.Tail, it.Content)
	}

	s.Apply(output(t, seq+2, "straggler\n"))
	if it := call(t, s); it.Tail != "" || it.Status != proto.StatusCompleted {
		t.Fatalf("after a straggling delta: tail %q status %q", it.Tail, it.Status)
	}

	// A delta carried on the finishing update itself is dropped with the rest.
	s2 := New("s2")
	seq = runningCall(t, s2)
	s2.Apply(event(t, seq, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: "c1", Status: proto.StatusFailed, OutputDelta: "exit 1\n",
	}))
	if it := call(t, s2); it.Tail != "" {
		t.Fatalf("tail after a failing update with output = %q", it.Tail)
	}
}

// A call the turn's finish cancels, and one its job's finish settles, drop
// their tails like any other finish. Mirrored by web/src/apply.test.ts.
func TestSettledCallsDropTheTail(t *testing.T) {
	s := New("s1")
	seq := runningCall(t, s)
	s.Apply(output(t, seq, "still going\n"))
	s.Apply(event(t, seq+1, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopCancelled}))
	if it := call(t, s); it.Status != proto.StatusCancelled || it.Tail != "" {
		t.Fatalf("after turn.finished: status %q tail %q", it.Status, it.Tail)
	}

	s2 := New("s2")
	seq = runningCall(t, s2)
	s2.Apply(event(t, seq, proto.JobStarted, proto.JobPayload{JobID: "j1", ToolCallID: "c1", TaskType: "local_bash"}))
	s2.Apply(output(t, seq+1, "serving\n"))
	s2.Apply(event(t, seq+2, proto.JobFinished, proto.JobPayload{JobID: "j1", Status: proto.JobFailed}))
	if it := call(t, s2); it.Status != proto.StatusFailed || it.Tail != "" {
		t.Fatalf("after job.finished: status %q tail %q", it.Status, it.Tail)
	}
}
