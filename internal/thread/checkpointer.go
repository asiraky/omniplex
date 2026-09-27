package session

import (
	"context"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// checkpointer keeps the snapshots that bracket each turn, and turns a finished
// turn into the file list a card renders.
//
// Every turn is measured between two snapshots of its own: one taken before the
// harness is told anything, one taken after it stops. Chaining them instead —
// reusing each turn's closing snapshot as the next turn's baseline — would be
// one snapshot cheaper, but it makes every gap a lie. A snapshot that fails, is
// dropped, or never happens leaves the next turn measured from further back than
// it should be, reporting work somebody else did; and anything a person edits
// between turns is billed to the agent. A card that is missing is a card nobody
// reads. A card that is wrong is one somebody believes.
//
// Snapshots all happen on one goroutine, in the order the jobs arrive, which is
// what lets the baseline pass from job to job without a lock.
type checkpointer struct {
	sessionID string
	root      string
	emit      func(proto.Emission)
	logf      func(string, ...any)

	jobs chan checkpointJob
	// ctx is cancelled by stop, which kills any Git process still running.
	ctx      context.Context
	cancel   context.CancelFunc
	finished chan struct{}
	once     sync.Once
}

// checkpointJob is one unit of work for the snapshot goroutine.
type checkpointJob struct {
	// kind is baseline or turn.
	kind   string
	turnID string
	// ack is closed once the job has been handled, so a caller can wait for the
	// baseline to exist before letting the harness touch the checkout.
	ack chan struct{}
}

const (
	jobBaseline = "baseline"
	jobTurn     = "turn"
)

// A snapshot of a large checkout is slow, but it is not unbounded.
const checkpointTimeout = 2 * time.Minute

// Turns arrive one at a time and contribute two jobs each, so this only has to
// absorb a burst. A queue that grows without limit would be a leak.
const checkpointQueue = 16

// How long to wait for the snapshot goroutine to notice it has been stopped.
// Cancelling kills the Git process, so this is a backstop, not the usual path.
const checkpointStopGrace = 5 * time.Second

// newCheckpointer starts the snapshot goroutine for a session, or answers nil
// when the session has no Git checkout to snapshot — which is not a failure,
// only a session whose turns will carry no cards.
func newCheckpointer(cwd, sessionID string, emit func(proto.Emission), logf func(string, ...any)) *checkpointer {
	probe, cancelProbe := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelProbe()

	root := repoRoot(probe, cwd)
	if root == "" {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &checkpointer{
		sessionID: sessionID,
		root:      root,
		emit:      emit,
		logf:      logf,
		jobs:      make(chan checkpointJob, checkpointQueue),
		ctx:       ctx,
		cancel:    cancel,
		finished:  make(chan struct{}),
	}
	go c.run()
	return c
}

// baseline takes the snapshot a turn will be measured against, and waits for it.
//
// The wait is the point. A baseline taken while the harness is already writing
// has the turn's own early edits inside it, and those edits then go missing from
// the card — the turn looks like it did less than it did. So the prompt does not
// go out until the picture of the checkout beforehand exists.
//
// It answers whether the baseline is usable. When it is not, the turn gets no
// card at all: measuring against a baseline of unknown age would produce a list
// that looks authoritative and is not.
func (c *checkpointer) baseline(ctx context.Context, turnID string) bool {
	if c == nil || turnID == "" {
		return false
	}
	ack := make(chan struct{})
	if !c.submit(checkpointJob{kind: jobBaseline, turnID: turnID, ack: ack}) {
		return false
	}
	select {
	case <-ack:
		return true
	case <-ctx.Done():
		// The turn goes ahead regardless: a prompt that never reaches the
		// harness costs far more than a missing card.
		c.logf("baseline for %s did not settle in time; this turn gets no file list", c.sessionID)
		return false
	case <-c.ctx.Done():
		return false
	}
}

// turnEnded snapshots the checkout and reports what the turn changed. It is only
// called for a turn whose baseline settled.
func (c *checkpointer) turnEnded(turnID string) {
	if c == nil || turnID == "" {
		return
	}
	c.submit(checkpointJob{kind: jobTurn, turnID: turnID})
}

// submit drops the job rather than blocking the actor loop. A missing card is a
// far smaller problem than a session that stops answering. It reports whether
// the job was taken, so a caller waiting on an ack does not wait for one that
// will never come.
func (c *checkpointer) submit(job checkpointJob) bool {
	select {
	case c.jobs <- job:
		return true
	case <-c.ctx.Done():
		return false
	default:
		c.logf("checkpoint queue full on %s; skipping a turn's file list", c.sessionID)
		return false
	}
}

// stop ends the snapshot goroutine and waits for it, killing any Git process it
// has running. Waiting is what makes drop safe: a capture still in flight would
// otherwise write back a ref that drop has already deleted.
func (c *checkpointer) stop() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.cancel()
		select {
		case <-c.finished:
		case <-time.After(checkpointStopGrace):
			c.logf("checkpoint worker for %s did not stop in time", c.sessionID)
		}
	})
}

