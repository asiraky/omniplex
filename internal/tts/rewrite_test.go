package tts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func body(input string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(input, "<text>"), "</text>"))
}

// lines is n lines of about width characters, each naming its number.
func lines(n, width int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("Line %d %s.", i, strings.Repeat("x", width))
	}
	return out
}

func TestSplitPartsCutsLongTextAtLineEnds(t *testing.T) {
	in := lines(60, 100)
	parts := splitParts(strings.Join(in, "\n"))
	if len(parts) < 2 {
		t.Fatalf("got %d parts, want a long text cut up", len(parts))
	}
	var back []string
	for _, p := range parts {
		if len(p) > partMaxChars {
			t.Fatalf("a part is %d characters, over %d", len(p), partMaxChars)
		}
		back = append(back, strings.Split(p, "\n")...)
	}
	if strings.Join(back, "\n") != strings.Join(in, "\n") {
		t.Fatal("the parts put back together are not the text")
	}
}

func TestSplitPartsKeepsShortTextWhole(t *testing.T) {
	parts := splitParts("One.\nTwo.\n\nThree.")
	if len(parts) != 1 || parts[0] != "One.\nTwo.\n\nThree." {
		t.Fatalf("got %q", parts)
	}
}

func TestSplitPartsKeepsATableTogether(t *testing.T) {
	lead := strings.Repeat("Words before the table. ", 80) // past partChars on its own
	var rows []string
	for i := range 10 {
		rows = append(rows, fmt.Sprintf("Row %d: Price $%d, Latency %d ms", i, i, i*10))
	}
	text := lead + "\n" + strings.Join(rows, "\n") + "\nAfter."
	for _, p := range splitParts(text) {
		if strings.Contains(p, "Row 0:") != strings.Contains(p, "Row 9:") {
			t.Fatalf("the table was cut: %q", p)
		}
	}
}

func TestRewriteRewritesEveryPart(t *testing.T) {
	r := NewRewriter(nil)
	text := strings.Join(lines(60, 100), "\n")
	got, whole := r.Rewrite(context.Background(), "m", func(_ context.Context, _, input string) (string, error) {
		return "<text>\n" + strings.ToUpper(body(input)) + "\n</text>", nil
	}, text)
	if !whole {
		t.Fatal("not whole")
	}
	if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(strings.ToUpper(text)), " ") {
		t.Fatal("the parts came back out of order, or the echoed tags were kept")
	}
}

func TestRewriteLeavesAFailedPartAsItCame(t *testing.T) {
	r := NewRewriter(nil)
	text := strings.Join(lines(60, 100), "\n")
	var n atomic.Int32
	failing := func(_ context.Context, _, input string) (string, error) {
		if strings.Contains(input, "Line 0 ") {
			return "", errors.New("down")
		}
		n.Add(1)
		return "  ", nil // an empty answer fails the part too
	}
	got, whole := r.Rewrite(context.Background(), "m", failing, text)
	if whole || strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(text), " ") {
		t.Fatalf("whole = %v; want every part as it came", whole)
	}
	// Not remembered: the next ask tries again.
	before := n.Load()
	r.Rewrite(context.Background(), "m", failing, text)
	if n.Load() == before {
		t.Fatal("a failed rewrite was remembered")
	}
}

func TestRewriteRemembersAndSharesWork(t *testing.T) {
	r := NewRewriter(nil)
	var calls atomic.Int32
	release := make(chan struct{})
	prompt := func(_ context.Context, _, input string) (string, error) {
		calls.Add(1)
		<-release
		return "Said.", nil
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, whole := r.Rewrite(context.Background(), "m", prompt, "Say."); got != "Said." || !whole {
				t.Errorf("got %q, %v", got, whole)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	r.Rewrite(context.Background(), "m", prompt, "Say.")
	if calls.Load() != 1 {
		t.Fatalf("the model was asked %d times for one text, want once", calls.Load())
	}
	r.Rewrite(context.Background(), "other", prompt, "Say.")
	if calls.Load() != 2 {
		t.Fatal("another model's rewrite was served from memory")
	}
}

func TestRewriteOutlivesACallerThatGivesUp(t *testing.T) {
	r := NewRewriter(nil)
	release := make(chan struct{})
	var calls atomic.Int32
	prompt := func(ctx context.Context, _, _ string) (string, error) {
		calls.Add(1)
		select {
		case <-release:
			return "Said.", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if got, whole := r.Rewrite(ctx, "m", prompt, "Say."); got != "Say." || whole {
			t.Errorf("a caller that gave up got %q, %v; want the text as it came", got, whole)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	close(release)
	if got, whole := r.Rewrite(context.Background(), "m", prompt, "Say."); got != "Said." || !whole {
		t.Fatalf("the next caller got %q, %v; want the rewrite the first one started", got, whole)
	}
	if calls.Load() != 1 {
		t.Fatalf("asked %d times, want once", calls.Load())
	}
}

func TestRewriteGivesUpOnASlowPart(t *testing.T) {
	r := NewRewriter(nil)
	r.timeout = 20 * time.Millisecond
	got, whole := r.Rewrite(context.Background(), "m", func(ctx context.Context, _, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}, "Say.")
	if got != "Say." || whole {
		t.Fatalf("got %q, %v; want the text as it came", got, whole)
	}
}

func TestRewriteMarksAModelThatFailsEverythingDown(t *testing.T) {
	r := NewRewriter(nil)
	fail := func(context.Context, string, string) (string, error) { return "", errors.New("signed out") }
	ok := func(context.Context, string, string) (string, error) { return "Said.", nil }
	if r.Down("m") {
		t.Fatal("down before it was asked")
	}
	r.Rewrite(context.Background(), "m", fail, "Say.")
	if !r.Down("m") || r.Down("other") {
		t.Fatal("a model that failed outright is not down, or another one is")
	}
	r.Rewrite(context.Background(), "m", ok, "Say again.")
	if r.Down("m") {
		t.Fatal("still down after it answered")
	}
}
