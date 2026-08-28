package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

func TestCancelFinishesTurnWhenBridgeConfirmsInterrupt(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	server := jsonrpc.NewConn(serverRead, serverWrite, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method != "interrupt" {
			t.Fatalf("bridge received %q, want interrupt", method)
		}
		return map[string]any{}, nil
	}, nil)
	client := jsonrpc.NewConn(clientRead, clientWrite, nil, nil)
	t.Cleanup(func() {
		_ = clientWrite.Close()
		_ = serverWrite.Close()
		<-server.Done()
	})

	s := &session{
		conn:    client,
		events:  make(chan proto.Emission, 1),
		done:    make(chan struct{}),
		turnID:  "turn-1",
		streams: map[string]*stream{},
	}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case em := <-s.events:
		p, ok := em.Payload.(proto.TurnFinishedPayload)
		if !ok || em.Type != proto.TurnFinished {
			t.Fatalf("Cancel emitted %s, want turn.finished", em.Type)
		}
		if p.TurnID != "turn-1" || p.StopReason != proto.StopCancelled {
			t.Fatalf("turn finish = %+v, want turn-1 cancelled", p)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed interrupt did not finish the turn")
	}
}

func TestCancelFinishCanRaceBridgeExit(t *testing.T) {
	// The interrupt response and EOF are two adjacent bridge frames. Exercise
	// their two goroutines against each other under the race detector: send
	// after close must be ignored, never panic.
	for range 1000 {
		s := &session{events: make(chan proto.Emission, 1), done: make(chan struct{})}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: "turn-1", StopReason: proto.StopCancelled,
			}))
		}()
		go func() {
			defer wg.Done()
			<-start
			s.closeEvents()
		}()
		close(start)
		wg.Wait()
	}
}

func TestSidecarAcknowledgesInterruptAfterSDK(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	data, err := sidecarFS.ReadFile("sidecar/sidecar.mjs")
	if err != nil {
		t.Fatal(err)
	}
	stubbed := strings.Replace(string(data),
		`import { query } from "@anthropic-ai/claude-agent-sdk";`,
		`const query = () => ({
  async *[Symbol.asyncIterator]() { await new Promise(() => {}); },
  interrupt: async () => {},
});`, 1)
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

	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":7,"method":"interrupt","params":{}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Text()
		}
	}()
	select {
	case raw := <-line:
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatalf("interrupt response is not JSON: %q", raw)
		}
		if response.ID != 7 || len(response.Result) == 0 {
			t.Fatalf("interrupt response = %s, want result for request 7", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sidecar did not acknowledge the interrupt")
	}
}