// drop removes every snapshot this session took. It must be called after stop,
// so that nothing is still writing refs.
func (c *checkpointer) drop() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := dropCheckpoints(ctx, c.root, c.sessionID); err != nil {
		c.logf("dropping checkpoints for %s: %v", c.sessionID, err)
	}
}

func (c *checkpointer) run() {
	defer close(c.finished)

	// base is the snapshot the turn now in flight will be measured against, and
	// belongs to this goroutine alone. It is cleared as soon as it has been
	// spent, so it can never be used for a turn it was not taken for.
	var base string

	for {
		select {
		case <-c.ctx.Done():
			return
		case job := <-c.jobs:
			base = c.handle(job, base)
			if job.ack != nil {
				close(job.ack)
			}
		}
	}
}

// handle takes one snapshot, and answers with the baseline that is now standing.
func (c *checkpointer) handle(job checkpointJob, base string) string {
	ctx, cancel := context.WithTimeout(c.ctx, checkpointTimeout)
	defer cancel()

	oid, err := captureCheckpoint(ctx, c.root, checkpointRef(c.sessionID, job.turnID, edgeFor(job.kind)))
	if err != nil {
		// Cancellation is a shutdown, not a fault worth telling a reader about.
		if c.ctx.Err() == nil {
			c.logf("checkpoint for %s: %v", c.sessionID, err)
			if job.kind == jobTurn {
				c.report(job.turnID, TurnChanges{}, err)
			}
		}
		return ""
	}

	if job.kind == jobBaseline {
		return oid
	}

	// No baseline: this turn cannot be measured, and guessing at one is how a
	// card comes to describe somebody else's work.
	if base == "" {
		return ""
	}

	changes, err := diffCheckpoints(ctx, c.root, base, oid)
	switch {
	case c.ctx.Err() != nil:
		// Shutting down; reporting now would race the log closing behind us.
	case err != nil:
		c.logf("turn diff for %s: %v", c.sessionID, err)
		c.report(job.turnID, TurnChanges{}, err)
	case len(changes.Files) > 0:
		c.report(job.turnID, changes, nil)
		// A turn that changed nothing gets no card: "0 files changed" is noise
		// on every turn that was only a question.
	}
	return ""
}

// edgeFor names which end of a turn a snapshot is.
func edgeFor(kind string) string {
	if kind == jobBaseline {
		return "base"
	}
	return "end"
}

// report puts the turn's file list in the log, where it survives a restart and
// replays to every presenter that attaches later.
func (c *checkpointer) report(turnID string, changes TurnChanges, err error) {
	payload := proto.TurnDiffPayload{
		TurnID:    turnID,
		Files:     changes.Files,
		Additions: changes.Additions,
		Deletions: changes.Deletions,
		Truncated: changes.Truncated,
	}
	if payload.Files == nil {
		payload.Files = []proto.ChangedFile{}
	}
	if err != nil {
		// The card says what went wrong rather than pretending the turn changed
		// nothing: an empty list and a failed snapshot look identical otherwise.
		payload.Error = err.Error()
	}
	c.emit(proto.Emit(proto.TurnDiff, payload))
}
