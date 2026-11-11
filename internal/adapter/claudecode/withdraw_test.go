package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

// withdrawHost blocks every prompt until its ctx ends, as the real host does
// while it waits on a human, except the tool named "Answered", which it allows
// at once.
type withdrawHost struct {
	entered  chan string
	released chan string
}

func (h *withdrawHost) RequestPermission(ctx context.Context, req adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	if req.ToolName == "Answered" {
		return adapter.PermissionOutcome{Outcome: proto.OutcomeAllowOnce}, nil
	}
	h.entered <- "permission"
	<-ctx.Done()
	h.released <- "permission"
	return adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}, ctx.Err()
}

func (h *withdrawHost) Elicit(ctx context.Context, _ adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	h.entered <- "elicit"
	<-ctx.Done()
	h.released <- "elicit"
	return adapter.ElicitationResult{Action: "cancel"}, ctx.Err()
}

func (h *withdrawHost) Logf(string, ...any) {}

// A permission the SDK aborted is settled in the bridge, which says so with
// permissionCancelled. That must release the host's wait — or the prompt stays
// up for a question nobody is asking — and no answer may go back for it.
func TestPermissionCancelledReleasesHost(t *testing.T) {
	for _, tc := range []struct {
		tool, input, call string
	}{
		{"Bash", `{"command":"ls"}`, "permission"},
		{"AskUserQuestion", askUserQuestionInput, "elicit"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			var input bytes.Buffer
			if err := json.Compact(&input, []byte(tc.input)); err != nil {
				t.Fatal(err)
			}
			serverRead, bridgeWrite := io.Pipe()
			bridgeRead, serverWrite := io.Pipe()
			host := &withdrawHost{entered: make(chan string, 1), released: make(chan string, 1)}
			s := &session{host: host, streams: map[string]*stream{}}
			s.conn = jsonrpc.NewConn(serverRead, serverWrite, s.handleRequest, s.handleNotification)
			t.Cleanup(func() {
				_ = bridgeWrite.Close()
				_ = serverWrite.Close()
				<-s.conn.Done()
			})

			frames := make(chan map[string]json.RawMessage, 8)
			go func() {
				sc := bufio.NewScanner(bridgeRead)
				for sc.Scan() {
					var f map[string]json.RawMessage
					if json.Unmarshal(sc.Bytes(), &f) == nil {
						frames <- f
					}
				}
			}()
			write := func(frame string) {
				t.Helper()
				if _, err := io.WriteString(bridgeWrite, frame+"\n"); err != nil {
					t.Fatal(err)
				}
			}
			wait := func(ch chan string, what string) {
				t.Helper()
				select {
				case got := <-ch:
					if got != tc.call {
						t.Fatalf("host %s via %s, want %s", what, got, tc.call)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("host was never %s", what)
				}
			}

			write(`{"jsonrpc":"2.0","id":1,"method":"permission","params":{"toolName":"` + tc.tool + `","input":` + input.String() + `}}`)
			wait(host.entered, "asked")
			write(`{"jsonrpc":"2.0","method":"permissionCancelled","params":{"id":1}}`)
			wait(host.released, "released")

			// A later request is answered, and the withdrawn one never is.
			write(`{"jsonrpc":"2.0","id":2,"method":"permission","params":{"toolName":"Answered","input":{}}}`)
			for {
				select {
				case f := <-frames:
					switch string(f["id"]) {
					case "1":
						t.Fatalf("withdrawn request was answered: %v", f)
					case "2":
						return
					}
				case <-time.After(2 * time.Second):
					t.Fatal("the request after the withdrawn one was never answered")
				}
			}
		})
	}
}

// The bridge withdraws a permission the SDK aborts while it is still waiting
// on the host, and only then: one the host already answered is not withdrawn
// when its signal later fires.
func TestSidecarWithdrawsAbortedPermission(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	data, err := sidecarFS.ReadFile("sidecar/sidecar.mjs")
	if err != nil {
		t.Fatal(err)
	}
	// The stub asks twice. The first ask is answered, then its signal fires;
	// the second is left waiting until interrupt aborts it.
	stubbed := strings.Replace(string(data),
		`import { query } from "@anthropic-ai/claude-agent-sdk";`,
		`const query = ({ options }) => {
  const turn = new AbortController();
  return {
    async *[Symbol.asyncIterator]() {
      const answered = new AbortController();
      await options.canUseTool("Read", { file_path: "a" }, { signal: answered.signal, suggestions: [] });
      const waiting = options.canUseTool("Bash", { command: "ls" }, { signal: turn.signal, suggestions: [] });
      answered.abort();
      await waiting;
      await new Promise(() => {});
    },
    interrupt: async () => turn.abort(),
  };
};`, 1)
	if stubbed == string(data) {
		t.Fatal("could not stub the SDK import; the sidecar's import line changed")
	}
	script := filepath.Join(dir, "sidecar.mjs")
	if err := os.WriteFile(script, []byte(stubbed), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGuard(t, dir)

	cfg, _ := json.Marshal(sidecarConfig{Cwd: dir})
	cmd := exec.Command(node, script, string(cfg))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	type frame struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ToolName string          `json:"toolName"`
			ID       json.RawMessage `json:"id"`
		} `json:"params"`
	}
	frames := make(chan frame, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			var f frame
			if json.Unmarshal(sc.Bytes(), &f) == nil {
				frames <- f
			}
		}
	}()
	next := func(want string) frame {
		t.Helper()
		select {
		case f := <-frames:
			return f
		case <-time.After(2 * time.Second):
			t.Fatalf("sidecar sent no %s", want)
			return frame{}
		}
	}
	write := func(line string) {
		t.Helper()
		if _, err := io.WriteString(stdin, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}

	first := next("first permission request")
	if first.Method != "permission" || first.Params.ToolName != "Read" {
		t.Fatalf("first frame = %+v, want the Read permission request", first)
	}
	write(`{"jsonrpc":"2.0","id":` + string(first.ID) + `,"result":{"behavior":"allow","updatedInput":{}}}`)

	second := next("second permission request")
	if second.Method != "permission" || second.Params.ToolName != "Bash" {
		t.Fatalf("second frame = %+v, want the Bash permission request", second)
	}
	write(`{"jsonrpc":"2.0","id":7,"method":"interrupt","params":{}}`)

	// Everything up to the interrupt's answer: the one withdrawal must name
	// the request left waiting, never the one already answered.
	var withdrawn []string
	for {
		f := next("interrupt response")
		if f.Method == "permissionCancelled" {
			withdrawn = append(withdrawn, string(f.Params.ID))
			continue
		}
		if f.Method == "" && string(f.ID) == "7" {
			break
		}
	}
	if len(withdrawn) != 1 || withdrawn[0] != string(second.ID) {
		t.Fatalf("withdrawn = %v, want only %s", withdrawn, second.ID)
	}
}
