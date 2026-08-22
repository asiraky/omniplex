// Package session owns the running sessions: one goroutine per session, which
// is the only thing that mutates that session's state. Fanout to presenters is
// non-blocking; a slow consumer is dropped and resynced, never buffered without
// limit and never allowed to stall a turn.
package session

import (
	"context"
	"errors"
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
	MaxReplayGap    = 1000 // no attach reads more events than this
	SubscriberQueue = 256  // bounded per-connection outbound queue
	SnapshotEvery   = 200  // snapshot cadence, a latency cache only
)

// Subscriber receives live events for one session. Ch is closed when the
// subscription ends. Resync is closed if the server dropped the queue.
type Subscriber struct {
	ID              string
	Ch              chan proto.Event
	Resync          chan struct{}
	ComposerChanged chan struct{}

	dropped bool
}

// Actor is one live session.
type Actor struct {
	ID      string
	Harness string
	Cwd     string

	store   *store.Store
	adapter adapter.Adapter
	// env is the provider instance's credential overlay, applied whenever this
	// actor spawns its harness — at start, at activation, and on resume. It is
	// fixed for the actor's lifetime, like the instance it came from.
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

	mu   sync.Mutex // guards subs and headSeq for readers outside the loop
	subs map[string]*Subscriber
	head int64
	// attention caches state.Attention() for readers outside the loop — the
	// session list wants it without a round trip through the inbox. Written
	// only by the actor goroutine; the projection stays the source of truth.
	attention string

	// onExit lets the manager forget a disposed actor, so the next attach
	// resumes the session from the log instead of finding a dead one.
	onExit func()
	// onPhase fires when the session moves between idle and turn, so a
	// session list rendered elsewhere can follow along.
	onPhase func()

	// recovery is set by Resume when the log shows a turn the server died in
	// the middle of. Recover consumes it; see recovery.go.
	recovery *proto.TurnRecovery

	// checkpoints snapshots the checkout around each turn, so a finished turn
	// can say which files it changed. Nil when the session has no Git checkout
	// to snapshot.
	checkpoints *checkpointer
	// measuring is the turn whose baseline was taken before the harness was
	// told anything. Only that turn can be measured: any other would be
	// compared against a picture of the checkout from the wrong moment.
	measuring string

	logf func(string, ...any)
}

type command struct {
	kind         string
	prompt       string
	images       []proto.PromptImage
	reqID        string
	outcome      adapter.PermissionOutcome
	perm         permAsk
	elicit       elicitAsk
	elicitResult adapter.ElicitationResult
	emission     *proto.Emission
	hard         bool // close: append session.closed, rather than just disposing
	reply        chan cmdResult
	model        string
	mode         string
	effort       string
	recovery     *proto.TurnRecovery // prompt: set when the server started this turn itself
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
)

// ErrBusy is returned when a prompt arrives while a turn is already running.
var ErrBusy = errors.New("a turn is already in progress")

// ErrClosed is returned when a command tries to mutate a closed transcript.
var ErrClosed = errors.New("session is closed")
var ErrNotReady = errors.New("workspace is not ready")

// ErrAlreadyResolved is returned to the loser of a permission race. It is an
// ack, not a failure: the request was answered, just not by this presenter.
var ErrAlreadyResolved = errors.New("already_resolved")

// Start creates a harness session and its actor goroutine. env is the
// provider instance's credential overlay; nil means ambient credentials.
func Start(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.SessionMeta, model, mode string, env map[string]string, logf func(string, ...any)) (*Actor, error) {
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

	sess, err := ad.CreateSession(ctx, hostServices{a}, adapter.CreateOptions{
		SessionID: meta.ID, Cwd: meta.Cwd, Model: model, Mode: mode, Effort: meta.Effort, Env: env,
	})
	if err != nil {
		return nil, err
	}
	a.sess = sess

	a.startCheckpoints()

	a.wg.Add(1)
	go a.run()
	a.pump(sess)

	// session.created is the first event in every session's log.
	a.enqueueEmission(proto.Emit(proto.SessionCreated, proto.SessionCreatedPayload{
		Cwd: meta.Cwd, Harness: meta.Harness, Model: model, Mode: mode, Effort: meta.Effort, Title: meta.Title,
	}))
	return a, nil
}

