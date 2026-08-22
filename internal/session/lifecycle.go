package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

const maxHookOutput = 4 << 20

type provisionContext struct {
	Version               int             `json:"version"`
	SessionID             string          `json:"sessionId"`
	ProjectRoot           string          `json:"projectRoot"`
	RequestedBranch       string          `json:"requestedBranch,omitempty"`
	BaseRef               string          `json:"baseRef,omitempty"`
	SuggestedWorktreePath string          `json:"suggestedWorktreePath,omitempty"`
	ProvisionResult       json.RawMessage `json:"provisionResult,omitempty"`
}

type provisionResult struct {
	Cwd       string         `json:"cwd"`
	Branch    string         `json:"branch,omitempty"`
	Resources map[string]any `json:"resources,omitempty"`
}

func lifecycleDir(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".omniplex", "workspaces", sessionID)
	return dir, os.MkdirAll(dir, 0o700)
}

func (m *Manager) provision(meta store.SessionMeta, p project.Project, a *Actor) {
	ctx := context.Background()
	_ = a.Emit(ctx, proto.Emit(proto.WorkspaceRequested, proto.WorkspaceRequestedPayload{
		ProjectID: p.ID, ProjectRoot: p.Root, Mode: meta.WorkspaceMode, Branch: meta.Branch, BaseRef: baseRefFor(meta, p),
	}))
	_ = m.store.SetPhase(ctx, meta.ID, "provisioning")
	m.notifyList()

	// meta.Cwd is the project root for a local session and the borrowed
	// checkout for an attached one; either way it is already the answer when
	// nothing has to be provisioned.
	base := meta.Cwd
	if base == "" {
		base = p.Root
	}
	result := provisionResult{Cwd: base, Branch: meta.Branch}
	if meta.ProvisionScript != "" {
		var err error
		result, err = m.runProvisionHook(ctx, meta, p, a)
		if err != nil {
			m.provisionFailed(meta.ID, a, err)
			return
		}
	} else if meta.WorkspaceMode == "managed" {
		var err error
		result, err = m.createWorktree(ctx, meta, p, a)
		if err != nil {
			m.provisionFailed(meta.ID, a, err)
			return
		}
	}

	info, err := os.Stat(result.Cwd)
	if err != nil || !info.IsDir() {
		m.provisionFailed(meta.ID, a, fmt.Errorf("provision result cwd is not a directory: %s", result.Cwd))
		return
	}
	raw, _ := json.Marshal(result)
	if err := m.store.UpdateWorkspace(ctx, meta.ID, result.Cwd, result.Branch, "provisioning", raw); err != nil {
		m.provisionFailed(meta.ID, a, err)
		return
	}
	a.Cwd = result.Cwd
	if err := a.Activate(ctx, result.Cwd, meta.Model, meta.Mode, meta.Effort); err != nil {
		m.provisionFailed(meta.ID, a, fmt.Errorf("start harness: %w", err))
		return
	}
	_ = m.store.SetPhase(ctx, meta.ID, "ready")
	_ = a.Emit(ctx, proto.Emit(proto.WorkspaceReady, proto.WorkspaceReadyPayload{Cwd: result.Cwd, Branch: result.Branch, Resources: result.Resources}))
	m.notifyList()
}

func (m *Manager) provisionFailed(id string, a *Actor, err error) {
	_ = m.store.SetPhase(context.Background(), id, "provision_failed")
	_ = a.Emit(context.Background(), proto.Emit(proto.WorkspaceFailed, proto.WorkspaceFailedPayload{Hook: "provision", Error: err.Error()}))
	m.notifyList()
}

