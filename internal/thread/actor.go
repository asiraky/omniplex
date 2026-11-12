// Package thread owns the running threads: one goroutine per thread, which
// is the only thing that mutates that thread's state. Fanout to presenters is
// non-blocking; a slow consumer is dropped and resynced, never buffered without
// limit and never allowed to stall a turn.
package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// Tunables named by the spec's invariants.
const (
	MaxReplayEvents = 128     // a longer catch-up is sent as a snapshot
	MaxReplayBytes  = 1 << 20 // so is one whose wire payloads outweigh this
	SubscriberQueue = 256     // bounded per-connection outbound queue
	SnapshotEvery   = 200     // snapshot cadence, a latency cache only
)

// Subscriber receives live events for one thread. Ch is closed when the
// subscription ends. Resync is closed if the server dropped the queue.
type Subscriber struct {
	ID              string
	Ch              chan proto.Event
	Resync          chan struct{}
	ComposerChanged chan struct{}

	dropped bool
}

// Actor is one live thread.
type Actor struct {
	scheduleReady   *sync.Map
	activationError error
	// preparing is true from StartPending until the harness starts. A prompt
	// sent meanwhile waits in the queue, which is what lets the first message
	// create the thread: it is there for the agent the moment it starts.
	preparing bool
	ID        string
	Harness   string
	Cwd       string

	store   *store.Store
	adapter adapter.Adapter
	// env is the provider instance's credential overlay, applied whenever this
	// actor spawns its harness — at start, at activation, and on resume. It
	// and adapter change only on an account switch, between processes.
	env  map[string]string
	sess adapter.Session

	inbox chan command
	quit  chan struct{}
	wg    sync.WaitGroup

	// Owned by the actor goroutine only.
	state         *projection.State
	lastSnapAt    int64
	pendingPerm   map[string]chan adapter.PermissionOutcome
	pendingElicit map[string]chan adapter.ElicitationResult
	turnActive    string
	// stoppedTurn is the last turn a human stopped. It never gets a usage
	// limit continuation, however late its finish says it hit one.
	stoppedTurn string
	// sent holds the queue ids the running harness process has been handed
	// (adapter.Steerer) and not yet reported reading. Those are the harness's
	// to run; dispatchQueued skips them. It is knowledge about this process,
	// so it is not in the log: a restart starts a new harness that knows
	// nothing, and the log's queue is dispatched afresh.
	sent map[string]bool
	// stopping is the Stop in progress: the turn it was asked to end, and the
	// timer that kills the harness if that turn has not ended in time.
	stopping *stopRequest
	// controlTail is closed once the last control call handed to inOrder has
	// posted its result; the next one waits on it. Nil for a process that
	// has had none.
	controlTail chan struct{}
	// controlPending counts the control calls handed to inOrder whose result
	// has not come back. A prompt waits behind them, so a model change made
	// just before sending is the model the prompt runs on.
	controlPending int
	// process numbers the harness processes this actor has run. It moves on
	// whenever one is started or dropped, and the host services handed to a
	// process carry the number it was started under, so a question from one
	// this actor no longer runs can be told from the current one's.
	process uint64

	mu   sync.Mutex // guards subs and headSeq for readers outside the loop
	subs map[string]*Subscriber
	head int64
	// attention caches state.Attention() for readers outside the loop — the
	// thread list wants it without a round trip through the inbox. Written
	// only by the actor goroutine; the projection stays the source of truth.
	attention string

	// onExit lets the manager forget a disposed actor, so the next attach
	// resumes the thread from the log instead of finding a dead one.
	onExit func()
	// onPhase fires when the thread moves between idle and turn, so a
	// thread list rendered elsewhere can follow along.
	onPhase func()

	// recovery is set by Resume when the log shows a turn the server died in
	// the middle of, or background work the agent was waiting on. Recover
	// consumes it; see recovery.go.
	recovery *recoveryPlan
	// heldNote is dead work the agent is owed news of that did not warrant a
	// turn of its own: shells a restart killed on an idle thread. The next
	// prompt a human sends carries it. Actor-loop only.
	heldNote string

	// cardLive says whether the server still holds a card, so resuming the
	// harness leaves it answerable. Set by the manager at adopt time; nil
	// means none is held. Guarded by mu.
	cardLive func(requestID string) bool

	// imagePath finds the host path of a stored image by thread and id,
	// for queued prompts whose images came back out of the log without one.
	imagePath func(threadID, imageID string) (string, error)

	// quotaSink receives the account-level usage-limit pushes the harness
	// streams (Claude rate-limit events, Codex rateLimits notifications). Set
	// by the manager at adopt time, bound to the instance the thread runs
	// under; nil means the pushes go nowhere, which is what a thread without
	// a manager (tests) wants.
	quotaSink       func(adapter.QuotaSnapshot)
	quotaGeneration uint64

	// checkpoints snapshots the checkout around each turn, so a finished turn
	// can say which files it changed. Nil when the thread has no Git checkout
	// to snapshot.
	checkpoints *checkpointer
	// measuring is the turn whose baseline was taken before the harness was
	// told anything. Only that turn can be measured: any other would be
	// compared against a picture of the checkout from the wrong moment.
	measuring string

	// deltas holds a streamed delta for up to deltaWindow, and deltaFlush
	// fires when the window closes. deltaFlush is nil when nothing is held.
	deltas     coalescer
	deltaFlush *time.Timer

	logf func(string, ...any)

	// evictable marks an actor restored only to be looked at (RestoreIdle,
	// RestoreClosed). Such an actor holds a whole projection and nothing
	// else, so the manager may drop it once nobody has used it for a while;
	// see evict.go. Set before the loop starts and never changed.
	evictable bool
	// lastUsed is when a caller last reached this actor: a command, a
	// subscription coming or going, or the manager handing it out. Guarded
	// by mu.
	lastUsed time.Time
}

type command struct {
	schedule     *proto.ScheduledPrompt
	now          int64
	kind         string
	prompt       string
	images       []proto.PromptImage
	reqID        string
	outcome      adapter.PermissionOutcome
	perm         permAsk
	elicit       elicitAsk
	elicitResult adapter.ElicitationResult
	emission     *proto.Emission
	hard         bool // close: append thread.closed, rather than just disposing
	resume       bool // activate: resume an existing harness conversation
	reply        chan cmdResult
	model        string
	mode         string
	effort       string
	recovery     *proto.TurnRecovery // prompt: set when the server started this turn itself
	plan         *recoveryPlan       // prompt: the restart continuation Recover is sending
	// from is the harness process a harness event or exit came from. A
	// process replaced by an account switch, or killed by Stop, still drains
	// its last events and its exit; those are about a process this actor no
	// longer runs. A reply is not filtered by it: whoever asked is waiting on
	// the answer, and each reply's onReply checks what it acts on itself.
	from adapter.Session
	// process is the generation of the harness process a question came from
	// (Actor.process).
	process uint64
	// onReply is what a harness reply does once it is back on the loop.
	onReply func()
	stop    *stopRequest
	account *accountSwitch
	show    *Show
	mcp     func(context.Context, adapter.MCPControl) (any, error)
	card    *cardCmd
	// evict commits an eviction with the manager; see Actor.evict.
	evict func() (idle time.Duration, ok bool)
	// read runs against the live projection inside the loop; see Actor.read.
	read func(*projection.State) any
}

// accountSwitch is what the manager hands the actor to move the thread to
// another account: the new account's adapter and env, and move, which carries
// the harness's own record of the conversation across and records the new
// account durably. move runs with no harness process alive.
type accountSwitch struct {
	ad      adapter.Adapter
	env     map[string]string
	move    func(harnessSessionID string) error
	changed proto.ThreadAccountChangedPayload
}

type permAsk struct {
	req adapter.PermissionRequest
	ch  chan adapter.PermissionOutcome
}

type elicitAsk struct {
	req adapter.ElicitationRequest
	ch  chan adapter.ElicitationResult
}

type cmdResult struct {
	value any
	err   error
}

const (
	cmdPrompt        = "prompt"
	cmdShowArtefact  = "show_artefact"
	cmdArtefact      = "artefact"
	cmdCancel        = "cancel"
	cmdResolvePerm   = "resolve_permission"
	cmdAskPerm       = "ask_permission"
	cmdResolveElicit = "resolve_elicitation"
	cmdAskElicit     = "ask_elicitation"
	cmdClose         = "close"
	cmdActivate      = "activate"
	cmdSetMode       = "set_mode"
	cmdSetModel      = "set_model"
	cmdSetEffort     = "set_effort"
	cmdListComposer  = "list_composer_items"
	cmdRunComposer   = "run_composer_action"
	cmdHarnessEvent  = "harness_event"
	cmdHarnessExit   = "harness_exit"
	cmdContinue      = "continue"
	cmdStopJob       = "stop_job"
	cmdDequeue       = "dequeue_prompt"
	cmdQuota         = "quota"
	cmdSwitchAccount = "switch_account"
	cmdMCP           = "mcp"
	cmdRaiseCard     = "raise_card"
	cmdResolveCard   = "resolve_card"
	cmdHarnessReply  = "harness_reply"
	cmdStopExpired   = "stop_expired"
	cmdDropRecovery  = "drop_recovery"
	cmdEvict         = "evict"
	cmdRead          = "read"
)

// ErrBusy is returned when a composer action arrives while a turn is already
// running. A plain prompt is never refused for this: it queues instead.
var ErrBusy = errors.New("a turn is already in progress")

// ErrAlreadySent is returned when asked to remove a queued prompt the harness
// already holds: it reads it at its next step, and there is no taking it back.
var ErrAlreadySent = errors.New("the harness already has that prompt")

// ErrNotQueued is returned when asked to remove a queued prompt that is not
// waiting — it already started, or was removed by someone else.
var ErrNotQueued = errors.New("that prompt is no longer queued")

// ErrClosed is returned when a command tries to mutate a closed transcript.
var ErrClosed = errors.New("thread is closed")
var ErrNotReady = errors.New("workspace is not ready")

