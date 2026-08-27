package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// modelListTimeout bounds the listing run. Asking costs a Claude Code start,
// so it is not fast; it is also not on any interactive path — the caller
// serves a cached or fallback list while this runs.
const modelListTimeout = 60 * time.Second

// Models is the fallback list, used only until a live answer arrives or when
// the harness cannot be asked. The current models are family aliases and
// nothing else: no versions, because claiming a version we did not read from
// the harness is how the old hardcoded list went stale. The curated legacy
// group is appended — those ids are verified to run, so a pending or failed
// discovery should not be the reason they cannot be picked.
//
// The default row carries no id at all. That is deliberate: an empty model
// means "whatever the harness picks", which is the only default that cannot be
// wrong — and it says so, rather than being an unexplained "Default".
func (a *Adapter) Models() []adapter.ModelMeta {
	base := []adapter.ModelMeta{
		{ID: "", Label: "Default", Version: "chosen by Claude Code", Description: "No model is named, so the harness starts whatever it is set to use", Default: true},
		{ID: "fable", Label: "Fable"},
		{ID: "opus", Label: "Opus"},
		{ID: "sonnet", Label: "Sonnet"},
	}
	// The legacy models the harness still runs are offered even before (or
	// without) a live answer: they were hardcoded precisely because they work,
	// so a pending or failed discovery should not hide them.
	return append(base, legacyModels...)
}

// legacyModels are older Claude models the installed Claude Code still accepts
// and runs — verified live: `claude-opus-4-8` starts and reports a 1M window,
// `claude-sonnet-4-5` a 200k one — but no longer advertises in supportedModels().
// They are the one piece of model data omniplex hardcodes, offered folded away under
// a "Legacy" group rather than first. Reviewed against claude CLI 2.1.238.
//
// Version is left empty on purpose: setting it equal to Label is what made the
// picker render the same name twice. Resolves is the id itself, because that is
// exactly what the harness reports back for these, so a session running one
// resolves to its row instead of showing a raw id. Efforts are left empty — the
// effort control simply does not appear for a legacy pick rather than offering
// levels an old model might reject.
var legacyModels = []adapter.ModelMeta{
	{ID: "claude-opus-4-8", Label: "Opus 4.8", Resolves: "claude-opus-4-8", Description: "The previous Opus generation.", Group: adapter.GroupLegacy},
	{ID: "claude-opus-4-7", Label: "Opus 4.7", Resolves: "claude-opus-4-7", Description: "An older Opus generation.", Group: adapter.GroupLegacy},
	{ID: "claude-opus-4-6", Label: "Opus 4.6", Resolves: "claude-opus-4-6", Description: "An older Opus generation.", Group: adapter.GroupLegacy},
	{ID: "claude-sonnet-4-5", Label: "Sonnet 4.5", Resolves: "claude-sonnet-4-5", Description: "The previous Sonnet generation.", Group: adapter.GroupLegacy},
}