func (m *Manager) runProvisionHook(ctx context.Context, meta store.SessionMeta, p project.Project, a *Actor) (provisionResult, error) {
	stateDir, err := lifecycleDir(meta.ID)
	if err != nil {
		return provisionResult{}, err
	}
	hook, err := project.ResolveHook(p.Root, meta.ProvisionScript)
	if err != nil {
		return provisionResult{}, err
	}
	branch, suggested := workspaceTarget(meta, p)
	meta.Branch = branch
	compatibility := isCompatibilityHook(hook, "setup")
	var hookArgs []string
	var compatibleResult provisionResult
	if compatibility {
		compatibleResult, err = m.createWorktree(ctx, meta, p, a)
		if err != nil {
			return provisionResult{}, err
		}
		raw, _ := json.Marshal(compatibleResult)
		if err := m.store.UpdateWorkspace(ctx, meta.ID, compatibleResult.Cwd, compatibleResult.Branch, "provisioning", raw); err != nil {
			return provisionResult{}, err
		}
		hookArgs = []string{compatibleResult.Branch}
		if base := baseRefFor(meta, p); base != "" {
			hookArgs = []string{"--base", base, compatibleResult.Branch}
		}
	}
	input := provisionContext{Version: 1, SessionID: meta.ID, ProjectRoot: p.Root, RequestedBranch: meta.Branch, BaseRef: baseRefFor(meta, p), SuggestedWorktreePath: suggested}
	contextPath, resultPath := filepath.Join(stateDir, "context.json"), filepath.Join(stateDir, "result.json")
	if err := writeJSON(contextPath, input); err != nil {
		return provisionResult{}, err
	}
	_ = os.Remove(resultPath)
	if err := m.runHook(ctx, a, meta, p.Root, hook, hookArgs, "provision", contextPath, resultPath, stateDir, p.Config.Workspace.ProvisionTimeoutSeconds); err != nil {
		return provisionResult{}, err
	}
	b, err := os.ReadFile(resultPath)
	if err != nil {
		if compatibility && os.IsNotExist(err) {
			return compatibleResult, nil
		}
		return provisionResult{}, fmt.Errorf("provision did not write OMNIPLEX_RESULT_FILE: %w", err)
	}
	var result provisionResult
	if err := json.Unmarshal(b, &result); err != nil {
		return result, fmt.Errorf("parse provision result: %w", err)
	}
	if result.Cwd == "" {
		return result, errors.New("provision result must contain cwd")
	}
	if !filepath.IsAbs(result.Cwd) {
		result.Cwd = filepath.Join(p.Root, result.Cwd)
	}
	return result, nil
}

func (m *Manager) runHook(parent context.Context, a *Actor, meta store.SessionMeta, cwd, hook string, hookArgs []string, kind, contextPath, resultPath, stateDir string, seconds int) error {
	ctx, cancel := context.WithTimeout(parent, time.Duration(seconds)*time.Second)
	defer cancel()
	runID, started := uuid.NewString(), time.Now()
	cmd, display := hookCommand(ctx, hook, hookArgs...)
	_ = a.Emit(parent, proto.Emit(proto.WorkspaceHookStarted, proto.WorkspaceHookStartedPayload{RunID: runID, Hook: kind, Command: display}))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "OMNIPLEX_LIFECYCLE_VERSION=2", "OMNIPLEX_HOOK="+kind, "OMNIPLEX_SESSION_ID="+meta.ID, "OMNIPLEX_PROJECT_ROOT="+cwd, "OMNIPLEX_CONTEXT_FILE="+contextPath, "OMNIPLEX_RESULT_FILE="+resultPath, "OMNIPLEX_STATE_DIR="+stateDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	truncated := false
	stream := func(name string, scanner *bufio.Scanner) {
		defer wg.Done()
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 256*1024)
		for scanner.Scan() {
			chunk := redactHookOutput(scanner.Text()) + "\n"
			mu.Lock()
			allowed := maxHookOutput - total
			if allowed > len(chunk) {
				allowed = len(chunk)
			}
			if allowed < len(chunk) {
				truncated = true
			}
			if allowed > 0 {
				total += allowed
				_ = a.Emit(parent, proto.Emit(proto.WorkspaceHookOutput, proto.WorkspaceHookOutputPayload{RunID: runID, Hook: kind, Stream: name, Chunk: chunk[:allowed]}))
			}
			mu.Unlock()
		}
	}
	wg.Add(2)
	go stream("stdout", bufio.NewScanner(stdout))
	go stream("stderr", bufio.NewScanner(stderr))
	err = cmd.Wait()
	wg.Wait()
	if truncated {
		_ = a.Emit(parent, proto.Emit(proto.WorkspaceHookOutput, proto.WorkspaceHookOutputPayload{RunID: runID, Hook: kind, Stream: "stdout", Chunk: "\n[output truncated by omniplex]\n"}))
	}
	exit := 0
	if err != nil {
		exit = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		}
	}
	_ = a.Emit(parent, proto.Emit(proto.WorkspaceHookFinished, proto.WorkspaceHookFinishedPayload{RunID: runID, Hook: kind, ExitCode: exit, DurationMs: time.Since(started).Milliseconds()}))
	if ctx.Err() != nil {
		return fmt.Errorf("%s hook timed out: %w", kind, ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("%s hook exited %d", kind, exit)
	}
	return nil
}