// ErrAlreadyResolved is returned to the loser of a permission race. It is an
// ack, not a failure: the request was answered, just not by this presenter.
var ErrAlreadyResolved = errors.New("already_resolved")

// Start creates a harness session and its actor goroutine. env is the
// provider instance's credential overlay; nil means ambient credentials.
func Start(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.ThreadMeta, model, mode string, env map[string]string, logf func(string, ...any)) (*Actor, error) {
	a := &Actor{
		ID:            meta.ID,
		Harness:       meta.Harness,
		Cwd:           meta.Cwd,
		store:         st,
		adapter:       ad,
		env:           env,
		inbox:         make(chan command, 64),
		quit:          make(chan struct{}),
		state:         projection.New(meta.ID),
		pendingPerm:   map[string]chan adapter.PermissionOutcome{},
		pendingElicit: map[string]chan adapter.ElicitationResult{},
		subs:          map[string]*Subscriber{},
		logf:          logf,
	}

	x := harnessExtras(ctx, st, ad, meta, meta.Cwd, logf)
	sess, err := ad.CreateSession(ctx, a.newHost(), x.options(adapter.CreateOptions{
		ThreadID: meta.ID, Cwd: meta.Cwd, Model: model, Mode: mode, Effort: meta.Effort, Env: env,
	}))
	if err != nil {
		return nil, err
	}
	a.sess = sess

	a.startCheckpoints()

	a.wg.Add(1)
	go a.run()
	a.pump(sess)

	// thread.created is the first event in every thread's log.
	a.enqueueEmission(proto.Emit(proto.ThreadCreated, proto.ThreadCreatedPayload{
		Cwd: meta.Cwd, Harness: meta.Harness, Model: model, Mode: mode, Effort: meta.Effort, Title: meta.Title,
	}))
	return a, nil
}

// StartPending creates an attachable actor without starting a harness. The
// lifecycle runner activates it only after provisioning has completed.
func StartPending(st *store.Store, ad adapter.Adapter, meta store.ThreadMeta, env map[string]string, logf func(string, ...any)) *Actor {
	a := &Actor{ID: meta.ID, Harness: meta.Harness, Cwd: meta.Cwd, store: st, adapter: ad, env: env,
		inbox: make(chan command, 64), quit: make(chan struct{}), state: projection.New(meta.ID),
		pendingPerm: map[string]chan adapter.PermissionOutcome{}, pendingElicit: map[string]chan adapter.ElicitationResult{},
		subs: map[string]*Subscriber{}, logf: logf, preparing: true}
	a.wg.Add(1)
	go a.run()
	a.enqueueEmission(proto.Emit(proto.ThreadCreated, proto.ThreadCreatedPayload{Cwd: meta.Cwd, Harness: meta.Harness, Model: meta.Model, Mode: meta.Mode, Effort: meta.Effort, Title: meta.Title}))
	return a
}

// Resume rebuilds an actor for an existing thread id, replaying its log into
// the projection before starting a fresh harness process.
func Resume(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.ThreadMeta, env map[string]string, logf func(string, ...any)) (*Actor, error) {
	state, _, err := loadState(ctx, st, meta.ID)
	if err != nil {
		return nil, err
	}

	a := &Actor{
		ID:            meta.ID,
		Harness:       meta.Harness,
		Cwd:           meta.Cwd,
		store:         st,
		adapter:       ad,
		env:           env,
		inbox:         make(chan command, 64),
		quit:          make(chan struct{}),
		state:         state,
		head:          state.Seq,
		pendingPerm:   map[string]chan adapter.PermissionOutcome{},
		pendingElicit: map[string]chan adapter.ElicitationResult{},
		subs:          map[string]*Subscriber{},
		logf:          logf,
	}

	// Decided here, while the projection is still this goroutine's: once the
	// actor loop starts, the state belongs to it. Closing the interrupted turn
	// below stops the UI lying about what is running, but it does not finish
	// the work — that is what Recover is for, and this is the evidence it
	// needs.
	a.recovery, a.heldNote = planRecovery(state, proto.NowMillis())
	a.holdRowOnlyIfAsked(meta.Phase)

	x := harnessExtras(ctx, st, ad, meta, meta.Cwd, logf)
	sess, err := ad.CreateSession(ctx, a.newHost(), x.options(adapter.CreateOptions{
		ThreadID:         meta.ID,
		Cwd:              meta.Cwd,
		Model:            state.Model,
		Mode:             state.Mode,
		Effort:           state.Effort,
		Resume:           true,
		HarnessSessionID: state.HarnessSessionID,
		Env:              env,
	}))
	if err != nil {
		return nil, err
	}
	a.sess = sess

	a.startCheckpoints()

	a.wg.Add(1)
	go a.run()
	a.pump(sess)

	// Server death ends the in-flight turn (the harness process is gone), but
	// the conversation remains resumable. Close every durable human request and
	// unfinished turn so the log-authoritative projection reopens idle rather
	// than leaving every presenter stuck on a Stop button forever.
	for _, pending := range state.Pending {
		a.enqueueEmission(proto.Emit(proto.PermissionResolved, proto.PermissionResolvedPayload{
			RequestID: pending.RequestID, Outcome: proto.OutcomeCancelled,
		}))
	}
	for _, pending := range state.Elicitations {
		a.enqueueEmission(proto.Emit(proto.ElicitationResolved, proto.ElicitationResolvedPayload{
			RequestID: pending.RequestID, Action: "cancel",
		}))
	}
	for _, turn := range state.Turns {
		if !turn.Done {
			a.enqueueEmission(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: turn.ID, StopReason: proto.StopError,
				Error: "server restarted during turn", Failure: proto.FailureRestart,
			}))
		}
	}
	for _, em := range interruptedJobs(state) {
		a.enqueueEmission(em)
	}
	return a, nil
}

// RestoreIdle rebuilds the projection and actor surface without starting the
// provider process. It is used by read-only attachment; ActivateResume pays
// the process cost only when a later command actually needs the harness.
func RestoreIdle(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.ThreadMeta, env map[string]string, logf func(string, ...any)) (*Actor, error) {
	state, snapAt, err := loadState(ctx, st, meta.ID)
	if err != nil {
		return nil, err
	}
	a := &Actor{
		ID: meta.ID, Harness: meta.Harness, Cwd: meta.Cwd,
		store: st, adapter: ad, env: env, inbox: make(chan command, 64), quit: make(chan struct{}),
		state: state, head: state.Seq, pendingPerm: map[string]chan adapter.PermissionOutcome{},
		pendingElicit: map[string]chan adapter.ElicitationResult{}, subs: map[string]*Subscriber{}, logf: logf,
		lastSnapAt: snapAt, evictable: true, lastUsed: time.Now(),
	}
	a.recovery, a.heldNote = planRecovery(state, proto.NowMillis())
	a.holdRowOnlyIfAsked(meta.Phase)
	for _, turn := range state.Turns {
		if !turn.Done {
			a.turnActive = turn.ID
		}
	}
	a.wg.Add(1)
	go a.run()
	return a, nil
}

// RestoreClosed creates a read-only actor for a closed transcript. It has no
// harness process, but retains the same attach/state/fanout surface so closed
// threads remain inspectable by presenters.
func RestoreClosed(ctx context.Context, st *store.Store, meta store.ThreadMeta, logf func(string, ...any)) (*Actor, error) {
	state, snapAt, err := loadState(ctx, st, meta.ID)
	if err != nil {
		return nil, err
	}
	if !state.Closed {
		ev, err := st.Append(ctx, meta.ID, proto.Emit(proto.ThreadClosed, proto.ThreadClosedPayload{Reason: "closed"}))
		if err != nil {
			return nil, err
		}
		state.Apply(ev)
	}
	a := &Actor{
		ID: meta.ID, Harness: meta.Harness, Cwd: meta.Cwd,
		store: st, inbox: make(chan command, 64), quit: make(chan struct{}),
		state: state, head: state.Seq, pendingPerm: map[string]chan adapter.PermissionOutcome{},
		pendingElicit: map[string]chan adapter.ElicitationResult{},
		subs:          map[string]*Subscriber{}, logf: logf,
		lastSnapAt: snapAt, evictable: true, lastUsed: time.Now(),
	}
	a.wg.Add(1)
	go a.run()
	return a, nil
}

func RestorePending(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.ThreadMeta, env map[string]string, logf func(string, ...any)) (*Actor, error) {
	state, _, err := loadState(ctx, st, meta.ID)
	if err != nil {
		return nil, err
	}
	a := &Actor{ID: meta.ID, Harness: meta.Harness, Cwd: meta.Cwd, store: st, adapter: ad, env: env, inbox: make(chan command, 64), quit: make(chan struct{}), state: state, head: state.Seq, pendingPerm: map[string]chan adapter.PermissionOutcome{}, pendingElicit: map[string]chan adapter.ElicitationResult{}, subs: map[string]*Subscriber{}, logf: logf}
	a.wg.Add(1)
	go a.run()
	return a, nil
}

// loadState folds the whole log, using the newest snapshot as a starting
// point, and says which seq that snapshot was taken at, zero for none or for
// one worth rewriting. An actor that knows it is not asked to write the same
// snapshot again.
func loadState(ctx context.Context, st *store.Store, id string) (*projection.State, int64, error) {
	state := projection.New(id)
	var snapAt int64
	if seq, blob, err := st.LatestSnapshot(ctx, id); err == nil && blob != nil {
		if s, err := projection.FromSnapshot(blob); err == nil {
			state = s
			state.Seq = seq
			// A snapshot from before tool output was capped loads small but
			// is still big on disk; leaving snapAt at zero gets it rewritten.
			if !s.Recapped() {
				snapAt = seq
			}
		}
	}
	for {
		evs, err := st.ReadEvents(ctx, id, state.Seq, 1000)
		if err != nil {
			return nil, 0, err
		}
		if len(evs) == 0 {
			return state, snapAt, nil
		}
		for _, ev := range evs {
			state.Apply(ev)
		}
	}
}