// StartPending creates an attachable actor without starting a harness. The
// lifecycle runner activates it only after provisioning has completed.
func StartPending(st *store.Store, ad adapter.Adapter, meta store.SessionMeta, env map[string]string, logf func(string, ...any)) *Actor {
	a := &Actor{ID: meta.ID, Harness: meta.Harness, Cwd: meta.Cwd, store: st, adapter: ad, env: env,
		inbox: make(chan command, 64), quit: make(chan struct{}), state: projection.New(meta.ID),
		pendingPerm: map[string]chan adapter.PermissionOutcome{}, pendingElicit: map[string]chan adapter.ElicitationResult{},
		subs: map[string]*Subscriber{}, logf: logf}
	a.wg.Add(1)
	go a.run()
	a.enqueueEmission(proto.Emit(proto.SessionCreated, proto.SessionCreatedPayload{Cwd: meta.Cwd, Harness: meta.Harness, Model: meta.Model, Mode: meta.Mode, Effort: meta.Effort, Title: meta.Title}))
	return a
}

// Resume rebuilds an actor for an existing session id, replaying its log into
// the projection before starting a fresh harness process.
func Resume(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.SessionMeta, env map[string]string, logf func(string, ...any)) (*Actor, error) {
	state, err := loadState(ctx, st, meta.ID)
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
	a.recovery = planRecovery(state)

	sess, err := ad.CreateSession(ctx, hostServices{a}, adapter.CreateOptions{
		SessionID:        meta.ID,
		Cwd:              meta.Cwd,
		Model:            state.Model,
		Mode:             state.Mode,
		Effort:           state.Effort,
		Resume:           true,
		HarnessSessionID: state.HarnessSessionID,
		Env:              env,
	})
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
				TurnID: turn.ID, StopReason: proto.StopError, Error: "server restarted during turn",
			}))
		}
	}
	return a, nil
}

// RestoreClosed creates a read-only actor for a closed transcript. It has no
// harness process, but retains the same attach/state/fanout surface so closed
// sessions remain inspectable by presenters.
func RestoreClosed(ctx context.Context, st *store.Store, meta store.SessionMeta, logf func(string, ...any)) (*Actor, error) {
	state, err := loadState(ctx, st, meta.ID)
	if err != nil {
		return nil, err
	}
	if !state.Closed {
		ev, err := st.Append(ctx, meta.ID, proto.Emit(proto.SessionClosed, proto.SessionClosedPayload{Reason: "closed"}))
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
	}
	a.wg.Add(1)
	go a.run()
	return a, nil
}

func RestorePending(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.SessionMeta, env map[string]string, logf func(string, ...any)) (*Actor, error) {
	state, err := loadState(ctx, st, meta.ID)
	if err != nil {
		return nil, err
	}
	a := &Actor{ID: meta.ID, Harness: meta.Harness, Cwd: meta.Cwd, store: st, adapter: ad, env: env, inbox: make(chan command, 64), quit: make(chan struct{}), state: state, head: state.Seq, pendingPerm: map[string]chan adapter.PermissionOutcome{}, pendingElicit: map[string]chan adapter.ElicitationResult{}, subs: map[string]*Subscriber{}, logf: logf}
	a.wg.Add(1)
	go a.run()
	return a, nil
}

