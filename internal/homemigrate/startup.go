package homemigrate

import (
	"context"
	"os"
	"strings"
)

// Startup runs the migration as the server starts, before any thread or
// harness: conversations its threads resume are not in use yet, so they move
// even if recently modified. It only logs; whatever happens, the server
// starts.
func Startup(ctx context.Context, opts Options, logf func(format string, args ...any)) {
	defer func() {
		if r := recover(); r != nil {
			logf("%spanicked: %v; the server starts anyway, see the newest manifest in ~/.omniplex/migrations", logPrefix, r)
		}
	}()
	opts.ExemptReferenced = true
	opts.Retry = false
	if opts.ServerEnv == nil {
		opts.ServerEnv = os.Getenv
	}
	p, err := NewPlan(ctx, opts)
	if err != nil {
		logf("%scould not plan: %v; nothing moved", logPrefix, err)
		return
	}
	rep, err := Execute(ctx, p)
	if rep != nil {
		for _, line := range rep.Lines() {
			logf("%s", line)
		}
	}
	if err != nil {
		logf("%s%v", logPrefix, strings.TrimSpace(err.Error()))
	}
}