// Head returns the current sequence number.
func (a *Actor) Head() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.head
}

// Attention returns the thread's derived attention state — see
// projection.Attention. Safe from any goroutine.
func (a *Actor) Attention() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.attention
}

// State returns a snapshot of the projection as of Head. It is produced inside
// the actor loop so it can never observe a half-applied event.
func (a *Actor) State(ctx context.Context) (*projection.State, error) {
	v, err := a.call(ctx, command{kind: "state"})
	if err != nil {
		return nil, err
	}
	return v.(*projection.State), nil
}

// read runs fn on the live projection inside the actor loop and returns what
// it made. fn may refresh the state's indexes, and must copy whatever it
// returns: the state goes on changing the moment it returns.
func (a *Actor) read(ctx context.Context, fn func(*projection.State) any) (any, error) {
	return a.call(ctx, command{kind: cmdRead, read: fn})
}

// WireState is the projection as a client should get it: windowed to maxTop
// top-level items, with tool input capped (see projection.State.WireCopy).
// Made from the live state, so only what is kept is ever copied.
func (a *Actor) WireState(ctx context.Context, maxTop int) (*projection.State, error) {
	v, err := a.read(ctx, func(s *projection.State) any { return s.WireCopy(maxTop) })
	if err != nil {
		return nil, err
	}
	return v.(*projection.State), nil
}

// ItemsPage is the page of at most maxTop top-level items ending before index
// `before`, the index it starts at, and the jobs its items spawned. See
// projection.State.WirePage.
func (a *Actor) ItemsPage(ctx context.Context, before, maxTop int) ([]projection.Item, int, []projection.Job, error) {
	type page struct {
		items []projection.Item
		start int
		jobs  []projection.Job
	}
	v, err := a.read(ctx, func(s *projection.State) any {
		items, start, jobs := s.WirePage(before, maxTop)
		return page{items, start, jobs}
	})
	if err != nil {
		return nil, 0, nil, err
	}
	p := v.(page)
	return p.items, p.start, p.jobs, nil
}

// Children is every child item of one parent, which a wire copy leaves out
// or trims. See projection.State.WireChildren.
func (a *Actor) Children(ctx context.Context, parentID string) ([]projection.Item, error) {
	v, err := a.read(ctx, func(s *projection.State) any { return s.WireChildren(parentID) })
	if err != nil {
		return nil, err
	}
	return v.([]projection.Item), nil
}

// Jobs is every job, which a wire copy windows.
func (a *Actor) Jobs(ctx context.Context) ([]projection.Job, error) {
	v, err := a.read(ctx, func(s *projection.State) any { return s.WireJobs() })
	if err != nil {
		return nil, err
	}
	return v.([]projection.Job), nil
}

// Subscribe registers a live listener. Callers must Unsubscribe.
//
// Subscribing happens before the caller reads history, which closes the window
// where an event lands between the read and the subscription.
func (a *Actor) Subscribe() *Subscriber {
	sub := &Subscriber{
		ID:              uuid.NewString(),
		Ch:              make(chan proto.Event, SubscriberQueue),
		Resync:          make(chan struct{}),
		ComposerChanged: make(chan struct{}, 1),
	}
	a.mu.Lock()
	select {
	case <-a.quit:
		// shutdown already drained the subscriber map, so registering now
		// would leave this channel open forever on an actor nobody can drive.
		close(sub.Ch)
	default:
		a.subs[sub.ID] = sub
	}
	a.lastUsed = time.Now()
	a.mu.Unlock()
	return sub
}

func (a *Actor) Unsubscribe(sub *Subscriber) {
	a.mu.Lock()
	delete(a.subs, sub.ID)
	// Idle is counted from the last viewer leaving, not from when they came.
	a.lastUsed = time.Now()
	a.mu.Unlock()
}

// ---- commands from presenters ----