// loadState folds the whole log, using the newest snapshot as a starting point.
func loadState(ctx context.Context, st *store.Store, id string) (*projection.State, error) {
	state := projection.New(id)
	if seq, blob, err := st.LatestSnapshot(ctx, id); err == nil && blob != nil {
		if s, err := projection.FromSnapshot(blob); err == nil {
			state = s
			state.Seq = seq
		}
	}
	for {
		evs, err := st.ReadEvents(ctx, id, state.Seq, 1000)
		if err != nil {
			return nil, err
		}
		if len(evs) == 0 {
			return state, nil
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

// Attention returns the session's derived attention state — see
// projection.Attention. Safe from any goroutine.
func (a *Actor) Attention() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.attention
}

// State returns a snapshot of the projection as of Head. It is produced inside
// the actor loop so it can never observe a half-applied event.
func (a *Actor) State(ctx context.Context) (*projection.State, error) {
	reply := make(chan cmdResult, 1)
	select {
	case a.inbox <- command{kind: "state", reply: reply}:
	case <-a.quit:
		return nil, errors.New("session closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-reply:
		if r.err != nil {
			return nil, r.err
		}
		return r.value.(*projection.State), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
	a.subs[sub.ID] = sub
	a.mu.Unlock()
	return sub
}

func (a *Actor) Unsubscribe(sub *Subscriber) {
	a.mu.Lock()
	delete(a.subs, sub.ID)
	a.mu.Unlock()
}

// ---- commands from presenters ----

func (a *Actor) call(ctx context.Context, c command) (any, error) {
	c.reply = make(chan cmdResult, 1)
	select {
	case a.inbox <- c:
	case <-a.quit:
		return nil, errors.New("session closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-c.reply:
		return r.value, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Prompt starts a turn. Images are already stored on this host; each carries
// the path the harness reads it from.
func (a *Actor) Prompt(ctx context.Context, text string, images []proto.PromptImage) (string, error) {
	v, err := a.call(ctx, command{kind: cmdPrompt, prompt: text, images: images})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Continue restarts work that ended in an error, on a human's say-so. It is
// the same continuation the server starts by itself after a restart, minus the
// attempt cap: someone is watching, and they can stop asking.
func (a *Actor) Continue(ctx context.Context) (string, error) {
	v, err := a.call(ctx, command{kind: cmdContinue})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (a *Actor) Emit(ctx context.Context, em proto.Emission) error {
	_, err := a.call(ctx, command{kind: "emit", emission: &em})
	return err
}

func (a *Actor) Activate(ctx context.Context, cwd, model, mode, effort string) error {
	_, err := a.call(ctx, command{kind: cmdActivate, prompt: cwd, model: model, mode: mode, effort: effort})
	return err
}

// SetMode switches the harness's permission mode mid-session and records the
// change as a session.config_changed event, so every presenter sees it.
func (a *Actor) SetMode(ctx context.Context, mode string) error {
	_, err := a.call(ctx, command{kind: cmdSetMode, mode: mode})
	return err
}

// SetModel switches the harness's model mid-session and records the change as
// a session.config_changed event, so every presenter sees it.
func (a *Actor) SetModel(ctx context.Context, model string) error {
	_, err := a.call(ctx, command{kind: cmdSetModel, model: model})
	return err
}

// SetEffort switches the harness's reasoning effort mid-session and records the
// change as a session.config_changed event, so every presenter sees it.
func (a *Actor) SetEffort(ctx context.Context, effort string) error {
	_, err := a.call(ctx, command{kind: cmdSetEffort, effort: effort})
	return err
}

// ComposerItems asks the live adapter what this exact session can invoke.
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

// ResolvePermission answers a pending request from any presenter.
func (a *Actor) ResolvePermission(ctx context.Context, requestID string, outcome adapter.PermissionOutcome) error {
	_, err := a.call(ctx, command{kind: cmdResolvePerm, reqID: requestID, outcome: outcome})
	return err
}

func (a *Actor) ResolveElicitation(ctx context.Context, requestID string, result adapter.ElicitationResult) error {
	_, err := a.call(ctx, command{kind: cmdResolveElicit, reqID: requestID, elicitResult: result})
	return err
}

// Close ends the session for good: session.closed is appended and the session
// can no longer be resumed.
func (a *Actor) Close(reason string) {
	select {
	case a.inbox <- command{kind: cmdClose, prompt: reason, hard: true}:
	case <-a.quit:
	}
	a.wg.Wait()
}

// Dispose tears down the harness process without ending the session. The log
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
			case a.inbox <- command{kind: cmdHarnessEvent, emission: &em}:
			case <-a.quit:
				return
			}
		}
		select {
		case a.inbox <- command{kind: cmdHarnessExit}:
		case <-a.quit:
		}
	}()
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
		select {
		case c := <-a.inbox:
			if a.handle(c) {
				return
			}
		}
	}
}

func (a *Actor) handle(c command) (stop bool) {
	ctx := context.Background()

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
		c.kind = cmdPrompt
		c.prompt = recoveryPrompt
		c.recovery = &proto.TurnRecovery{ResumeOf: last.ID, Attempt: attempt}
	}

	switch c.kind {
	case "state":
		c.reply <- cmdResult{value: a.state.Clone()}

	case "emit":
		a.append(*c.emission)
		if c.reply != nil {
			c.reply <- cmdResult{}
		}

	case cmdHarnessEvent:
		a.append(*c.emission)

	case cmdHarnessExit:
		a.shutdown(false)
		return true

	case cmdActivate:
		if a.sess != nil {
			c.reply <- cmdResult{}
			return false
		}
		// A pending actor can be restored leniently — for cleanup — with no
		// adapter behind it. Cleanup never activates; anything that does gets
		// a legible refusal instead of a nil dereference.
		if a.adapter == nil {
			c.reply <- cmdResult{err: errors.New("this session's provider instance is no longer configured")}
			return false
		}
		sess, err := a.adapter.CreateSession(ctx, hostServices{a}, adapter.CreateOptions{SessionID: a.ID, Cwd: c.prompt, Model: c.model, Mode: c.mode, Effort: c.effort, Env: a.env})
		if err != nil {
			c.reply <- cmdResult{err: err}
			return false
		}
		a.Cwd, a.sess = c.prompt, sess
		a.pump(sess)
		a.startCheckpoints()
		c.reply <- cmdResult{}

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
			c.reply <- cmdResult{err: errors.New("this harness cannot change permission mode mid-session")}
			return false
		}
		// Bounded: this is a round-trip to the harness from inside the actor
		// loop, and a wedged process must not stall the loop forever.
		modeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := switcher.SetMode(modeCtx, c.mode)
		cancel()
		if err != nil {
			c.reply <- cmdResult{err: err}
			return false
		}
		// Durable and fanned out, so the change lands in the log and every
		// connected presenter follows — same requirement as permission.resolved.
		a.append(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Mode: c.mode}))
		c.reply <- cmdResult{}

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
			c.reply <- cmdResult{err: errors.New("this harness cannot change model mid-session")}
			return false
		}
		// Bounded for the same reason as cmdSetMode: a wedged harness must
		// not stall the actor loop forever.
		modelCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := switcher.SetModel(modelCtx, c.model)
		cancel()
		if err != nil {
			c.reply <- cmdResult{err: err}
			return false
		}
		a.append(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Model: c.model}))
		c.reply <- cmdResult{}

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
			c.reply <- cmdResult{err: errors.New("this harness cannot change reasoning effort mid-session")}
			return false
		}
		// Bounded for the same reason as cmdSetModel: a wedged harness must
		// not stall the actor loop forever.
		effortCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := switcher.SetEffort(effortCtx, c.effort)
		cancel()
		if err != nil {
			c.reply <- cmdResult{err: err}
			return false
		}
		a.append(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Effort: &c.effort}))
		c.reply <- cmdResult{}

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
		listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		items, err := catalogue.ComposerItems(listCtx)
		cancel()
		c.reply <- cmdResult{value: items, err: err}

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
		turnID := uuid.NewString()
		a.turnActive = turnID
		a.append(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: turnID, Prompt: c.model}))
		actionCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		value, err := runner.RunComposerAction(actionCtx, adapter.ComposerActionInput{
			TurnID: turnID,
			Action: c.mode,
			Args:   c.prompt,
		})
		cancel()
		if err != nil {
			a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: turnID, StopReason: proto.StopError, Error: err.Error(),
			}))
		}
		c.reply <- cmdResult{value: value, err: err}

	case cmdPrompt:
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
		a.append(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: turnID, Prompt: c.prompt, Images: c.images, Recovery: c.recovery}))
		// A recovery prompt is the server talking to itself; naming a session
		// after it would bury what the human actually asked for.
		if c.recovery == nil {
			title := truncate(c.prompt, 60)
			if title == "" && len(c.images) > 0 {
				// An image-only prompt still deserves a name in the sidebar.
				title = proto.ImageTitle(len(c.images))
			}
			_ = a.store.SetTitle(ctx, a.ID, title)
		}

		if err := a.sess.Prompt(ctx, adapter.PromptInput{TurnID: turnID, Text: c.prompt, Images: c.images}); err != nil {
			// turnActive is left for append to clear: a prompt that failed on
			// the way out may still have reached the harness, and the closing
			// snapshot is the only way to find out what it did.
			a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: turnID, StopReason: proto.StopError, Error: err.Error(),
			}))
			c.reply <- cmdResult{err: err}
			return false
		}
		c.reply <- cmdResult{value: turnID}

	case cmdCancel:
		if a.turnActive == "" {
			c.reply <- cmdResult{value: "idle"}
			return false
		}
		err := a.sess.Cancel(ctx)
		c.reply <- cmdResult{value: "cancelling", err: err}

	case cmdAskPerm:
		req := c.perm.req
		requestID := uuid.NewString()
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
		requestID := uuid.NewString()
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

	case cmdClose:
		if c.hard {
			a.append(proto.Emit(proto.SessionClosed, proto.SessionClosedPayload{Reason: c.prompt}))
		}
		a.shutdown(c.hard)
		return true
	}
	return false
}

