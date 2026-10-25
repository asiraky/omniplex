package tts

import (
	"container/list"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// A message written for a screen reads badly aloud: hashes, paths, code
// names, tables and markdown leftovers. Before it is synthesised, a small,
// fast model rewrites it for the voice, on the account of the thread it came
// from. That takes seconds, so a long message is cut into parts that are
// rewritten side by side, and anything that fails is read as it came.

//go:embed rewrite.txt
var rewriteInstructions string

const (
	// partChars is where a part is cut once it is past it. Rewriting time
	// grows with the length of the answer and the audio waits for the
	// slowest part, so smaller parts finish sooner, at the cost of one CLI
	// start each and less context per part.
	partChars = 2000
	// partMaxChars cuts a part even in the middle of what looks like a
	// table, so one huge table cannot make one slow part.
	partMaxChars = 2 * partChars
	// rewriteWorkers bounds the CLIs running at once across every request:
	// each is a whole harness process.
	rewriteWorkers = 6
	// partTimeout gives up on one part. The part is then read as it came.
	partTimeout = 45 * time.Second
	// recentRewrites is how many finished rewrites are kept in memory. The
	// audio is cached on disk, but a clip is often asked for twice before
	// any of it exists: Safari probes with a range request, and a player
	// that fails asks again for the reason.
	recentRewrites = 32
	// downFor is how long a model that failed every part is left alone. A
	// model the account cannot use, or a harness signed out, fails the same
	// way every time, and each try costs the listener its wait.
	downFor = 10 * time.Minute
)

// Prompt asks a model one question: instructions and input, answer back.
type Prompt func(ctx context.Context, instructions, input string) (string, error)

// Rewriter rewrites text for the voice. The zero value is not usable; use
// NewRewriter.
type Rewriter struct {
	logf    func(string, ...any)
	sem     chan struct{}
	timeout time.Duration

	mu       sync.Mutex
	recent   map[string]*list.Element // of rewritten
	order    *list.List
	inflight map[string]*rewriteCall
	down     map[string]time.Time // model → when it last failed every part
}

type rewritten struct {
	key  string
	text string
}

type rewriteCall struct {
	done  chan struct{}
	text  string
	whole bool
}

// NewRewriter logs a part that could not be rewritten with logf.
func NewRewriter(logf func(string, ...any)) *Rewriter {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Rewriter{
		logf:     logf,
		sem:      make(chan struct{}, rewriteWorkers),
		timeout:  partTimeout,
		recent:   map[string]*list.Element{},
		order:    list.New(),
		inflight: map[string]*rewriteCall{},
		down:     map[string]time.Time{},
	}
}

// RewriteKey names text as model rewrites it with these instructions. Put in
// place of the text in the audio's cache key, it means a change to either is
// never answered with audio made the old way.
func RewriteKey(model, text string) string {
	sum := sha256.Sum256([]byte(rewriteInstructions))
	return "rewrite:" + model + ":" + hex.EncodeToString(sum[:6]) + "\n" + text
}

// Down reports whether model failed every part of a recent rewrite, in which
// case a caller should read text as it came rather than wait for it to fail
// again.
func (r *Rewriter) Down(model string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.down[model]
	return ok && time.Since(at) < downFor
}

// Rewrite returns text rewritten for the voice by model through prompt, and
// whether all of it was. A part the model failed on is left as it came,
// which reads worse but still reads; a caller should not keep audio made
// from a text that is not whole, so the next play tries again.
//
// The same text asked for again while it is being rewritten waits for that
// rewrite rather than starting another. The rewrite outlives a caller that
// gives up, so the request after it finds the answer.
func (r *Rewriter) Rewrite(ctx context.Context, model string, prompt Prompt, text string) (string, bool) {
	key := RewriteKey(model, text)
	r.mu.Lock()
	if el, ok := r.recent[key]; ok {
		r.order.MoveToFront(el)
		r.mu.Unlock()
		return el.Value.(rewritten).text, true
	}
	call, running := r.inflight[key]
	if !running {
		call = &rewriteCall{done: make(chan struct{})}
		r.inflight[key] = call
	}
	r.mu.Unlock()

	if !running {
		go r.run(context.WithoutCancel(ctx), model, key, call, prompt, text)
	}
	select {
	case <-call.done:
		return call.text, call.whole
	case <-ctx.Done():
		return text, false
	}
}

func (r *Rewriter) run(ctx context.Context, model, key string, call *rewriteCall, prompt Prompt, text string) {
	parts := splitParts(text)
	out := make([]string, len(parts))
	var failed atomic.Int32
	var wg sync.WaitGroup
	for i, part := range parts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok := r.rewritePart(ctx, prompt, part)
			out[i] = got
			if !ok {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()
	whole := failed.Load() == 0
	joined := strings.Join(out, "\n\n")
	if whole && utf8.RuneCountInString(joined) > MaxChars {
		r.logf("tts rewrite: %d characters is too long to read, reading the message as it came", utf8.RuneCountInString(joined))
		joined, whole = text, false
	}

	r.mu.Lock()
	delete(r.inflight, key)
	if int(failed.Load()) == len(parts) {
		r.down[model] = time.Now()
	} else {
		delete(r.down, model)
	}
	if whole {
		r.recent[key] = r.order.PushFront(rewritten{key: key, text: joined})
		for r.order.Len() > recentRewrites {
			oldest := r.order.Back()
			r.order.Remove(oldest)
			delete(r.recent, oldest.Value.(rewritten).key)
		}
	}
	r.mu.Unlock()
	call.text, call.whole = joined, whole
	close(call.done)
}

// rewritePart rewrites one part, or returns it as it came.
func (r *Rewriter) rewritePart(ctx context.Context, prompt Prompt, part string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		r.logf("tts rewrite: no worker free in time")
		return part, false
	}
	got, err := prompt(ctx, rewriteInstructions, "<text>\n"+part+"\n</text>")
	if err != nil {
		r.logf("tts rewrite: %v", err)
		return part, false
	}
	got = strings.TrimSpace(got)
	got = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(got, "<text>"), "</text>"))
	if got == "" {
		r.logf("tts rewrite: the model answered with nothing")
		return part, false
	}
	return got, true
}

// tableRow is a line as speech.ts reads a table row: a short label, a colon,
// then values separated by commas.
var tableRow = regexp.MustCompile(`^[^:\n]{1,60}: [^\n]*, `)

// splitParts cuts text into parts of about partChars, at line ends. A part
// past partChars runs on while the next line looks like the next row of a
// table, so a table is rewritten whole when it can be: summarising half a
// table is how a model gets the totals wrong.
func splitParts(text string) []string {
	lines := strings.Split(text, "\n")
	var parts []string
	var cur strings.Builder
	for i, line := range lines {
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
		last := i == len(lines)-1
		inTable := !last && tableRow.MatchString(line) && tableRow.MatchString(lines[i+1])
		if last || cur.Len() >= partMaxChars || (cur.Len() >= partChars && !inTable) {
			if p := strings.TrimSpace(cur.String()); p != "" {
				parts = append(parts, p)
			}
			cur.Reset()
		}
	}
	return parts
}