func (a *Actor) call(ctx context.Context, c command) (any, error) {
	a.touch()
	c.reply = make(chan cmdResult, 1)
	select {
	case a.inbox <- c:
	case <-a.quit:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-c.reply:
		return r.value, r.err
	case <-a.quit:
		// The actor may stop after accepting this command but before handling
		// it (for example when a close command was already queued). Prefer a
		// reply that made it out during shutdown, otherwise do not strand a
		// caller on an inbox that nobody reads anymore.
		select {
		case r := <-c.reply:
			return r.value, r.err
		default:
			return nil, ErrClosed
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PromptResult says what became of a prompt: the turn it started, or the
// queue entry it became because a turn was already running.
type PromptResult struct {
	TurnID  string
	QueueID string
}

// Queued reports whether the prompt is waiting rather than running.
func (r PromptResult) Queued() bool { return r.QueueID != "" }

// Prompt starts a turn, or queues one if a turn is already running: the
// queued prompt starts its own turn once the thread is idle. Images are
// already stored on this host; each carries the path the harness reads it
// from.
func (a *Actor) Prompt(ctx context.Context, text string, images []proto.PromptImage) (PromptResult, error) {
	v, err := a.call(ctx, command{kind: cmdPrompt, prompt: text, images: images})
	if err != nil {
		return PromptResult{}, err
	}
	return v.(PromptResult), nil
}

// DequeuePrompt takes back a prompt that is still waiting its turn.
func (a *Actor) DequeuePrompt(ctx context.Context, queueID string) error {
	_, err := a.call(ctx, command{kind: cmdDequeue, reqID: queueID})
	return err
}

// Continue restarts work that ended in an error, on a human's say-so. It is
// the same continuation the server starts by itself after a restart, minus the
// attempt cap: someone is watching, and they can stop asking.
func (a *Actor) Continue(ctx context.Context) (string, error) {
	v, err := a.call(ctx, command{kind: cmdContinue})
	if err != nil {
		return "", err
	}
	return v.(PromptResult).TurnID, nil
}

func (a *Actor) Emit(ctx context.Context, em proto.Emission) error {
	_, err := a.call(ctx, command{kind: "emit", emission: &em})
	return err
}

func (a *Actor) Activate(ctx context.Context, cwd, model, mode, effort string) error {
	_, err := a.call(ctx, command{kind: cmdActivate, prompt: cwd, model: model, mode: mode, effort: effort})
	return err
}

// ActivateResume starts the provider process for an actor restored only for
// viewing. Attaching to a transcript must not pay this process-start cost;
// the first command that needs the harness does.
func (a *Actor) ActivateResume(ctx context.Context) error {
	_, err := a.call(ctx, command{kind: cmdActivate, resume: true})
	return err
}

// SetMode switches the harness's permission mode mid-thread and records the
// change as a thread.config_changed event, so every presenter sees it.
func (a *Actor) SetMode(ctx context.Context, mode string) error {
	_, err := a.call(ctx, command{kind: cmdSetMode, mode: mode})
	return err
}

// SetModel switches the harness's model mid-thread and records the change as
// a thread.config_changed event, so every presenter sees it.
func (a *Actor) SetModel(ctx context.Context, model string) error {
	_, err := a.call(ctx, command{kind: cmdSetModel, model: model})
	return err
}

// SetEffort switches the harness's reasoning effort mid-thread and records the
// change as a thread.config_changed event, so every presenter sees it.
func (a *Actor) SetEffort(ctx context.Context, effort string) error {
	_, err := a.call(ctx, command{kind: cmdSetEffort, effort: effort})
	return err
}

// ComposerItems asks the live adapter what this exact thread can invoke.
func (a *Actor) ComposerItems(ctx context.Context) ([]adapter.ComposerItem, error) {
	v, err := a.call(ctx, command{kind: cmdListComposer})
	if err != nil {
		return nil, err
	}
	return v.([]adapter.ComposerItem), nil
}

// RunComposerAction routes an opaque provider action as a canonical turn. The
// invocation is presentation only; the adapter still interprets action.
func (a *Actor) RunComposerAction(ctx context.Context, action, args, invocation string) (any, error) {
	return a.call(ctx, command{kind: cmdRunComposer, prompt: args, mode: action, model: invocation})
}

// Cancel interrupts the running turn. It is a command on the inbox, never a
// context cancellation: losing every client must not interrupt a turn.
func (a *Actor) Cancel(ctx context.Context) error {
	_, err := a.call(ctx, command{kind: cmdCancel})
	return err
}

// StopJob asks the harness to stop one running job by id. The job's own
// finish arrives through the harness's event stream.
func (a *Actor) StopJob(ctx context.Context, jobID string) error {
	_, err := a.call(ctx, command{kind: cmdStopJob, reqID: jobID})
	return err
}

// jobOutputChunk caps one reply: output is polled in pieces so a phone on a
// slow link never waits on a multi-megabyte shell log.
const jobOutputChunk = 64 * 1024

// JobOutput reads the job's output file from offset. next is where the next
// read starts; done is true once the job has finished and the read reached
// the end of the file. A job with no output file is refused.
func (a *Actor) JobOutput(ctx context.Context, jobID string, offset int64) (text string, next int64, done bool, err error) {
	state, err := a.State(ctx)
	if err != nil {
		return "", 0, false, err
	}
	var job *projection.Job
	for i := range state.Jobs {
		if state.Jobs[i].ID == jobID {
			job = &state.Jobs[i]
		}
	}
	if job == nil {
		return "", 0, false, fmt.Errorf("no job %q in this thread", jobID)
	}
	if job.OutputFile == "" {
		return "", 0, false, fmt.Errorf("job %q has no output file", jobID)
	}
	if offset < 0 {
		offset = 0
	}
	f, err := os.Open(job.OutputFile)
	if err != nil {
		if os.IsNotExist(err) {
			// The harness names the file before it writes it; an absent file
			// is an empty one, not an error.
			return "", offset, proto.JobDone(job.Status), nil
		}
		return "", 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, false, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, false, fmt.Errorf("job %q output is not a regular file", jobID)
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	buf := make([]byte, jobOutputChunk)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return "", 0, false, err
	}
	next = offset + int64(n)
	done = proto.JobDone(job.Status) && next >= info.Size()
	return string(buf[:n]), next, done, nil
}

// interruptedJobs finishes every job still live in a state whose process is
// gone. Nobody chose this, so the status is interrupted rather than stopped.
func interruptedJobs(state *projection.State) []proto.Emission {
	var out []proto.Emission
	for _, j := range state.Jobs {
		if proto.JobDone(j.Status) {
			continue
		}
		out = append(out, proto.Emit(proto.JobFinished, proto.JobPayload{
			JobID: j.ID, ToolCallID: j.ToolCallID, ParentJobID: j.ParentJobID, Status: proto.JobInterrupted,
		}))
	}
	return out
}

// ResolvePermission answers a pending request from any presenter.
func (a *Actor) ResolvePermission(ctx context.Context, requestID string, outcome adapter.PermissionOutcome) error {
	_, err := a.call(ctx, command{kind: cmdResolvePerm, reqID: requestID, outcome: outcome})
	return err
}

func (a *Actor) ResolveElicitation(ctx context.Context, requestID string, result adapter.ElicitationResult) error {
	_, err := a.call(ctx, command{kind: cmdResolveElicit, reqID: requestID, elicitResult: result})
	return err
}

// Close ends the thread for good: thread.closed is appended and the thread
// can no longer be resumed.
func (a *Actor) Close(reason string) {
	select {
	case a.inbox <- command{kind: cmdClose, prompt: reason, hard: true}:
	case <-a.quit:
	}
	a.wg.Wait()
}

// Dispose tears down the harness process without ending the thread. The log
// is untouched and the next attach resumes it, so restarting the server does
// not throw away conversations.
func (a *Actor) Dispose(reason string) {
	select {
	case a.inbox <- command{kind: cmdClose, prompt: reason, hard: false}:
	case <-a.quit:
	}
	a.wg.Wait()
}

// enqueueEmission appends an event produced outside the harness stream.
func (a *Actor) enqueueEmission(em proto.Emission) {
	select {
	case a.inbox <- command{kind: "emit", emission: &em}:
	case <-a.quit:
	}
}

func (a *Actor) pump(sess adapter.Session) {
	go func() {
		for em := range sess.Events() {
			select {
			case a.inbox <- command{kind: cmdHarnessEvent, emission: &em, from: sess}:
			case <-a.quit:
				return
			}
		}
		select {
		case a.inbox <- command{kind: cmdHarnessExit, from: sess}:
		case <-a.quit:
		}
	}()
}

// SwitchAccount moves the thread to another account of the same harness. The
// running harness process is stopped, the conversation moved, and the next
// command that needs a harness resumes it under the new account. Refused while
// anything is in flight: a turn, a job, or a question waiting on a human all
// belong to the process being replaced.
func (a *Actor) SwitchAccount(ctx context.Context, sw accountSwitch) error {
	_, err := a.call(ctx, command{kind: cmdSwitchAccount, account: &sw})
	return err
}

// ---- the actor loop ----

func (a *Actor) run() {
	defer a.wg.Done()
	// Seed the attention cache from the state the actor starts with — a
	// resumed projection, or an empty one. Every constructor builds the state
	// before starting this goroutine, so this is the first read.
	a.mu.Lock()
	a.attention = a.state.Attention()
	a.mu.Unlock()
	for {
		var flush <-chan time.Time
		if a.deltaFlush != nil {
			flush = a.deltaFlush.C
		}
		select {
		case c := <-a.inbox:
			if a.handle(c) {
				return
			}
			// Every command that can end a turn — a harness event, a cancel,
			// a resume closing an interrupted turn — comes through here, so
			// this is the one place a Stop learns its turn is over and a
			// queued prompt needs to be picked up.
			a.settleStop()
			a.dispatchQueued(context.Background())
		case <-flush:
			a.flushDeltas()
		}
	}
}

func (a *Actor) handle(c command) (stop bool) {
	ctx := context.Background()

	// Whatever a command reads or decides, it does so on a projection with
	// every held delta in it. Only another harness event may leave them held.
	if c.kind != cmdHarnessEvent {
		a.flushDeltas()
	}

	// Continuing is a prompt the server writes, so it is turned into one here
	// where the projection can say which turn is being continued.
	if c.kind == cmdContinue {
		last := a.lastTurn()
		if last == nil || !last.Done || last.StopReason != proto.StopError {
			c.reply <- cmdResult{err: ErrNothingToContinue}
			return false
		}
		attempt := 1
		if last.Recovery != nil {
			attempt = last.Recovery.Attempt + 1
		}
		// A restart is the only thing that entitles anyone to say one
		// happened. This is a human continuing a turn that ended in an error —
		// which is true whether or not a restart caused the original failure —
		// so it carries its own prompt and its own cause.
		c.kind = cmdPrompt
		c.prompt = continuePrompt
		c.recovery = &proto.TurnRecovery{
			ResumeOf: last.ID, Attempt: attempt, Cause: proto.RecoveryContinue,
		}
	}

	switch c.kind {
	case "schedule_prompt", "cancel_schedule", "send_schedule", "schedule_tick":
		c.reply <- cmdResult{err: a.handleSchedule(c)}
	case "state":
		c.reply <- cmdResult{value: a.state.Clone()}
	case cmdRead:
		c.reply <- cmdResult{value: c.read(a.state)}

	case cmdShowArtefact:
		v, err := a.handleShow(c.show)
		c.reply <- cmdResult{value: v, err: err}
	case cmdArtefact:
		v, ok := a.state.ArtefactByID(c.reqID)
		if !ok {
			c.reply <- cmdResult{err: ErrNoArtefact}
			break
		}
		c.reply <- cmdResult{value: v}

	case "emit":
		a.append(*c.emission)
		if c.reply != nil {
			c.reply <- cmdResult{}
		}

	case cmdHarnessEvent:
		if c.from != a.sess {
			return false
		}
		a.appendStreamed(*c.emission)

	case cmdHarnessReply:
		c.onReply()

	case cmdStopExpired:
		// The turn Stop was asked to end is still running: the harness did
		// not, or could not, do as it was told.
		if a.stopping == c.stop && a.turnActive == c.stop.turnID {
			a.forceStop()
		}

	case cmdHarnessExit:
		if c.from != a.sess {
			// The process an account switch replaced, or one Stop killed. Its
			// going is expected and says nothing about this thread.
			return false
		}
		// A harness that dies without closing its turn leaves the log saying
		// work is still in flight. Everything downstream then reads that as a
		// server restart — the only other way a turn stays open — and offers
		// to resume work that was never interrupted by a restart at all. The
		// adapter closes the turn itself whenever it can name a reason; this
		// is the backstop for the race where the process died between the turn
		// opening and the adapter learning its id.
		if a.turnActive != "" {
			a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: a.turnActive, StopReason: proto.StopError,
				Error: "the harness process ended before the turn finished",
			}))
		}
		// Any job the harness was running died with it.
		for _, em := range interruptedJobs(a.state) {
			a.append(em)
		}
		// So did anything it was holding: the queue in the log is what the
		// next process gets.
		a.sent = nil
		a.shutdown(false)
		return true

	case cmdActivate:
		if a.state.Closed {
			c.reply <- cmdResult{}
			return false
		}
		if a.sess != nil {
			c.reply <- cmdResult{}
			return false
		}
		// A pending actor can be restored leniently — for cleanup — with no
		// adapter behind it. Cleanup never activates; anything that does gets
		// a legible refusal instead of a nil dereference.
		if a.adapter == nil {
			if a.activationError != nil {
				c.reply <- cmdResult{err: a.activationError}
				return false
			}
			c.reply <- cmdResult{err: errors.New("this thread's provider instance is no longer configured")}
			return false
		}
		cwd, model, mode, effort := c.prompt, c.model, c.mode, c.effort
		if c.resume {
			cwd, model, mode, effort = a.Cwd, a.state.Model, a.state.Mode, a.state.Effort
		}
		// A resumed thread's worktree can be deleted or moved while the
		// server is down. A harness started there fails later with an error
		// that never names the directory, so refuse here, where we can.
		if cwd != "" {
			info, statErr := os.Stat(cwd)
			var unusable error
			switch {
			case errors.Is(statErr, fs.ErrNotExist):
				unusable = fmt.Errorf("this thread's workspace %s no longer exists, so it was probably deleted or moved; restore the directory to continue, or start a new thread", cwd)
			case statErr != nil:
				unusable = fmt.Errorf("this thread's workspace %s cannot be opened: %w", cwd, statErr)
			case !info.IsDir():
				unusable = fmt.Errorf("this thread's workspace %s is no longer a directory; restore it to continue, or start a new thread", cwd)
			}
			if unusable != nil {
				c.reply <- cmdResult{err: unusable}
				return false
			}
		}
		meta, err := a.store.Thread(ctx, a.ID)
		if err != nil {
			c.reply <- cmdResult{err: err}
			return false
		}
		x := harnessExtras(ctx, a.store, a.adapter, meta, cwd, a.logf)
		sess, err := a.adapter.CreateSession(ctx, a.newHost(), x.options(adapter.CreateOptions{
			ThreadID: a.ID, Cwd: cwd, Model: model, Mode: mode, Effort: effort, Env: a.env,
			Resume: c.resume, HarnessSessionID: a.state.HarnessSessionID,
		}))
		if err != nil {
			c.reply <- cmdResult{err: err}
			return false
		}
		a.Cwd, a.sess, a.preparing = cwd, sess, false
		a.pump(sess)
		a.startCheckpoints()
		if c.resume {
			// Server death ends anything that was waiting in the old process.
			// Do this only when the harness is actually resumed: viewing the
			// durable transcript is read-only and must not rewrite its history.
			a.cancelProcessAsks()
			for _, turn := range append([]projection.Turn(nil), a.state.Turns...) {
				if !turn.Done {
					a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turn.ID, StopReason: proto.StopError, Error: "server restarted during turn", Failure: proto.FailureRestart}))
				}
			}
			for _, em := range interruptedJobs(a.state) {
				a.append(em)
			}
		}
		c.reply <- cmdResult{}

	case cmdStopJob:
		if a.state.Closed {
			c.reply <- cmdResult{err: ErrClosed}
			return false
		}
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		stopper, ok := a.sess.(adapter.JobStopper)
		if !ok {
			c.reply <- cmdResult{err: errors.New("this harness cannot stop a job")}
			return false
		}
		a.inOrder(30*time.Second, func(ctx context.Context) (any, error) {
			return nil, stopper.StopJob(ctx, c.reqID)
		}, func(_ any, err error) {
			c.reply <- cmdResult{err: err}
		})

	case cmdQuota:
		// An account-level read, not a turn: the harness is asked out-of-band
		// through its own control path, so it is safe to serve while a turn
		// runs. A harness that cannot answer says so legibly.
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		reader, ok := a.sess.(adapter.SessionQuota)
		if !ok {
			c.reply <- cmdResult{err: errors.New("this harness does not report usage limits")}
			return false
		}
		a.offLoop(quotaTimeout, func(ctx context.Context) (any, error) {
			return reader.Quota(ctx)
		}, func(snap any, err error) {
			c.reply <- cmdResult{value: snap, err: err}
		})

	case cmdMCP:
		// A read or a reconnect through the harness's own control path, like
		// the quota read; bounded so a wedged harness cannot strand the caller.
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		ctl, ok := a.sess.(adapter.MCPControl)
		if !ok {
			c.reply <- cmdResult{err: adapter.ErrMCPUnsupported}
			return false
		}
		a.offLoop(mcpTimeout, func(ctx context.Context) (any, error) {
			return c.mcp(ctx, ctl)
		}, func(v any, err error) {
			c.reply <- cmdResult{value: v, err: err}
		})

	case cmdSetMode:
		if a.state.Closed {
			c.reply <- cmdResult{err: ErrClosed}
			return false
		}
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		switcher, ok := a.sess.(adapter.ModeSwitcher)
		if !ok {
			c.reply <- cmdResult{err: errors.New("this harness cannot change permission mode mid-thread")}
			return false
		}
		// Durable and fanned out, so the change lands in the log and every
		// connected presenter follows — same requirement as permission.resolved.
		a.setting(func(ctx context.Context) error { return switcher.SetMode(ctx, c.mode) },
			proto.ThreadConfigChangedPayload{Mode: c.mode}, c.reply)

	case cmdSwitchAccount:
		c.reply <- cmdResult{err: a.switchAccount(c.account)}

	case cmdSetModel:
		if a.state.Closed {
			c.reply <- cmdResult{err: ErrClosed}
			return false
		}
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		switcher, ok := a.sess.(adapter.ModelSwitcher)
		if !ok {
			c.reply <- cmdResult{err: errors.New("this harness cannot change model mid-thread")}
			return false
		}
		a.setting(func(ctx context.Context) error { return switcher.SetModel(ctx, c.model) },
			proto.ThreadConfigChangedPayload{Model: c.model}, c.reply)

	case cmdSetEffort:
		if a.state.Closed {
			c.reply <- cmdResult{err: ErrClosed}
			return false
		}
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		switcher, ok := a.sess.(adapter.EffortSwitcher)
		if !ok {
			c.reply <- cmdResult{err: errors.New("this harness cannot change reasoning effort mid-thread")}
			return false
		}
		a.setting(func(ctx context.Context) error { return switcher.SetEffort(ctx, c.effort) },
			proto.ThreadConfigChangedPayload{Effort: &c.effort}, c.reply)

	case cmdListComposer:
		if a.state.Closed {
			c.reply <- cmdResult{value: []adapter.ComposerItem{}}
			return false
		}
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		catalogue, ok := a.sess.(adapter.ComposerCataloguer)
		if !ok {
			c.reply <- cmdResult{value: []adapter.ComposerItem{}}
			return false
		}
		a.offLoop(30*time.Second, func(ctx context.Context) (any, error) {
			return catalogue.ComposerItems(ctx)
		}, func(items any, err error) {
			c.reply <- cmdResult{value: items, err: err}
		})

	case cmdRunComposer:
		if a.state.Closed {
			c.reply <- cmdResult{err: ErrClosed}
			return false
		}
		if a.turnActive != "" {
			c.reply <- cmdResult{err: ErrBusy}
			return false
		}
		if a.sess == nil {
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		runner, ok := a.sess.(adapter.ComposerActionRunner)
		if !ok {
			c.reply <- cmdResult{err: errors.New("this harness cannot run composer actions")}
			return false
		}
		// The turn is reserved here, on the loop, so nothing else can start
		// one while the harness is being asked; the asking happens off it.
		turnID := uuid.NewString()
		a.turnActive = turnID
		a.append(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: turnID, Prompt: c.model}))
		in := adapter.ComposerActionInput{TurnID: turnID, Action: c.mode, Args: c.prompt}
		a.inOrder(60*time.Second, func(ctx context.Context) (any, error) {
			return runner.RunComposerAction(ctx, in)
		}, func(value any, err error) {
			// The adapter may already know what kind of failure this is —
			// a harness that needs a login says so when it refuses the
			// prompt — and dropping that leaves the turn looking generic
			// enough to be offered a continue button it cannot use. A turn
			// that already ended meanwhile, by Stop or by the harness, is
			// not closed a second time.
			if err != nil && a.turnActive == turnID {
				a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
					TurnID: turnID, StopReason: proto.StopError, Error: err.Error(),
					Failure: adapter.FailureOf(err),
				}))
			}
			c.reply <- cmdResult{value: value, err: err}
		})

	case cmdPrompt:
		if a.state.Closed {
			c.reply <- cmdResult{err: ErrClosed}
			return false
		}
		if c.plan != nil {
			a.mu.Lock()
			current := a.recovery
			a.mu.Unlock()
			if current != c.plan {
				// Taken by a second Recover racing this one, or superseded
				// by a human prompt: either way there is nothing left to
				// send.
				c.reply <- cmdResult{}
				return false
			}
			c.prompt, c.recovery = c.plan.prompt, &c.plan.rec
		}
		if a.turnActive != "" {
			if c.plan != nil {
				// The server's own continuation does not wait in line: if
				// work is already running there is nothing to continue.
				a.abandonRecovery()
				c.reply <- cmdResult{err: ErrBusy}
				return false
			}
			// Not refused: the prompt waits in the log. A harness that can
			// take a message mid-turn gets it now and reads it at its next
			// step, the way the terminal does; otherwise it starts its own
			// turn when this one ends. A steer that fails on the way out is
			// simply queued the old way.
			queueID := uuid.NewString()
			a.append(proto.Emit(proto.PromptQueued, proto.PromptQueuedPayload{QueueID: queueID, Prompt: c.prompt, Images: c.images}))
			// Nor while a Stop is open: it would reach the harness behind the
			// interrupt and start the turn over. It starts once the turn ends.
			st, ok := a.sess.(adapter.Steerer)
			if !ok || a.stopping != nil {
				c.reply <- cmdResult{value: PromptResult{QueueID: queueID}}
				return false
			}
			a.steer(st, queueID, adapter.PromptInput{QueueID: queueID, Text: c.prompt, Images: c.images}, c.reply)
			return false
		}
		// A prompt cannot overtake a control call still on its way, so while one
		// is it waits in the queue, which starts it once the call has answered.
		if (a.sess == nil && a.preparing || a.sess != nil && a.controlPending > 0) && c.recovery == nil {
			queueID := uuid.NewString()
			a.append(proto.Emit(proto.PromptQueued, proto.PromptQueuedPayload{QueueID: queueID, Prompt: c.prompt, Images: c.images}))
			c.reply <- cmdResult{value: PromptResult{QueueID: queueID}}
			return false
		}
		if a.sess == nil {
			// The plan is left in place: the harness is not up yet, and the
			// continuation is still owed once it is.
			c.reply <- cmdResult{err: ErrNotReady}
			return false
		}
		if c.plan != nil {
			// The continuation is being taken now; a retry, if it comes to
			// one, puts it back.
			a.setRecovery(nil)
		}
		turnID, err := a.startTurn(ctx, c.prompt, c.images, c.recovery, "")
		if err != nil {
			// A usage limit takes the continuation over: whatever turn goes
			// next past the held queue carries the note (followLimit).
			if c.plan != nil && !a.state.QueueHeld() {
				// Put back for a retry, or for its note to ride on the
				// next human prompt: either way the agent still has to hear
				// what died.
				prev := a.wantedPhase()
				a.setRecovery(c.plan)
				if !retryableRecovery(err) {
					a.abandonRecovery()
				}
				a.writePhase(prev)
			}
			c.reply <- cmdResult{err: err}
			return false
		}
		c.reply <- cmdResult{value: PromptResult{TurnID: turnID}}

	case cmdDropRecovery:
		a.abandonRecovery()
		c.reply <- cmdResult{}

	case cmdDequeue:
		if a.state.Closed {
			c.reply <- cmdResult{err: ErrClosed}
			return false
		}
		if !a.isQueued(c.reqID) {
			c.reply <- cmdResult{err: ErrNotQueued}
			return false
		}
		if a.sent[c.reqID] {
			c.reply <- cmdResult{err: ErrAlreadySent}
			return false
		}
		a.append(proto.Emit(proto.PromptDequeued, proto.PromptDequeuedPayload{QueueID: c.reqID, Reason: proto.DequeueRemoved}))
		c.reply <- cmdResult{}

	case cmdCancel:
		// Stop means stop: a prompt queued behind the interrupted turn would
		// otherwise start the moment the interrupt landed. The web puts the
		// text back in the composer before it asks. A continuation armed for
		// a usage limit is the same thing, only later; so is one for the
		// turn being stopped, which a late limit finish would otherwise arm.
		// With no turn running, the one Stop is aimed at is the limit just
		// reached: Codex may still back-fill its reset.
		a.disarmLimit()
		if a.turnActive != "" {
			a.stoppedTurn = a.turnActive
		} else if last := a.lastTurn(); last != nil && a.state.QueueHeld() {
			a.stoppedTurn = last.ID
		}
		if a.turnActive == "" {
			a.dropQueue()
			c.reply <- cmdResult{value: "idle"}
			return false
		}
		if a.sess == nil {
			// A read-only restore may expose the interrupted turn before the
			// recovery worker activates its process. Stop means do not recover it.
			a.abandonRecovery()
			a.dropQueue()
			a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: a.turnActive, StopReason: proto.StopCancelled,
			}))
			c.reply <- cmdResult{value: "idle"}
			return false
		}
		if a.stopping != nil && a.stopping.turnID == a.turnActive {
			// Asked twice: whoever is asking has seen the first Stop not
			// work, and waiting out the rest of the grace would be the
			// server knowing better.
			a.forceStop()
			c.reply <- cmdResult{value: "idle"}
			return false
		}
		a.requestStop()
		c.reply <- cmdResult{value: "cancelling"}

	case cmdAskPerm:
		// From a process Stop killed or an account switch replaced, asked on
		// its way out, or asked as its turn ended and overtaken by the
		// finish. Nobody is waiting on it, and recorded it would wait in the
		// log for ever.
		if c.process != a.process || a.turnOver(c.perm.req.TurnID) {
			c.perm.ch <- adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}
			return false
		}
		req := c.perm.req
		requestID := c.reqID
		a.pendingPerm[requestID] = c.perm.ch
		opts := req.Options
		if len(opts) == 0 {
			opts = proto.DefaultPermissionOptions()
		}
		a.append(proto.Emit(proto.PermissionRequested, proto.PermissionRequestedPayload{
			RequestID:  requestID,
			TurnID:     req.TurnID,
			ToolCallID: req.ToolCallID,
			ToolName:   req.ToolName,
			Title:      req.Title,
			RawInput:   req.RawInput,
			Options:    opts,
		}))

	case cmdResolvePerm:
		ch, ok := a.pendingPerm[c.reqID]
		if !ok {
			// First resolution already won. The loser gets an ack, not an error.
			c.reply <- cmdResult{value: "already_resolved"}
			return false
		}
		delete(a.pendingPerm, c.reqID)
		a.append(proto.Emit(proto.PermissionResolved, proto.PermissionResolvedPayload{
			RequestID: c.reqID, Outcome: c.outcome.Outcome, OptionID: c.outcome.OptionID,
		}))
		select {
		case ch <- c.outcome:
		default:
		}
		c.reply <- cmdResult{value: "resolved"}

	case cmdAskElicit:
		if c.process != a.process || a.turnOver(c.elicit.req.TurnID) {
			// As for a permission.
			c.elicit.ch <- adapter.ElicitationResult{Action: "cancel"}
			return false
		}
		requestID := c.reqID
		a.pendingElicit[requestID] = c.elicit.ch
		a.append(proto.Emit(proto.ElicitationRequested, proto.ElicitationRequestedPayload{
			RequestID: requestID, TurnID: c.elicit.req.TurnID,
			Prompt: c.elicit.req.Prompt, Schema: c.elicit.req.Schema,
		}))

	case cmdResolveElicit:
		ch, ok := a.pendingElicit[c.reqID]
		if !ok {
			c.reply <- cmdResult{value: "already_resolved"}
			return false
		}
		delete(a.pendingElicit, c.reqID)
		a.append(proto.Emit(proto.ElicitationResolved, proto.ElicitationResolvedPayload{
			RequestID: c.reqID, Action: c.elicitResult.Action, Value: c.elicitResult.Value,
		}))
		select {
		case ch <- c.elicitResult:
		default:
		}
		c.reply <- cmdResult{value: "resolved"}

	case cmdRaiseCard, cmdResolveCard:
		v, err := a.handleCard(c)
		c.reply <- cmdResult{value: v, err: err}

	case cmdClose:
		if c.hard {
			a.append(proto.Emit(proto.ThreadClosed, proto.ThreadClosedPayload{Reason: c.prompt}))
		}
		a.shutdown(c.hard)
		return true

	case cmdEvict:
		return a.evict(c.evict)
	}
	return false
}

