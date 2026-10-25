package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/procgroup"
)

// Prompt answers one prompt with `codex exec`, in an empty directory, read
// only and ephemeral. --ignore-user-config keeps the user's MCP servers and
// profiles out of it: starting those costs more than the answer does, and a
// broken one stalls the run for most of a minute. The login lives beside the
// config, not in it, so the account is still the instance's.
//
// Codex has no system prompt to set, so the instructions lead the input.
func (a *Adapter) Prompt(ctx context.Context, env map[string]string, req adapter.PromptRequest) (string, error) {
	dir, err := os.MkdirTemp("", "omniplex-codex-prompt-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	args := []string{"exec", "--json",
		"--model", req.Model,
		"--skip-git-repo-check",
		"--ephemeral",
		"--ignore-rules",
		"--ignore-user-config",
		"--sandbox", "read-only",
		"--cd", dir,
	}
	if req.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+req.Effort)
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, a.Bin, args...)
	cmd.Dir = dir
	cmd.Env = adapter.MergeEnv(os.Environ(), env)
	input := req.Input
	if req.Instructions != "" {
		input = req.Instructions + "\n\n" + input
	}
	cmd.Stdin = strings.NewReader(input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	tree := procgroup.Attach(cmd, "codex-prompt")
	defer tree.Kill()
	cmd.Cancel = func() error {
		tree.Kill()
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start %s exec: %w", a.Bin, err)
	}
	answer, readErr := execAnswer(stdout)
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case readErr != nil:
		return "", readErr
	case waitErr != nil:
		return "", fmt.Errorf("codex exec: %w", waitErr)
	}
	return answer, nil
}

// execAnswer reads the event stream `codex exec --json` prints: the last
// agent message is the answer, and a failed turn is an error. Item errors are
// warnings (an unknown model's metadata, say) that the turn may survive.
func execAnswer(r io.Reader) (string, error) {
	var answer string
	var failure string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(bytes.TrimSpace(sc.Bytes()), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "item.completed" && ev.Item.Type == "agent_message":
			answer = ev.Item.Text
		case ev.Type == "turn.failed":
			failure = ev.Error.Message
		case ev.Type == "error" && failure == "":
			failure = ev.Message
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if failure != "" {
		return "", fmt.Errorf("codex exec: %s", upstreamMessage(failure))
	}
	if answer == "" {
		return "", errors.New("codex exec gave no answer")
	}
	return answer, nil
}

// upstreamMessage digs the message out of an API error Codex passes on as
// JSON in a string, which is how a refused model arrives.
func upstreamMessage(s string) string {
	var wrapped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s), &wrapped) == nil && wrapped.Error.Message != "" {
		return wrapped.Error.Message
	}
	return s
}
