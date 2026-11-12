package thread

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

// repoLocks serialises the Git commands that write to a repository's shared
// state — fetch, branch creation, worktree add and remove. Two threads
// provisioning at once in one repository otherwise race on .git's lock files,
// and whichever loses fails with "Unable to create '.../index.lock'". Every
// worktree of a repository shares one .git, so the key is the common dir, not
// the checkout.
var repoLocks sync.Map // common dir -> *sync.Mutex

// lockRepo blocks until this process holds the repository's write lock, and
// returns the release. It is not re-entrant: nothing holding it may call
// anything else that takes it.
func lockRepo(ctx context.Context, root string) func() {
	mu, _ := repoLocks.LoadOrStore(repoKey(ctx, root), &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

func repoKey(ctx context.Context, root string) string {
	if common, ok := gitCapture(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir"); ok && common != "" {
		return canonicalPath(filepath.Clean(common))
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return canonicalPath(strings.TrimSpace(root))
}