func (a *Actor) switchAccount(sw *accountSwitch) error {
	switch {
	case a.state.Closed:
		return ErrClosed
	case a.turnActive != "" || a.state.Phase != "idle":
		return errors.New("wait for the turn to finish before switching account")
	case len(interruptedJobs(a.state)) > 0:
		return errors.New("background work is still running; wait for it or stop it before switching account")
	case len(a.state.Pending) > 0 || a.state.Questions() > 0:
		// A card is omniplex's to hold, not the harness's, so it does not
		// stand in the way.
		return errors.New("answer the waiting question before switching account")
	}
	// The harness writes its conversation as it goes, so it has to be gone
	// before its files are moved. Dropping the process first is what marks
	// its exit, still on its way through the inbox, as stale.
	if old := a.dropProcess(); old != nil {
		_ = old.Close()
	}
	id := a.state.HarnessSessionID
	if id == "" {
		// Named at start by omniplex and reported back only once the harness
		// runs; a thread that never ran has nothing to move.
		id = a.ID
	}
	if err := sw.move(id); err != nil {
		// Nothing changed but the process, which the next command restarts
		// under the account the thread still has.
		return err
	}
	a.adapter, a.env, a.activationError = sw.ad, sw.env, nil
	a.append(proto.Emit(proto.ThreadAccountChanged, sw.changed))
	return nil
}