func hookCommand(ctx context.Context, hook string, args ...string) (*exec.Cmd, string) {
	switch strings.ToLower(filepath.Ext(hook)) {
	case ".ts", ".mts":
		argv := append([]string{"run", hook}, args...)
		return exec.CommandContext(ctx, "bun", argv...), strings.Join(append([]string{"bun", "run", hook}, args...), " ")
	case ".js", ".mjs", ".cjs":
		argv := append([]string{hook}, args...)
		return exec.CommandContext(ctx, "node", argv...), strings.Join(append([]string{"node", hook}, args...), " ")
	case ".sh":
		argv := append([]string{hook}, args...)
		return exec.CommandContext(ctx, "sh", argv...), strings.Join(append([]string{"sh", hook}, args...), " ")
	default:
		return exec.CommandContext(ctx, hook, args...), strings.Join(append([]string{hook}, args...), " ")
	}
}

func isCompatibilityHook(path, kind string) bool {
	base := strings.ToLower(filepath.Base(path))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "worktree-"+kind {
		return true
	}
	return base == kind && filepath.Base(filepath.Dir(path)) == "worktree" && filepath.Base(filepath.Dir(filepath.Dir(path))) == ".claude"
}

// baseRefFor is the ref a new worktree branches from: the session's own choice
// where it made one, and the project default otherwise. Empty means neither
// was set, which each caller reads its own way — the hooks omit --base, and
// `git worktree add` falls back to HEAD.
func baseRefFor(meta store.SessionMeta, p project.Project) string {
	if base := strings.TrimSpace(meta.BaseRef); base != "" {
		return base
	}
	return strings.TrimSpace(p.Config.Defaults.BaseBranch)
}

// checkBaseRef refuses a base that Git cannot resolve to a commit, so a typo
// surfaces as "no such base" rather than as a worktree branched from somewhere
// unexpected. It also keeps a ref that looks like a flag out of the argv of
// `git worktree add`, where it would be read as one.
func checkBaseRef(ctx context.Context, root, base string) error {
	if strings.HasPrefix(base, "-") {
		return fmt.Errorf("base ref %q is not a valid ref", base)
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", base+"^{commit}")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("base ref %q does not resolve to a commit", base)
	}
	return nil
}

func workspaceTarget(meta store.SessionMeta, p project.Project) (string, string) {
	branch := meta.Branch
	if branch == "" {
		branch = "feature/omniplex-" + strings.ReplaceAll(meta.ID, "-", "")[:8]
	}
	dir := strings.ReplaceAll(branch, "/", "-")
	return branch, filepath.Join(p.Root, p.Config.Workspace.SuggestedRoot, dir)
}

