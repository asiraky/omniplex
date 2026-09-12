package preview

import (
	"context"
	"time"
)

// Target is one session worth polling, as the watcher needs it.
type Target struct {
	SessionID string
	// Root is the session's checkout. Detection is scoped to it: a listening
	// port only counts as this session's if the process holding it is working
	// inside this directory.
	Root   string
	Branch string
	// Declared are services the project named, which are trusted without
	// probing.
	Declared []Found
}

// Watcher keeps the registry current.
//
// It polls rather than watches because there is no portable event for "a
// process bound a port": kqueue does not report it, and the alternatives are
// platform-specific enough to be worse than a cheap poll. The cost is bounded
// by only ever polling sessions someone is actually looking at.
type Watcher struct {
	registry *Registry
	targets  func(context.Context) []Target
	interval time.Duration
	// now exists so tests can drive the loop without sleeping.
	nudge chan struct{}
}

func NewWatcher(registry *Registry, targets func(context.Context) []Target, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &Watcher{
		registry: registry,
		targets:  targets,
		interval: interval,
		nudge:    make(chan struct{}, 1),
	}
}

// Nudge asks for an immediate poll. Called when a turn finishes, because that
// is the moment a dev server is most likely to have just come up and the
// moment the user is most likely to be looking.
func (w *Watcher) Nudge() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

// Run polls until the context is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		w.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.nudge:
		}
	}
}

func (w *Watcher) poll(ctx context.Context) {
	targets := w.targets(ctx)
	if len(targets) == 0 {
		return
	}
	// A slow docker or lsof must not hold the loop open indefinitely; the
	// next tick will try again anyway.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, t := range targets {
		if t.Root == "" {
			continue
		}
		w.registry.Refresh(ctx, t.SessionID, t.Root, t.Branch, t.Declared)
	}
}
