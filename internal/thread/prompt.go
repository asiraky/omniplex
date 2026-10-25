package thread

import (
	"context"
	"errors"
	"fmt"

	"github.com/asiraky/omniplex/internal/adapter"
)

// ErrNoPrompter says a thread's harness cannot answer one-off prompts.
var ErrNoPrompter = errors.New("this thread's harness cannot answer one-off prompts")

// Prompter answers one-off prompts on the account a thread runs under.
type Prompter struct {
	// Driver is the harness: "claude", "codex".
	Driver string
	// Models is the instance's catalogue, as the model picker has it.
	Models []adapter.ModelMeta
	run    func(context.Context, adapter.PromptRequest) (string, error)
}

// Prompt asks once. See adapter.Prompter.
func (p Prompter) Prompt(ctx context.Context, req adapter.PromptRequest) (string, error) {
	return p.run(ctx, req)
}

// PrompterFor is one-off prompting on the provider instance a thread runs
// under, so a job done for the thread is billed to its account and never to
// whichever account happens to be the machine's.
func (m *Manager) PrompterFor(ctx context.Context, threadID string) (Prompter, error) {
	meta, err := m.store.Thread(ctx, threadID)
	if err != nil {
		return Prompter{}, err
	}
	reg, err := m.instanceFor(meta)
	if err != nil {
		return Prompter{}, err
	}
	if !reg.inst.Enabled {
		return Prompter{}, fmt.Errorf("%s is disabled", reg.inst.DisplayName)
	}
	p, ok := reg.ad.(adapter.Prompter)
	if !ok {
		return Prompter{}, ErrNoPrompter
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return Prompter{}, err
	}
	return Prompter{
		Driver: reg.inst.Driver,
		Models: m.modelsFor(reg, m.availability(ctx, reg)),
		run: func(ctx context.Context, req adapter.PromptRequest) (string, error) {
			return p.Prompt(ctx, env, req)
		},
	}, nil
}
