package piapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// Models is the built-in fallback: a single "whatever pi picks" row. Pi's real
// list is entirely credential-dependent — get_available_models returns only
// models the instance can actually call — so any static list would be wrong
// for someone; the live ListModels answer replaces this.
func (a *Adapter) Models() []adapter.ModelMeta {
	return []adapter.ModelMeta{
		{
			ID:          "",
			Label:       "Default",
			Description: "Let pi choose from its configured models",
			Default:     true,
		},
	}
}

// piModel is the subset of pi's Model shape the catalogue needs.
type piModel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Reasoning bool   `json:"reasoning"`
	// ThinkingLevelMap maps a thinking level to the provider's own value, with
	// an explicit null meaning "this model does not support that level".
	ThinkingLevelMap map[string]json.RawMessage `json:"thinkingLevelMap"`
}

// ListModels spawns a throwaway `pi --mode rpc --no-session` under the
// instance's env overlay and asks it what it can run right now. The answer is
// authenticated models only, so an instance with no credentials legitimately
// gets an error (and the caller falls back to Models) rather than a list of
// models that would all fail.
func (a *Adapter) ListModels(ctx context.Context, env map[string]string) ([]adapter.ModelMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// --no-session keeps the probe out of pi's session store: a catalogue
	// refresh must not litter the user's session list.
	cmd := exec.CommandContext(ctx, a.Bin, "--mode", "rpc", "--no-session")
	cmd.Env = adapter.MergeEnv(os.Environ(), env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", a.Bin, err)
	}
	// Reap on every path: an early return must not leave a live pi behind.
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Both questions go out up front; the loop below collects the two
	// id-correlated answers and ignores everything else on the stream.
	// get_state names the model pi would use, which is the row marked Default.
	const stateID, modelsID = "state", "models"
	for _, c := range []string{
		fmt.Sprintf(`{"id":%q,"type":"get_state"}`, stateID),
		fmt.Sprintf(`{"id":%q,"type":"get_available_models"}`, modelsID),
	} {
		if _, err := fmt.Fprintln(stdin, c); err != nil {
			return nil, fmt.Errorf("pi refused the model query: %w", err)
		}
	}

	var (
		state struct {
			Model *piModel `json:"model"`
		}
		catalogue struct {
			Models []piModel `json:"models"`
		}
		haveState, haveModels bool
	)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for !(haveState && haveModels) && sc.Scan() {
		var res struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Success bool            `json:"success"`
			Error   string          `json:"error"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &res); err != nil || res.Type != "response" {
			continue
		}
		switch res.ID {
		case stateID:
			if !res.Success {
				return nil, fmt.Errorf("pi get_state: %s", res.Error)
			}
			if err := json.Unmarshal(res.Data, &state); err != nil {
				return nil, err
			}
			haveState = true
		case modelsID:
			if !res.Success {
				return nil, fmt.Errorf("pi get_available_models: %s", res.Error)
			}
			if err := json.Unmarshal(res.Data, &catalogue); err != nil {
				return nil, err
			}
			haveModels = true
		}
	}
	if !haveState || !haveModels {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("pi did not answer the model query: %w", ctx.Err())
		}
		return nil, errors.New("pi exited before answering the model query")
	}

	return mapModels(catalogue.Models, state.Model)
}

// mapModels turns pi's catalogue into ModelMeta rows. Separated from the
// process plumbing so the mapping is unit-testable on canned data.
func mapModels(models []piModel, current *piModel) ([]adapter.ModelMeta, error) {
	if len(models) == 0 {
		// No credentials means no models: an honest error the caller turns
		// into the static fallback, not an empty dropdown.
		return nil, errors.New("pi reports no available models; sign in to a provider first")
	}
	out := make([]adapter.ModelMeta, 0, len(models))
	for _, m := range models {
		out = append(out, adapter.ModelMeta{
			// Provider ids never contain a slash, so provider/id is losslessly
			// split back apart by SetModel.
			ID:      m.Provider + "/" + m.ID,
			Label:   m.Name,
			Default: current != nil && m.Provider == current.Provider && m.ID == current.ID,
			Efforts: supportedThinkingLevels(m),
		})
	}
	return out, nil
}

// thinkingLevels is pi's full ladder, most modest first, matching its
// ThinkingLevel type.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// supportedThinkingLevels is a Go port of pi's getSupportedThinkingLevels: a
// non-reasoning model supports only "off"; a reasoning model supports every
// level except those its thinkingLevelMap explicitly nulls out, with the two
// extended levels (xhigh, max) opt-in — offered only when explicitly mapped.
func supportedThinkingLevels(m piModel) []string {
	if !m.Reasoning {
		return []string{"off"}
	}
	out := make([]string, 0, len(thinkingLevels))
	for _, level := range thinkingLevels {
		mapped, present := m.ThinkingLevelMap[level]
		if present && string(mapped) == "null" {
			continue
		}
		if (level == "xhigh" || level == "max") && !present {
			continue
		}
		out = append(out, level)
	}
	return out
}