func (a *Actor) shutdown(hard bool) {
	ctx := context.Background()
	phase := "idle"
	switch {
	case hard || a.state.Closed:
		phase = "closed"
	case a.turnActive != "":
		// Disposed mid-turn. The row keeps saying "turn" so the next start can
		// find this session and finish what it was doing; recording idle here
		// would erase the only cheap evidence that work was in flight. A kill
		// -9 leaves the same value behind, so both deaths look alike.
		phase = "turn"
	}
	_ = a.store.SetPhase(ctx, a.ID, phase)

	// Unblock every adapter goroutine waiting on a human.
	for id, ch := range a.pendingPerm {
		select {
		case ch <- adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}:
		default:
		}
		delete(a.pendingPerm, id)
	}
	for id, ch := range a.pendingElicit {
		select {
		case ch <- adapter.ElicitationResult{Action: "cancel"}:
		default:
		}
		delete(a.pendingElicit, id)
	}

	if a.sess != nil {
		_ = a.sess.Close()
	}
	// A session that is merely disposed will be resumed from the log, and its
	// snapshots are the baseline the next turn needs. Only a session closed for
	// good is done with them.
	a.checkpoints.stop()
	if phase == "closed" {
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

// append writes the event, folds it into the projection, and fans it out.
// This is the only place any of those three happen.
func (a *Actor) append(em proto.Emission) {
	ctx := context.Background()

	ev, err := a.store.Append(ctx, a.ID, em)
	if err != nil {
		a.logf("append %s on %s: %v", em.Type, a.ID, err)
		return
	}

	prevPhase := a.state.Phase
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
	}

	// The stored phase column is a cache of the projection, kept for the
	// session list and for restart recovery, which scans rows without folding
	// logs. Syncing it on every turn-shaped transition — not just on turn
	// events — is what lets activity-promoted turns (streaming or a tool
	// going active while the log said idle) survive a restart. Workspace
	// events are excluded: the lifecycle runner owns those writes, and its
	// vocabulary (ready, provisioning, …) is wider than the projection's.
	switch em.Type {
	case proto.TurnStarted, proto.TurnFinished, proto.MessageChunk, proto.ToolCallStarted, proto.ToolCallUpdated, proto.SessionClosed:
		if phase := a.state.Phase; phase != prevPhase && (phase == "turn" || phase == "idle" || phase == "closed") {
			if err := a.store.SetPhase(ctx, a.ID, phase); err != nil {
				a.logf("set phase %s on %s: %v", phase, a.ID, err)
			}
		}
	}

	// Attention is the derived whose-turn-is-it signal. Any event that moves
	// it — turn boundaries, a permission being asked or answered, activity
	// while idle — re-notifies the session list.
	if att := a.state.Attention(); att != a.Attention() {
		a.mu.Lock()
		a.attention = att
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

// ---- host services exposed to adapters ----

type hostServices struct{ a *Actor }

// RequestPermission appends a durable permission.requested event and blocks
// until any presenter resolves it. Nothing about this lives in a connection.
func (h hostServices) RequestPermission(ctx context.Context, req adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	ch := make(chan adapter.PermissionOutcome, 1)

	select {
	case h.a.inbox <- command{kind: cmdAskPerm, perm: permAsk{req: req, ch: ch}}:
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
		return adapter.PermissionOutcome{}, ctx.Err()
	}
}

func (h hostServices) Elicit(ctx context.Context, req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	ch := make(chan adapter.ElicitationResult, 1)
	select {
	case h.a.inbox <- command{kind: cmdAskElicit, elicit: elicitAsk{req: req, ch: ch}}:
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
		return adapter.ElicitationResult{}, ctx.Err()
	}
}

func (h hostServices) Logf(format string, args ...any) { h.a.logf(format, args...) }

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

// AttachResult is what a presenter needs to reconstruct state exactly.
type AttachResult struct {
	Kind     string
	Snapshot *projection.State
	Events   []proto.Event
	Seq      int64
}

// Attach computes the catch-up payload for a cursor. The caller must already
// have subscribed.
func (a *Actor) Attach(ctx context.Context, afterSeq int64, hasCursor bool) (AttachResult, error) {
	head := a.Head()
	gap := head - afterSeq

	if !hasCursor || gap < 0 || gap > MaxReplayGap {
		state, err := a.State(ctx)
		if err != nil {
			return AttachResult{}, err
		}
		return AttachResult{Kind: AttachSnapshot, Snapshot: state, Seq: state.Seq}, nil
	}

	evs, err := a.store.ReadEvents(ctx, a.ID, afterSeq, MaxReplayGap)
	if err != nil {
		return AttachResult{}, err
	}
	seq := afterSeq
	if len(evs) > 0 {
		seq = evs[len(evs)-1].Seq
	}
	return AttachResult{Kind: AttachReplay, Events: evs, Seq: seq}, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