func (a *Actor) shutdown(hard bool) {
	ctx := context.Background()
	phase := "idle"
	switch {
	case hard || a.state.Closed:
		phase = "closed"
	case a.turnActive != "":
		// Disposed mid-turn. The row keeps saying "turn" so the next start can
		// find this thread and finish what it was doing; recording idle here
		// would erase the only cheap evidence that work was in flight. A kill
		// -9 leaves the same value behind, so both deaths look alike.
		phase = "turn"
	case a.state.LiveJobs().Working():
		// Idle, but a subagent or a monitor the agent is waiting on dies
		// with this process. The next start has to tell the agent, or it
		// waits for a result that never comes.
		phase = "background"
	default:
		// A continuation that was never sent dies with this process too.
		if owed := a.owedPhase(); owed != "" {
			phase = owed
		}
	}
	_ = a.store.SetPhase(ctx, a.ID, phase)
	a.stop(phase == "closed")
}

// stop ends the actor without touching the stored phase: it releases whatever
// is waiting on it, closes the harness, and ends every subscription. drop
// discards the checkout snapshots too, for a thread that is done with them.
func (a *Actor) stop(drop bool) {
	a.releaseAsks()
	a.clearStopping()

	if a.sess != nil {
		_ = a.sess.Close()
	}
	// A thread that is merely disposed will be resumed from the log, and its
	// snapshots are the baseline the next turn needs. Only a thread closed for
	// good is done with them.
	a.checkpoints.stop()
	if drop {
		a.checkpoints.drop()
	}
	close(a.quit)

	a.mu.Lock()
	subs := a.subs
	a.subs = map[string]*Subscriber{}
	a.mu.Unlock()
	for _, s := range subs {
		close(s.Ch)
	}

	a.mu.Lock()
	onExit := a.onExit
	a.mu.Unlock()
	if onExit != nil {
		onExit()
	}
}

// checkpointBaselineWait bounds how long a prompt waits for the snapshot that
// its turn will be measured against.
const checkpointBaselineWait = 15 * time.Second

// startCheckpoints begins snapshotting the checkout, so each turn can say which
// files it changed. Every turn takes its own baseline when it starts; there is
// nothing to do here but be ready.
func (a *Actor) startCheckpoints() {
	if a.checkpoints != nil || a.Cwd == "" {
		return
	}
	a.checkpoints = newCheckpointer(a.Cwd, a.ID, a.enqueueEmission, a.logf)
}

// startTurn opens a turn for a prompt and sends it to the harness. The caller
// has already checked that the thread is open, activated, and idle.
func (a *Actor) startTurn(ctx context.Context, prompt string, images []proto.PromptImage, recovery *proto.TurnRecovery, queueID string) (string, error) {
	turnID := uuid.NewString()
	a.turnActive = turnID

	// The baseline this turn is measured against has to be a picture of the
	// checkout from before the harness could touch it, so it is taken — and
	// waited for — before the prompt goes out. The wait is bounded: a slow
	// checkout should cost this turn its card, not the turn itself.
	baseCtx, cancelBase := context.WithTimeout(ctx, checkpointBaselineWait)
	if a.checkpoints.baseline(baseCtx, turnID) {
		a.measuring = turnID
	}
	cancelBase()

	// append records the phase change; no SetPhase needed here.
	if err := a.append(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: turnID, Prompt: prompt, Images: images, Recovery: recovery, QueueID: queueID})); err != nil {
		a.turnActive = ""
		return "", err
	}
	// A recovery prompt is the server talking to itself; naming a thread
	// after it would bury what the human actually asked for.
	if recovery == nil {
		title := truncate(prompt, 60)
		if title == "" && len(images) > 0 {
			// An attachment-only prompt still deserves a name in the sidebar.
			title = proto.AttachmentTitle(images)
		}
		_ = a.store.SetTitle(ctx, a.ID, title)
	}

	// A turn a human asked for — a prompt, or a continue — is the agent's
	// next chance to hear about work a restart killed. One that gets in ahead
	// of the continuation supersedes it — the human has picked the thread up
	// — and carries its news instead.
	text, note := prompt, ""
	if recovery == nil || recovery.Cause == proto.RecoveryContinue {
		a.abandonRecovery()
		note, a.heldNote = a.heldNote, ""
		text = withNote(note, prompt)
	}

	// This one call to the harness is made on the loop, so a harness that
	// never takes the prompt must not hold the thread for ever. Every adapter
	// returns once the prompt is written or the turn acknowledged.
	promptCtx, cancelPrompt := context.WithTimeout(ctx, 30*time.Second)
	err := a.sess.Prompt(promptCtx, adapter.PromptInput{TurnID: turnID, Text: text, Images: images})
	cancelPrompt()
	if err != nil {
		// A refused prompt delivered nothing, so its note waits for the next.
		if note != "" {
			a.heldNote = withNote(note, a.heldNote)
		}
		// turnActive is left for append to clear: a prompt that failed on
		// the way out may still have reached the harness, and the closing
		// snapshot is the only way to find out what it did.
		// The adapter may already know what kind of failure this is —
		// a harness that needs a login says so when it refuses the
		// prompt — and dropping that leaves the turn looking generic
		// enough to be offered a continue button it cannot use.
		a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: turnID, StopReason: proto.StopError, Error: err.Error(),
			Failure: adapter.FailureOf(err),
		}))
		return "", err
	}
	return turnID, nil
}

