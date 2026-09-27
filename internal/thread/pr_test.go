package session

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// fakeGh puts a `gh` on PATH ahead of any real one. The script writes the
// arguments it was handed to argsFile, so a test can assert what was asked as
// well as what came back.
func fakeGh(t *testing.T, stdout, stderr string, exit int) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if stderr != "" {
		script += "printf '%s' " + shellQuote(stderr) + " >&2\n"
	}
	if stdout != "" {
		script += "printf '%s' " + shellQuote(stdout) + "\n"
	}
	script += "exit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// prJSON wraps one pull request the way `gh pr list --json` returns it.
func prJSON(body string) string { return "[" + body + "]" }

// prSession registers a session the PR lookup will accept, in a directory that
// exists, so only the thing under test decides the outcome.
func prSession(t *testing.T, mode, branch string) (*Manager, string) {
	t.Helper()
	root, worktree, _ := gitRepo(t)
	st, p := testProject(t, root)
	now := proto.NowMillis()
	err := st.CreateSession(context.Background(), store.SessionMeta{
		ID: "s1", Cwd: worktree, Harness: "fake", Title: "t",
		CreatedAt: now, UpdatedAt: now, Phase: "idle",
		ProjectID: p.ID, Branch: branch, WorkspaceMode: mode,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	return mgr, worktree
}

// headOf is the commit a checkout is actually sitting on, which is what a
// finished branch's merged pull request should agree with.
func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestParsePRCallsAPullRequestMergedOnlyWithBothStateAndTimestamp(t *testing.T) {
	cases := []struct {
		name string
		json string
		want bool
	}{
		{"merged", prJSON(`{"number":7,"state":"MERGED","mergedAt":"2026-08-20T01:02:03Z"}`), true},
		{"lowercase state", prJSON(`{"number":7,"state":"merged","mergedAt":"2026-08-20T01:02:03Z"}`), true},
		{"open", prJSON(`{"number":7,"state":"OPEN","mergedAt":""}`), false},
		{"closed unmerged", prJSON(`{"number":7,"state":"CLOSED","mergedAt":""}`), false},
		{"state without timestamp", prJSON(`{"number":7,"state":"MERGED","mergedAt":""}`), false},
		{"timestamp without state", prJSON(`{"number":7,"state":"CLOSED","mergedAt":"2026-08-20T01:02:03Z"}`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pr, reason := parsePR([]byte(c.json))
			if reason != "" {
				t.Fatalf("unexpected reason %q", reason)
			}
			if pr.Merged != c.want {
				t.Fatalf("merged = %v, want %v", pr.Merged, c.want)
			}
		})
	}
}

func TestParsePRRefusesOutputWithNoPullRequestInIt(t *testing.T) {
	if _, reason := parsePR([]byte(`[]`)); reason == "" {
		t.Fatal("an empty list must not pass as a pull request")
	}
	if _, reason := parsePR([]byte(`[{}]`)); reason == "" {
		t.Fatal("an empty object must not pass as a pull request")
	}
	if _, reason := parsePR([]byte(`not json`)); reason == "" {
		t.Fatal("unparseable output must not pass as a pull request")
	}
}

func TestSessionPRAsksGhAboutTheSessionsOwnBranchByName(t *testing.T) {
	// Deliberately a branch that is also a valid pull request number: the
	// selector must be unambiguous or this session adopts PR #75's fate.
	mgr, worktree := prSession(t, "managed", "75")
	argsFile := fakeGh(t, prJSON(`{"number":9,"title":"Prompt","url":"https://example/9","state":"MERGED","mergedAt":"2026-08-20T01:02:03Z","headRefOid":"`+headOf(t, worktree)+`"}`), "", 0)

	pr, reason := mgr.SessionPR(context.Background(), "s1")
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if pr == nil || pr.Number != 9 || !pr.Merged {
		t.Fatalf("merged pull request not reported: %+v", pr)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--head\n75\n") {
		t.Fatalf("the branch must be passed as a branch, not a positional selector: %q", args)
	}
}

func TestSessionPRWillNotCallABranchFinishedAfterItHasMovedOn(t *testing.T) {
	mgr, _ := prSession(t, "managed", "issue/75-prompt")
	// The merge happened at a commit the worktree is no longer sitting on:
	// there is unmerged work here, whatever the pull request says.
	fakeGh(t, prJSON(`{"number":75,"state":"MERGED","mergedAt":"2026-08-20T01:02:03Z","headRefOid":"0000000000000000000000000000000000000000"}`), "", 0)

	pr, reason := mgr.SessionPR(context.Background(), "s1")
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if pr == nil {
		t.Fatal("the pull request itself is still worth reporting")
	}
	if pr.Merged {
		t.Fatal("a branch with commits the merge never contained is not finished work")
	}
}

func TestSessionPRSaysNothingForALocalSession(t *testing.T) {
	mgr, _ := prSession(t, "local", "main")
	fakeGh(t, prJSON(`{"number":75,"state":"MERGED","mergedAt":"2026-08-20T01:02:03Z"}`), "", 0)

	pr, reason := mgr.SessionPR(context.Background(), "s1")
	if pr != nil {
		t.Fatalf("a local session has no worktree to reclaim: %+v", pr)
	}
	if reason == "" {
		t.Fatal("the refusal must say why")
	}
}

func TestSessionPRSaysNothingForASessionWithNoBranch(t *testing.T) {
	mgr, _ := prSession(t, "managed", "")
	fakeGh(t, prJSON(`{"number":75,"state":"MERGED","mergedAt":"2026-08-20T01:02:03Z"}`), "", 0)

	if pr, reason := mgr.SessionPR(context.Background(), "s1"); pr != nil || reason == "" {
		t.Fatalf("pr = %+v, reason = %q", pr, reason)
	}
}

func TestSessionPRTreatsAGhFailureAsSimplyNotKnowing(t *testing.T) {
	mgr, _ := prSession(t, "borrowed", "issue/75-prompt")
	fakeGh(t, "", "gh: not authenticated", 1)

	pr, reason := mgr.SessionPR(context.Background(), "s1")
	if pr != nil {
		t.Fatalf("a failed lookup is not a pull request: %+v", pr)
	}
	if !strings.Contains(reason, "not authenticated") {
		t.Fatalf("gh's own words should survive: %q", reason)
	}
}

func TestSessionPRTreatsAnEmptyListAsNoPullRequestYet(t *testing.T) {
	mgr, _ := prSession(t, "managed", "issue/75-prompt")
	fakeGh(t, "[]", "", 0)

	if pr, reason := mgr.SessionPR(context.Background(), "s1"); pr != nil || reason == "" {
		t.Fatalf("pr = %+v, reason = %q", pr, reason)
	}
}
