package piapp

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/asiraky/omniplex/internal/adapter"
)

// Pi has no MCP client, so the session's stdio MCP servers reach it through
// a Pi extension of ours that speaks MCP and registers each server's tools
// as native Pi tools. See mcpbridge.mjs.
//
//go:embed mcpbridge.mjs
var mcpBridgeScript []byte

var mcpBridgePath = extractor("pi-mcpbridge", mcpBridgeScript)

// mcpBridgeEnv carries the servers to the extension. Their env values can be
// credentials, so they go in pi's environment, never on argv, and the
// extension removes the variable from pi's environment as it reads it.
const mcpBridgeEnv = "OMNIPLEX_PI_MCP"

type bridgedServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// mcpLaunch is what pi needs to run the session's MCP servers: the extension
// flag and the config's assignment, or nothing when no server is one the
// bridge runs. Only stdio servers are bridged; a URL server is skipped.
func mcpLaunch(servers []adapter.MCPServer) (args []string, env string, err error) {
	var bridged []bridgedServer
	for _, m := range servers {
		if m.Command == "" {
			continue
		}
		bridged = append(bridged, bridgedServer{Name: m.Name, Command: m.Command, Args: m.Args, Env: m.Env})
	}
	if len(bridged) == 0 {
		return nil, "", nil
	}
	path, err := mcpBridgePath()
	if err != nil {
		return nil, "", err
	}
	blob, err := json.Marshal(bridged)
	if err != nil {
		return nil, "", fmt.Errorf("encode MCP servers for pi: %w", err)
	}
	return []string{"--extension", path}, mcpBridgeEnv + "=" + string(blob), nil
}