func (a *Actor) isQueued(queueID string) bool {
	for _, q := range a.state.Queued {
		if q.QueueID == queueID {
			return true
		}
	}
	return false
}

// dispatchQueued starts the oldest queued prompt if the thread is idle and
// able to run one. A prompt that fails on the way out ends as an errored turn
// like any other; the rest of the queue stays, each to be tried in its turn.
func (a *Actor) dispatchQueued(ctx context.Context) {
	if len(a.state.Queued) == 0 || a.turnActive != "" || a.sess == nil || a.state.Closed || a.controlPending > 0 {
		return
	}
	// Stop means stop. The stopped turn can end before the harness answers
	// the interrupt; settleStop drops what was queued behind it the moment
	// it does, and nothing queued may start while a Stop is still open.
	if a.stopping != nil {
		return
	}
	// A restart interrupted a turn and the continuation has not been sent
	// yet. It goes first: the queue was written behind that work, not
	// instead of it.
	a.mu.Lock()
	recovering := a.recovery != nil
	a.mu.Unlock()
	if recovering {
		return
	}
	// A prompt the harness already holds is its to run: it starts a turn
	// with it itself, or reads it into one. Only what nobody has is started
	// here. Nor what a usage limit holds: that waits for the next turn to
	// start rather than walking into the limit one prompt at a time.
	for _, next := range a.state.Queued {
		if a.sent[next.QueueID] || a.state.HeldByLimit(next) {
			continue
		}
		if _, err := a.startTurn(ctx, next.Prompt, a.resolveImages(next.Images), nil, next.QueueID); err != nil {
			a.logf("queued prompt on %s: %v", a.ID, err)
		}
		return
	}
}

// setRecovery replaces the pending continuation. Recover reads it from
// outside the loop, so it is written under mu.
func (a *Actor) setRecovery(plan *recoveryPlan) {
	a.mu.Lock()
	a.recovery = plan
	a.mu.Unlock()
}

// abandonRecovery gives up the pending continuation. Its note is held for the
// next human prompt: whether or not the thread wakes, the agent should not be
// left waiting on work that died.
func (a *Actor) abandonRecovery() {
	prev := a.wantedPhase()
	a.mu.Lock()
	if a.recovery != nil {
		a.heldNote = withNote(a.heldNote, a.recovery.note)
		a.recovery = nil
	}
	a.mu.Unlock()
	a.writePhase(prev)
}

// holdRowOnlyIfAsked keeps the pending continuation only when the thread's
// row is what asked for it: nothing else sends it, and a plan left pending
// holds the queue back. A thread nobody is recovering — its last chain given
// up on, a harness that exited, a row from before rows said "background" —
// keeps only the note, for its next prompt.
func (a *Actor) holdRowOnlyIfAsked(rowPhase string) {
	if a.recovery != nil && !interruptedPhase(rowPhase) {
		a.heldNote = withNote(a.heldNote, a.recovery.note)
		a.recovery = nil
	}
}

// owedPhase is the phase a pending continuation holds the row at, or empty.
func (a *Actor) owedPhase() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.recovery == nil {
		return ""
	}
	return a.recovery.phase
}

// wantedPhase is what the thread's row should say: the projection's phase,
// except that an idle thread still owed a continuation keeps the phase that
// will find it again after another restart.
func (a *Actor) wantedPhase() string {
	phase := storedPhase(a.state)
	if owed := a.owedPhase(); phase == "idle" && owed != "" {
		return owed
	}
	return phase
}

// writePhase syncs the row's phase column after a change that may have moved
// it from prev.
func (a *Actor) writePhase(prev string) {
	if phase := a.wantedPhase(); phase != prev && phase != "" {
		if err := a.store.SetPhase(context.Background(), a.ID, phase); err != nil {
			a.logf("set phase %s on %s: %v", phase, a.ID, err)
		}
	}
}

func (a *Actor) markSent(queueID string) {
	if a.sent == nil {
		a.sent = map[string]bool{}
	}
	a.sent[queueID] = true
}

// resolveImages puts the host path back on images that came out of the log,
// which never carries one. Without a resolver — or when it fails — the image
// goes without a path, and the adapter says so.
func (a *Actor) resolveImages(images []proto.PromptImage) []proto.PromptImage {
	if len(images) == 0 {
		return nil
	}
	a.mu.Lock()
	resolve := a.imagePath
	a.mu.Unlock()
	out := make([]proto.PromptImage, len(images))
	for i, img := range images {
		out[i] = img
		if img.Path == "" && resolve != nil {
			path, err := resolve(a.ID, img.ID)
			if err != nil {
				a.logf("queued image %s on %s: %v", img.ID, a.ID, err)
				continue
			}
			out[i].Path = path
		}
	}
	return out
}

// dropQueue takes every waiting prompt out of the queue. Presenters that
// want the text back read it from the state before asking for whatever
// caused this.
func (a *Actor) dropQueue() {
	for _, q := range append([]projection.QueuedPrompt(nil), a.state.Queued...) {
		a.append(proto.Emit(proto.PromptDequeued, proto.PromptDequeuedPayload{QueueID: q.QueueID, Reason: proto.DequeueCancelled}))
	}
	// Cancel discards what the harness was holding too (adapter.Steerer).
	a.sent = nil
}

// appendStreamed appends an event from the harness, holding it for the
// delta window when it is a delta that can be merged with the ones after it.
func (a *Actor) appendStreamed(em proto.Emission) {
	known := func(id string) bool {
		_, ok := a.state.ItemByID(id)
		return ok
	}
	flushed, held := a.deltas.add(em, known)
	if len(flushed) > 0 {
		a.stopDeltaFlush()
	}
	for _, f := range flushed {
		a.write(f)
	}
	if !held {
		a.write(em)
		return
	}
	if a.deltaFlush == nil {
		a.deltaFlush = time.NewTimer(deltaWindow)
	}
}

// trimOutput cuts a tool call's output delta to what its tail can still
// show. The projection keeps only that much, and the call's result carries
// the whole output once it finishes; the rest of a noisy build would only
// make the log and every client's connection pay for it twice. On an update
// that finishes the call — output the coalescer joined to its result — the
// tail is dropped anyway, so none of it is kept.
func trimOutput(em proto.Emission) proto.Emission {
	if p, ok := em.Payload.(proto.ToolCallUpdatedPayload); ok && p.OutputDelta != "" {
		if proto.ToolDone(p.Status) {
			p.OutputDelta = ""
		} else {
			p.OutputDelta = projection.TrimOutputDelta(p.OutputDelta)
		}
		em.Payload = p
	}
	return em
}

func (a *Actor) stopDeltaFlush() {
	if a.deltaFlush != nil {
		a.deltaFlush.Stop()
		a.deltaFlush = nil
	}
}

// flushDeltas writes the held delta, if any, and closes the window.
func (a *Actor) flushDeltas() {
	a.stopDeltaFlush()
	for _, em := range a.deltas.take() {
		a.write(em)
	}
}

// append writes the event after any deltas still held, so the log keeps the
// order the events happened in.
func (a *Actor) append(em proto.Emission) error {
	a.flushDeltas()
	return a.write(em)
}