func redactHookOutput(value string) string {
	for _, entry := range os.Environ() {
		name, secret, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if !ok || len(secret) < 4 || !(strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.HasSuffix(upper, "_KEY")) {
			continue
		}
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func (m *Manager) createWorktree(ctx context.Context, meta store.SessionMeta, p project.Project, a *Actor) (provisionResult, error) {
	branch, path := workspaceTarget(meta, p)
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return provisionResult{Cwd: path, Branch: branch}, nil
	}
	base := baseRefFor(meta, p)
	if base == "" {
		base = "HEAD"
	}
	_ = a.Emit(ctx, proto.Emit(proto.WorkspaceHookStarted, proto.WorkspaceHookStartedPayload{RunID: uuid.NewString(), Hook: "provision", Command: "git worktree add " + path}))
	// An existing branch is checked out where it stands: the base names where a
	// branch begins, and this one already began somewhere. Only the create case
	// consults it, so only the create case has to be able to resolve it.
	args := []string{"worktree", "add", path, branch}
	branchCheck := exec.CommandContext(ctx, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	branchCheck.Dir = p.Root
	if branchCheck.Run() != nil {
		if err := checkBaseRef(ctx, p.Root, base); err != nil {
			return provisionResult{}, err
		}
		args = []string{"worktree", "add", path, "-b", branch, base}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = p.Root
	b, err := cmd.CombinedOutput()
	if len(b) > 0 && a != nil {
		_ = a.Emit(ctx, proto.Emit(proto.WorkspaceHookOutput, proto.WorkspaceHookOutputPayload{Hook: "provision", Stream: "stdout", Chunk: string(b)}))
	}
	if err != nil {
		return provisionResult{}, fmt.Errorf("git worktree add: %w", err)
	}
	return provisionResult{Cwd: path, Branch: branch}, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// cleanup releases a session. removeWorktree decides whether the checkout on
// disk goes with it; nothing infers that from the workspace mode any more,
// because the mode says who created the directory and not whether the user
// wants it gone.
func (m *Manager) cleanup(meta store.SessionMeta, p project.Project, a *Actor, purge, removeWorktree bool) {
	ctx := context.Background()
	_ = a.Emit(ctx, proto.Emit(proto.WorkspaceCleanupStarted, map[string]any{"purge": purge, "removeWorktree": removeWorktree}))
	_ = m.store.SetPhase(ctx, meta.ID, "cleaning")
	m.notifyList()

	// The snapshots have to go before the checkout does. They are refs in the
	// repository this worktree belongs to, so removing the worktree strands
	// them — along with every blob they hold, including the contents of files
	// Git was otherwise never tracking. This is not conditional on the
	// workspace mode: a local session's snapshots are just as much ours to
	// clean up, and its checkout is the one the user keeps.
	purgeCheckpoints(ctx, meta.Cwd, meta.ID, m.logf)

	// The main checkout is never omniplex's to remove however the caller asks, and
	// the mode is checked before the script rather than after it. A session
	// created by an earlier build can be local and still carry a deprovision
	// script; running that script would be running a worktree-teardown hook
	// over the directory the user works in.
	var err error
	// The caller checked this before starting; teardown runs outside its lock,
	// so it is checked again here, as late as it can be. A session that moved
	// in while this one was shutting down keeps its files.
	if removeWorktree && meta.WorkspaceMode != "local" {
		if holder, shared := m.otherSessionIn(ctx, meta.ID, meta.Cwd); shared {
			m.logf("keeping %s: %q is still there", meta.Cwd, holder)
			removeWorktree = false
		}
	}
	if removeWorktree && meta.WorkspaceMode != "local" {
		if meta.DeprovisionScript != "" {
			err = m.runDeprovisionHook(ctx, meta, p, a)
		} else {
			err = m.removeWorktree(ctx, meta, p, a)
		}
	}
	if err != nil {
		_ = m.store.SetPhase(ctx, meta.ID, "cleanup_failed")
		_ = a.Emit(ctx, proto.Emit(proto.WorkspaceCleanupFailed, proto.WorkspaceFailedPayload{Hook: "deprovision", Error: err.Error()}))
		m.notifyList()
		return
	}
	_ = a.Emit(ctx, proto.Emit(proto.WorkspaceCleanupFinished, map[string]any{}))
	_ = a.Emit(ctx, proto.Emit(proto.WorkspaceReleased, map[string]any{}))
	// Closing changes the durable phase to closed before the actor's exit hook
	// removes it from the live map. Keep Get behind the lifecycle lock across
	// that transition so it can never return the dying cleanup actor. For a
	// purge, keep the deletion in the same critical section so Get cannot
	// restore a transcript in the gap between close and delete either.
	m.lifecycle.Lock()
	a.Close("workspace released")
	if purge {
		// Only once the session is actually gone: a failed delete leaves the
		// transcript in place, and a transcript whose pictures were thrown away
		// is worse than one that is simply still there.
		if err := m.store.DeleteSession(ctx, meta.ID); err != nil {
			m.logf("delete cleaned session %s: %v", meta.ID, err)
		} else {
			m.purgeAttachments(meta.ID)
		}
	}
	m.lifecycle.Unlock()
	m.notifyList()
}

func (m *Manager) runDeprovisionHook(ctx context.Context, meta store.SessionMeta, p project.Project, a *Actor) error {
	stateDir, err := lifecycleDir(meta.ID)
	if err != nil {
		return err
	}
	hook, err := project.ResolveHook(p.Root, meta.DeprovisionScript)
	if err != nil {
		return err
	}
	input := provisionContext{Version: 1, SessionID: meta.ID, ProjectRoot: p.Root, RequestedBranch: meta.Branch, BaseRef: baseRefFor(meta, p), ProvisionResult: meta.ProvisionResult}
	contextPath, resultPath := filepath.Join(stateDir, "deprovision-context.json"), filepath.Join(stateDir, "deprovision-result.json")
	if err := writeJSON(contextPath, input); err != nil {
		return err
	}
	var hookArgs []string
	if isCompatibilityHook(hook, "teardown") {
		identity := meta.Branch
		if identity == "" {
			identity = filepath.Base(meta.Cwd)
		}
		hookArgs = []string{identity}
	}
	return m.runHook(ctx, a, meta, p.Root, hook, hookArgs, "deprovision", contextPath, resultPath, stateDir, p.Config.Workspace.DeprovisionTimeoutSeconds)
}

func (m *Manager) removeWorktree(ctx context.Context, meta store.SessionMeta, p project.Project, a *Actor) error {
	return m.removeGitWorktree(ctx, meta, p, a, false)
}

func (m *Manager) removeGitWorktree(ctx context.Context, meta store.SessionMeta, p project.Project, a *Actor, allowMissingLease bool) error {
	target, err := filepath.Abs(meta.Cwd)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(p.Root)
	if err != nil {
		return err
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(target); canonicalErr == nil {
		target = canonical
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(root); canonicalErr == nil {
		root = canonical
	}
	if target == "" || target == root {
		if allowMissingLease {
			prune := exec.CommandContext(ctx, "git", "worktree", "prune")
			prune.Dir = root
			return prune.Run()
		}
		return errors.New("refusing to remove the project root")
	}
	if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
		prune := exec.CommandContext(ctx, "git", "worktree", "prune")
		prune.Dir = root
		return prune.Run()
	}
	if rel, relErr := filepath.Rel(target, root); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("refusing to remove an ancestor of the project root")
	}
	listed, err := exec.CommandContext(ctx, "git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}
	found := false
	for _, line := range strings.Split(string(listed), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			abs, _ := filepath.Abs(path)
			if canonical, canonicalErr := filepath.EvalSymlinks(abs); canonicalErr == nil {
				abs = canonical
			}
			if abs == target {
				found = true
				break
			}
		}
	}
	if !found {
		if allowMissingLease {
			return fmt.Errorf("cannot force delete: %s exists but is not a registered Git worktree", target)
		}
		return fmt.Errorf("refusing cleanup: %s is not a registered worktree", target)
	}
	common := func(dir string) (string, error) {
		b, e := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-common-dir").Output()
		if e != nil {
			return "", e
		}
		v := strings.TrimSpace(string(b))
		if !filepath.IsAbs(v) {
			v = filepath.Join(dir, v)
		}
		abs, absErr := filepath.Abs(v)
		if absErr != nil {
			return "", absErr
		}
		if canonical, canonicalErr := filepath.EvalSymlinks(abs); canonicalErr == nil {
			abs = canonical
		}
		return abs, nil
	}
	rootCommon, err := common(root)
	if err != nil {
		return err
	}
	targetCommon, err := common(target)
	if err != nil {
		return err
	}
	if rootCommon != targetCommon {
		return errors.New("refusing cleanup: worktree belongs to another repository")
	}
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", target)
	cmd.Dir = p.Root
	b, err := cmd.CombinedOutput()
	if len(b) > 0 && a != nil {
		_ = a.Emit(ctx, proto.Emit(proto.WorkspaceHookOutput, proto.WorkspaceHookOutputPayload{Hook: "deprovision", Stream: "stdout", Chunk: string(b)}))
	}
	if err != nil {
		return err
	}
	prune := exec.CommandContext(ctx, "git", "worktree", "prune")
	prune.Dir = p.Root
	return prune.Run()
}
