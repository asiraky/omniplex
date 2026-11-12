package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
)

// modelListTimeout bounds the listing run. It starts an app-server and asks it
// one question, so it is quick — but it is still a process start, and nothing
// interactive waits on it.
const modelListTimeout = 20 * time.Second

// Models is the fallback list, used only until a live answer arrives or when
// the CLI cannot be asked. Codex has no aliases — every id names one released
// model — so naming any of them here would be guessing at what the installed
// CLI serves, and sending a guess is how a session fails to start. The one row
// therefore has no id: an empty model is "whatever Codex picks", which no
// version of the CLI can reject, and it says as much. It carries no Version,
// because the picker's trigger runs label and version together.
func (a *Adapter) Models() []adapter.ModelMeta {
	return []adapter.ModelMeta{
		{ID: "", Label: "Codex's default", Description: "Codex has not said which model yet, so it starts whatever it is set to use", Default: true},
	}
}

// codexModel is one row of the app-server's model/list response. Only the
// fields omniplex presents are named; the rest of the payload is ignored rather than
// mirrored, so a Codex release adding a field changes nothing here.
type codexModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	IsDefault   bool   `json:"isDefault"`
	Efforts     []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

// ListModels asks the installed Codex CLI what it offers. model/list answers
// straight after initialize — no thread, no turn — so this costs one short
// app-server run and never touches a live session.
func (a *Adapter) ListModels(ctx context.Context, env map[string]string) ([]adapter.ModelMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.Bin, "app-server")
	cmd.Env = adapter.MergeEnv(os.Environ(), env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", a.Bin, err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Notifications arrive unasked on this connection (remote-control status,
	// for one); they are dropped rather than handled, since the only thing
	// wanted here is the reply to model/list.
	conn := jsonrpc.NewConn(stdout, stdin,
		func(context.Context, string, json.RawMessage) (any, error) {
			return nil, fmt.Errorf("this connection only lists models")
		},
		func(string, json.RawMessage) {},
		nil,
	)

	if err := conn.Call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "omniplex", "version": "0.1.0"},
		"capabilities": map[string]any{},
	}, nil); err != nil {
		return nil, fmt.Errorf("codex initialize: %w", err)
	}
	if err := conn.Notify("initialized", map[string]any{}); err != nil {
		return nil, err
	}

	// The response is paginated. Codex returns every model in one page today,
	// but following the cursor costs nothing and stops a longer catalogue from
	// being silently truncated.
	var rows []codexModel
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data       []codexModel `json:"data"`
			NextCursor string       `json:"nextCursor"`
		}
		if err := conn.Call(ctx, "model/list", params, &res); err != nil {
			return nil, fmt.Errorf("codex model/list: %w", err)
		}
		rows = append(rows, res.Data...)
		// Stop on the end of the catalogue, an empty page, or a cursor that
		// has not moved: a server that keeps handing back the same cursor
		// would otherwise spin until the deadline, appending as it went.
		if res.NextCursor == "" || res.NextCursor == cursor || len(res.Data) == 0 {
			break
		}
		cursor = res.NextCursor
	}

	models := mapCodexModels(rows)
	if len(models) == 0 {
		return nil, fmt.Errorf("codex listed no models")
	}
	return models, nil
}

// mapCodexModels turns Codex's rows into omniplex's and decides the legacy split.
//
// Codex marks nothing as superseded — `hidden` is false and `upgrade` null for
// every row — so the split is omniplex's presentation call: the newest generation is
// current and everything below it is legacy. Deriving it from the ids rather
// than listing names means a 5.7 release re-sorts itself.
func mapCodexModels(in []codexModel) []adapter.ModelMeta {
	var newest generationNo
	for _, m := range in {
		if m.Hidden {
			continue
		}
		if g := generationOf(m.ID); g.newerThan(newest) {
			newest = g
		}
	}

	out := make([]adapter.ModelMeta, 0, len(in))
	for _, m := range in {
		// A hidden model is one Codex does not want offered; honour that
		// rather than second-guessing it.
		if m.Hidden || m.ID == "" {
			continue
		}
		label := m.DisplayName
		if label == "" {
			label = m.ID
		}
		meta := adapter.ModelMeta{
			ID:          m.ID,
			Label:       label,
			Description: m.Description,
			Default:     m.IsDefault,
		}
		if g := generationOf(m.ID); g.known() {
			meta.Version = g.text
			if newest.newerThan(g) {
				meta.Group = adapter.GroupLegacy
			}
		}
		for _, e := range m.Efforts {
			if e.ReasoningEffort != "" {
				meta.Efforts = append(meta.Efforts, e.ReasoningEffort)
			}
		}
		out = append(out, meta)
	}
	return out
}

// generationNo is a model id's version, kept as its parts rather than as a
// number: 5.10 is newer than 5.6, which is exactly what parsing it as a float
// would get wrong.
type generationNo struct {
	major, minor int
	text         string
}

func (g generationNo) known() bool { return g.text != "" }

func (g generationNo) newerThan(other generationNo) bool {
	if !g.known() {
		return false
	}
	if !other.known() {
		return true
	}
	if g.major != other.major {
		return g.major > other.major
	}
	return g.minor > other.minor
}

// generationPattern picks the version out of a model id: gpt-5.6-sol → 5.6.
var generationPattern = regexp.MustCompile(`(\d+)(?:\.(\d+))?`)

// generationOf reads a model id's version. An id carrying none is never sorted
// into legacy: there is nothing to compare it against.
func generationOf(id string) generationNo {
	m := generationPattern.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return generationNo{}
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return generationNo{}
	}
	minor := 0
	if m[2] != "" {
		if minor, err = strconv.Atoi(m[2]); err != nil {
			return generationNo{}
		}
	}
	return generationNo{major: major, minor: minor, text: m[0]}
}