// modelInfo is the SDK's ModelInfo, as the sidecar relays it.
type modelInfo struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// ListModels asks the installed Claude Code what it offers, by running the
// bridge in its one-shot listing mode. The answer is the SDK's own, so a new
// model or a renamed one appears in omniplex without a release.
func (a *Adapter) ListModels(ctx context.Context, env map[string]string) ([]adapter.ModelMeta, error) {
	r, avail := a.resolve(ctx)
	if !avail.OK() {
		return nil, fmt.Errorf("claude is unavailable: %s", avail.Reason)
	}

	blob, err := json.Marshal(sidecarConfig{Op: "models", Cwd: workingDir(), ClaudePath: r.claudePath})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.runtime, append(append([]string{}, r.runtimeArgs...), string(blob))...)
	cmd.Dir = workingDir()
	cmd.Env = append(adapter.MergeEnv(os.Environ(), env), "CLAUDE_CODE_ENTRYPOINT=sdk-ts")
	cmd.Stderr = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude bridge: %w", err)
	}
	// The process is short-lived by design, but a wedged one must not outlive
	// this call: killing it is what the deadline is for.
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	var models []adapter.ModelMeta
	var fatal string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var frame struct {
			Method string `json:"method"`
			Params struct {
				Models  []modelInfo `json:"models"`
				Message string      `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &frame); err != nil {
			continue
		}
		switch frame.Method {
		case "models":
			models = mapClaudeModels(frame.Params.Models)
		case "fatal":
			fatal = frame.Params.Message
		}
		if models != nil || fatal != "" {
			break
		}
	}
	switch {
	case len(models) > 0:
		return models, nil
	case fatal != "":
		return nil, fmt.Errorf("claude model listing failed: %s", firstLine(strings.TrimSpace(fatal)))
	case ctx.Err() != nil:
		return nil, fmt.Errorf("claude model listing: %w", ctx.Err())
	default:
		return nil, fmt.Errorf("claude model listing returned nothing")
	}
}

// mapClaudeModels turns the SDK's rows into omniplex's, curated for the picker.
//
// The presentation calls made here, none of which the harness list does itself:
//   - Haiku is dropped — it is a quick-answer model, not a coding one.
//   - Every alias that resolves to one concrete model collapses into a single
//     row (the "default" and "opus[1m]" aliases both name Opus 5), labelled by
//     its generation ("Opus 5", "Fable 5", "Sonnet 5") rather than repeating
//     the name twice.
//   - The row's id is the bare concrete model — no "[1m]" tag — so a session
//     defaults to the 200k window; the 1M variant is chosen separately (the
//     adapter turns the tag into the context-window env at start). The bare id
//     is also exactly what the harness reports back, so the running model
//     resolves to its row instead of a raw label.
//   - Rows are ordered by strength (Fable, Opus, Sonnet) and the curated legacy
//     group is appended, folded away.
func mapClaudeModels(in []modelInfo) []adapter.ModelMeta {
	if len(in) == 0 {
		return nil
	}
	current := make([]adapter.ModelMeta, 0, len(in))
	byModel := map[string]int{}
	// byAlias remembers which input row a kept row came from.
	byAlias := map[string]int{}
	for idx, m := range in {
		if m.Value == "" || isHaiku(m) {
			continue
		}
		bare := stripContextTag(m.ResolvedModel)
		if bare == "" {
			bare = m.Value
		}
		version, description := splitDescription(m.Description)
		row := adapter.ModelMeta{
			ID:          bare,
			Label:       generationLabel(version, m.DisplayName),
			Description: description,
			Resolves:    bare,
			// The SDK has no isDefault flag; the row it *calls* "default" is
			// the one Claude Code itself would pick.
			Default: m.Value == "default",
			Efforts: m.SupportedEffortLevels,
		}
		if i, seen := byModel[bare]; seen {
			// Same concrete model under another alias: keep one row, preserving
			// the recommended flag and a real (non-"default") label.
			// Same concrete model under another alias: keep one row. The
			// "default" alias describes itself ("Use the default model
			// (currently …)") rather than the model, so a named alias for the
			// same model replaces it wholesale, keeping only the recommended
			// flag.
			kept := current[i]
			if in[byAlias[bare]].Value == "default" && m.Value != "default" {
				row.Default = true
				current[i] = row
				byAlias[bare] = idx
				continue
			}
			kept.Default = kept.Default || row.Default
			if kept.Label == "" || strings.EqualFold(kept.Label, "default") {
				kept.Label = row.Label
			}
			current[i] = kept
			continue
		}
		byAlias[bare] = idx
		byModel[bare] = len(current)
		current = append(current, row)
	}
	sortByStrength(current)

	out := make([]adapter.ModelMeta, 0, len(current)+len(legacyModels))
	out = append(out, current...)
	out = append(out, legacyModels...)
	return out
}

// generationLabel is the clean one-line name for a current model: the
// generation the harness names in its description ("Opus 5 with 1M context" ->
// "Opus 5", "Fable 5" -> "Fable 5"), falling back to the display name when the
// description carries no generation.
func generationLabel(version, display string) string {
	if version == "" {
		return display
	}
	if before, _, found := strings.Cut(version, " with "); found {
		return strings.TrimSpace(before)
	}
	return version
}

// isHaiku spots the quick-answer model by either alias or resolved id, so it is
// dropped however the harness names it.
func isHaiku(m modelInfo) bool {
	return strings.Contains(m.Value, "haiku") || strings.Contains(m.ResolvedModel, "haiku")
}

// stripContextTag drops a trailing context-window tag like "[1m]" from a model
// id, so "claude-opus-5[1m]" and the bare "claude-opus-5" the harness reports
// mid-session are treated as the same model.
func stripContextTag(id string) string {
	if i := strings.LastIndex(id, "["); i >= 0 && strings.HasSuffix(id, "]") {
		return id[:i]
	}
	return id
}

// sortByStrength orders the current models Fable, then Opus, then Sonnet, then
// anything unrecognised — the frontier order the picker shows top to bottom.
// It is stable, so two rows of one family keep the harness's order.
func sortByStrength(models []adapter.ModelMeta) {
	rank := func(m adapter.ModelMeta) int {
		id := m.Resolves + " " + m.ID
		switch {
		case strings.Contains(id, "fable"):
			return 0
		case strings.Contains(id, "opus"):
			return 1
		case strings.Contains(id, "sonnet"):
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(models, func(i, j int) bool { return rank(models[i]) < rank(models[j]) })
}

// splitDescription separates the generation from the summary. A description
// with no separator is all summary: inventing a version from a sentence would
// be a guess.
func splitDescription(s string) (version, description string) {
	before, after, found := strings.Cut(s, " · ")
	if !found {
		return "", strings.TrimSpace(s)
	}
	return strings.TrimSpace(before), strings.TrimSpace(after)
}

// workingDir gives the listing run somewhere to be. The answer does not depend
// on the directory, so any real one will do — but the SDK insists on one.
func workingDir() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return os.TempDir()
}