// write stores the event, folds it into the projection, and fans it out.
// This is the only place any of those three happen.
func (a *Actor) write(em proto.Emission) error {
	ctx := context.Background()
	em = a.redact(em)
	em = trimOutput(em)

	ev, err := a.store.Append(ctx, a.ID, em)
	if err != nil {
		a.logf("append %s on %s: %v", em.Type, a.ID, err)
		return err
	}

	prevPhase := a.wantedPhase()
	a.state.Apply(ev)

	a.mu.Lock()
	a.head = ev.Seq
	a.mu.Unlock()

	if em.Type == proto.TurnStarted {
		// A turn the actor did not start itself is the harness resuming work
		// on its own — a background task completing, an auto-continuation.
		// Track it like any other turn so prompts are refused while it runs
		// and cancel has something to interrupt.
		if p, ok := em.Payload.(proto.TurnStartedPayload); ok && a.turnActive == "" {
			a.turnActive = p.TurnID
		}
		if p, ok := em.Payload.(proto.TurnStartedPayload); ok && p.QueueID != "" {
			delete(a.sent, p.QueueID)
		}
	}
	// The harness has read a prompt it was handed; it is no longer in
	// flight.
	if p, ok := em.Payload.(proto.PromptInjectedPayload); ok {
		delete(a.sent, p.QueueID)
	}
	if p, ok := em.Payload.(proto.PromptDequeuedPayload); ok {
		delete(a.sent, p.QueueID)
	}
	if em.Type == proto.TurnFinished {
		// Only the finish of the turn that is actually active may clear it. A
		// finish for some other turn — a stale close from the adapter, or the
		// resolution of an interleaving the actor has already moved past —
		// must not release the busy guard while different work is running.
		// The projection applies the same guard when folding the event, so
		// the phase it lands on and the busy guard agree.
		p, ok := em.Payload.(proto.TurnFinishedPayload)
		if a.turnActive == "" || (ok && p.TurnID == a.turnActive) {
			// Only a turn whose baseline was taken can be measured. A
			// turn.finished closing out a turn a restart interrupted, or one
			// whose baseline never settled, has nothing honest to be compared
			// against.
			if a.measuring != "" && a.measuring == a.turnActive {
				a.checkpoints.turnEnded(a.turnActive)
			}
			a.measuring = ""
			a.turnActive = ""
		}
		a.releaseSettled()
	}

	// The stored phase column is a cache of the projection, kept for the
	// thread list and for restart recovery, which scans rows without folding
	// logs. Syncing it on every turn-shaped transition — not just on turn
	// events — is what lets activity-promoted turns (streaming or a tool
	// going active while the log said idle) survive a restart, and syncing
	// it on job events is what lets a kill -9 leave a waiting subagent as
	// much evidence as a clean shutdown does. Workspace events are excluded:
	// the lifecycle runner owns those writes, and its vocabulary (ready,
	// provisioning, …) is wider than the projection's.
	switch em.Type {
	case proto.TurnStarted, proto.TurnFinished, proto.MessageChunk, proto.ToolCallStarted, proto.ToolCallUpdated, proto.ThreadClosed,
		proto.JobStarted, proto.JobUpdated, proto.JobFinished:
		a.writePhase(prevPhase)
	}

	// Attention is the derived whose-turn-is-it signal. Any event that moves
	// it — turn boundaries, a permission being asked or answered, activity
	// while idle — re-notifies the thread list.
	if att := a.state.Attention(); att != a.Attention() {
		a.mu.Lock()
		a.attention = att
		onPhase := a.onPhase
		a.mu.Unlock()
		if onPhase != nil {
			onPhase()
		}
	}

	if em.Type == proto.PromptScheduled {
		a.mu.Lock()
		onPhase := a.onPhase
		a.mu.Unlock()
		if onPhase != nil {
			onPhase()
		}
	}
	a.fanout(ev)

	if ev.Seq-a.lastSnapAt >= SnapshotEvery || em.Type == proto.TurnFinished {
		a.lastSnapAt = ev.Seq
		if err := a.store.PutSnapshot(ctx, a.ID, ev.Seq, a.state); err != nil {
			a.logf("snapshot %s: %v", a.ID, err)
		}
	}
	// Last, so whatever it appends follows this event everywhere it went.
	a.followLimit(ev)
	return nil
}

// storedPhase is what the thread row records for a projection: idle, turn or
// closed as the projection has it, and background for an idle thread whose
// subagents or monitors are still running — work the agent is waiting on, so
// a restart has to wake it. Empty for a workspace phase, which is the
// lifecycle runner's to record.
func storedPhase(s *projection.State) string {
	switch {
	case s.Closed || s.Phase == "closed":
		return "closed"
	case s.Phase == "turn":
		return "turn"
	case s.Phase == "idle" && s.LiveJobs().Working():
		return "background"
	case s.Phase == "idle":
		return "idle"
	}
	return ""
}

// fanout delivers to every subscriber without blocking. On a full queue the
// subscriber is dropped and told to resync rather than growing server memory
// or stalling the turn.
func (a *Actor) fanout(ev proto.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for id, sub := range a.subs {
		if sub.dropped {
			continue
		}
		select {
		case sub.Ch <- ev:
		default:
			sub.dropped = true
			close(sub.Resync)
			delete(a.subs, id)
		}
	}
}

// turnOver reports whether the log has already finished turn. An ask that
// names no turn is never over.
func (a *Actor) turnOver(turn string) bool {
	if turn == "" {
		return false
	}
	for _, t := range a.state.Turns {
		if t.ID == turn {
			return t.Done
		}
	}
	return false
}

// releaseSettled unblocks the asker of every request the projection no longer
// holds open. A finished turn takes its requests with it, whether or not the
// harness said it withdrew them, and the adapter goroutine waiting on one
// must not wait forever.
func (a *Actor) releaseSettled() {
	open := map[string]bool{}
	for _, p := range a.state.Pending {
		open[p.RequestID] = true
	}
	for _, p := range a.state.Elicitations {
		open[p.RequestID] = true
	}
	a.releaseAsksExcept(open)
}

// ---- host services exposed to adapters ----

// hostServices is what one harness process is given to reach the actor.
// process is the generation it was started under (Actor.process).
type hostServices struct {
	a       *Actor
	process uint64
}

// RequestPermission appends a durable permission.requested event and blocks
// until any presenter resolves it. Nothing about this lives in a connection.
// An adapter cancels ctx when the harness withdraws the request; the request
// is then resolved as cancelled, so no presenter is left holding a card the
// harness has stopped waiting on.
func (h hostServices) RequestPermission(ctx context.Context, req adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	ch := make(chan adapter.PermissionOutcome, 1)
	requestID := uuid.NewString()

	select {
	case h.a.inbox <- command{kind: cmdAskPerm, process: h.process, reqID: requestID, perm: permAsk{req: req, ch: ch}}:
	case <-h.a.quit:
		return adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}, nil
	case <-ctx.Done():
		return adapter.PermissionOutcome{}, ctx.Err()
	}

	select {
	case out := <-ch:
		return out, nil
	case <-h.a.quit:
		return adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}, nil
	case <-ctx.Done():
		cancelled := adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}
		h.withdraw(command{kind: cmdResolvePerm, reqID: requestID, outcome: cancelled})
		return cancelled, ctx.Err()
	}
}

// Elicit is RequestPermission's twin for a question, withdrawn the same way.
func (h hostServices) Elicit(ctx context.Context, req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	ch := make(chan adapter.ElicitationResult, 1)
	requestID := uuid.NewString()
	select {
	case h.a.inbox <- command{kind: cmdAskElicit, process: h.process, reqID: requestID, elicit: elicitAsk{req: req, ch: ch}}:
	case <-h.a.quit:
		return adapter.ElicitationResult{Action: "cancel"}, nil
	case <-ctx.Done():
		return adapter.ElicitationResult{}, ctx.Err()
	}
	select {
	case result := <-ch:
		return result, nil
	case <-h.a.quit:
		return adapter.ElicitationResult{Action: "cancel"}, nil
	case <-ctx.Done():
		cancelled := adapter.ElicitationResult{Action: "cancel"}
		h.withdraw(command{kind: cmdResolveElicit, reqID: requestID, elicitResult: cancelled})
		return cancelled, ctx.Err()
	}
}

// withdraw resolves a request whose asker stopped waiting. It does not wait
// for the actor: a human answer that got there first wins, and the asker has
// nothing left to do with either.
func (h hostServices) withdraw(c command) {
	c.reply = make(chan cmdResult, 1)
	select {
	case h.a.inbox <- c:
	case <-h.a.quit:
	}
}

func (h hostServices) Logf(format string, args ...any) { h.a.logf(format, args...) }

// ReportQuota forwards a live usage-limit push from the harness to the
// manager's per-instance cache. The actor is a conduit only: the instance
// identity was bound at adopt time, and the adapter that pushed cannot even
// see it.
func (h hostServices) ReportQuota(snap adapter.QuotaSnapshot) {
	h.a.mu.Lock()
	sink := h.a.quotaSink
	h.a.mu.Unlock()
	if sink != nil {
		sink(snap)
	}
}

func (h hostServices) ComposerCatalogueChanged() {
	h.a.mu.Lock()
	defer h.a.mu.Unlock()
	for _, sub := range h.a.subs {
		select {
		case sub.ComposerChanged <- struct{}{}:
		default: // one invalidation already means "replace the whole catalogue"
		}
	}
}

// ---- attach ----

// Attach result kinds.
const (
	AttachSnapshot = "snapshot"
	AttachReplay   = "replay"
)

// AttachResult is what a presenter needs to reconstruct state exactly. Events
// are the wire copies, capped like live delivery; Snapshot is windowed.
type AttachResult struct {
	Kind     string
	Snapshot *projection.State
	Events   []proto.Event
	Seq      int64
}

// Attach computes the catch-up payload for a cursor: the events since it, or
// a snapshot of the newest snapshotItems top-level items when there is no
// cursor or the catch-up would not fit the replay budget. The caller must
// already have subscribed.
func (a *Actor) Attach(ctx context.Context, afterSeq int64, hasCursor bool, snapshotItems int) (AttachResult, error) {
	if hasCursor && replayable(a.Head()-afterSeq) {
		evs, err := a.store.ReadEvents(ctx, a.ID, afterSeq, MaxReplayEvents)
		if err != nil {
			return AttachResult{}, err
		}
		if wire, ok := wireReplay(evs); ok {
			seq := afterSeq
			if len(evs) > 0 {
				seq = evs[len(evs)-1].Seq
			}
			return AttachResult{Kind: AttachReplay, Events: wire, Seq: seq}, nil
		}
	}
	state, err := a.WireState(ctx, snapshotItems)
	if err != nil {
		return AttachResult{}, err
	}
	return AttachResult{Kind: AttachSnapshot, Snapshot: state, Seq: state.Seq}, nil
}

// replayable reports whether a cursor gap events behind head is worth reading
// at all. A negative gap is a cursor from some other history.
func replayable(gap int64) bool {
	return gap >= 0 && gap <= MaxReplayEvents
}

// wireReplay is evs as a client should see them, or ok=false when they are
// more than a replay should carry — past MaxReplayEvents, or past
// MaxReplayBytes once capped and encoded — and one snapshot is the cheaper
// answer. T3 Code's budget, for the same reason: a client back from a long
// absence on a phone should not download every chunk it missed.
func wireReplay(evs []proto.Event) (wire []proto.Event, ok bool) {
	if len(evs) > MaxReplayEvents {
		return nil, false
	}
	wire = make([]proto.Event, 0, len(evs))
	size := 0
	for _, ev := range evs {
		w := projection.WireEvent(ev)
		// The event as it is encoded in the frame, envelope and all: the
		// budget is what goes over the wire, not the payloads alone.
		b, err := json.Marshal(w)
		if err != nil {
			return nil, false
		}
		size += len(b)
		if size > MaxReplayBytes {
			return nil, false
		}
		wire = append(wire, w)
	}
	return wire, true
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
