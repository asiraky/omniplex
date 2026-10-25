package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/procgroup"
)

// Prompt answers one prompt with `claude -p`, straight from the CLI rather
// than through the bridge a session needs. It runs in an empty directory with
// only project settings loaded, no tools, no MCP servers and no skills, and
// keeps no transcript: the model sees the instructions and the input and
// nothing of the user's setup.
func (a *Adapter) Prompt(ctx context.Context, env map[string]string, req adapter.PromptRequest) (string, error) {
	claude, ok := a.Executable()
	if !ok {
		return "", errors.New("Claude Code is not installed")
	}
	dir, err := os.MkdirTemp("", "omniplex-claude-prompt-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	args := []string{"-p",
		"--model", req.Model,
		"--system-prompt", req.Instructions,
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources", "project",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--output-format", "json",
	}
	if req.Effort != "" {
		args = append(args, "--effort", req.Effort)
	}
	cmd := exec.CommandContext(ctx, claude, args...)
	cmd.Dir = dir
	cmd.Env = adapter.MergeEnv(os.Environ(), env)
	cmd.Stdin = strings.NewReader(req.Input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	tree := procgroup.Attach(cmd, "claude-prompt")
	defer tree.Kill()
	cmd.Cancel = func() error {
		tree.Kill()
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return promptResult(out, err, stderr.String())
}

// promptResult reads the one JSON object `claude -p --output-format json`
// prints. It prints one on most failures too, with is_error set and the
// reason in result, and exits non-zero; stderr is the fallback for the rest.
func promptResult(out []byte, runErr error, stderr string) (string, error) {
	var res struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Subtype string `json:"subtype"`
	}
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out), &res); jsonErr != nil {
		if runErr == nil {
			runErr = jsonErr
		}
		if msg := strings.TrimSpace(stderr); msg != "" {
			return "", fmt.Errorf("claude -p: %v: %s", runErr, msg)
		}
		return "", fmt.Errorf("claude -p: %v", runErr)
	}
	if res.IsError || res.Subtype != "success" {
		return "", fmt.Errorf("claude -p: %s: %s", res.Subtype, strings.TrimSpace(res.Result))
	}
	return res.Result, nil
}
