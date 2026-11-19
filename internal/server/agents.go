package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// agentCommands are the agent page's commands. Their names and shapes are
// web/src/lib/agentApi.ts's.
var agentCommands = map[string]bool{
	"create_agent":        true,
	"save_agent":          true,
	"archive_agent":       true,
	"agent_new_thread":    true,
	"agent_revisions":     true,
	"save_agent_revision": true,
	"agent_preamble":      true,
	"agent_log":           true,
	"agent_compaction":    true,
}

// agentReadCommand reports the agent commands that only read. They skip the
// command ledger like the other reads: a stored answer would be stale, and a
// compaction's bodies have no business in a durable table twice.
func agentReadCommand(name string) bool {
	switch name {
	case "agent_revisions", "agent_preamble", "agent_log", "agent_compaction":
		return true
	}
	return false
}

type agentArgs struct {
	AgentID string `json:"agentId"`
}

type saveAgentArgs struct {
	AgentID string `json:"agentId"`
	thread.AgentPatch
}

type agentNewThreadArgs struct {
	AgentID      string `json:"agentId"`
	FirstMessage string `json:"firstMessage"`
}

type agentRevisionsArgs struct {
	AgentID string `json:"agentId"`
	Kind    string `json:"kind"`
}

type saveAgentRevisionArgs struct {
	AgentID string `json:"agentId"`
	Kind    string `json:"kind"`
	Body    string `json:"body"`
	Reason  string `json:"reason"`
	// BaseRev is the revision the edit started from; the save is refused
	// when another has been saved since. 0 saves whatever the head.
	BaseRev int `json:"baseRev,omitempty"`
}

type agentLogArgs struct {
	AgentID  string `json:"agentId"`
	ThreadID string `json:"threadId"`
}

type agentCompactionArgs struct {
	AgentID string `json:"agentId"`
	ID      int64  `json:"id"`
}

var errNoAgent = errors.New("which agent? agentId is missing")

func needAgent(id string) error {
	if strings.TrimSpace(id) == "" {
		return errNoAgent
	}
	return nil
}

func (s *Server) agentCommand(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	mgr := s.mgr
	switch name {
	case "create_agent":
		a, err := decode[thread.NewAgent](raw)
		if err != nil {
			return nil, err
		}
		ag, start, err := mgr.CreateAgent(ctx, a)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"agent": ag}
		startOut(out, start)
		return out, nil

	case "save_agent":
		a, err := decode[saveAgentArgs](raw)
		if err != nil {
			return nil, err
		}
		if err := needAgent(a.AgentID); err != nil {
			return nil, err
		}
		ag, err := mgr.SaveAgent(ctx, a.AgentID, a.AgentPatch)
		if err != nil {
			return nil, err
		}
		return map[string]any{"agent": ag}, nil

	case "archive_agent":
		a, err := decode[agentArgs](raw)
		if err != nil {
			return nil, err
		}
		if err := needAgent(a.AgentID); err != nil {
			return nil, err
		}
		if err := mgr.ArchiveAgent(ctx, a.AgentID); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "agent_new_thread":
		a, err := decode[agentNewThreadArgs](raw)
		if err != nil {
			return nil, err
		}
		if err := needAgent(a.AgentID); err != nil {
			return nil, err
		}
		start, err := mgr.AgentNewThread(ctx, a.AgentID, a.FirstMessage)
		if err != nil {
			return nil, err
		}
		out := map[string]any{}
		startOut(out, start)
		return out, nil

	case "agent_revisions":
		a, err := decode[agentRevisionsArgs](raw)
		if err != nil {
			return nil, err
		}
		if err := needAgent(a.AgentID); err != nil {
			return nil, err
		}
		revs, err := mgr.AgentRevisions(ctx, a.AgentID, a.Kind)
		if err != nil {
			return nil, err
		}
		if revs == nil {
			revs = []store.AgentRevision{}
		}
		return map[string]any{"revisions": revs}, nil

	case "save_agent_revision":
		a, err := decode[saveAgentRevisionArgs](raw)
		if err != nil {
			return nil, err
		}
		if err := needAgent(a.AgentID); err != nil {
			return nil, err
		}
		// The agent's own edits come through its approved card, never this
		// command: here it is always the user writing.
		rev, restart, err := mgr.SaveAgentRevision(ctx, a.AgentID, a.Kind, a.Body, store.AuthorUser, a.Reason, a.BaseRev)
		if err != nil {
			return nil, err
		}
		return map[string]any{"revision": rev, "restart": restart}, nil

	case "agent_preamble":
		return map[string]any{"text": mgr.AgentPreamble()}, nil

	case "agent_log":
		a, err := decode[agentLogArgs](raw)
		if err != nil {
			return nil, err
		}
		if err := needAgent(a.AgentID); err != nil {
			return nil, err
		}
		threadID, rows, archived, err := mgr.AgentLog(ctx, a.AgentID, a.ThreadID)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"compactions": rows, "archived": archived}
		if threadID != "" {
			out["threadId"] = threadID
		}
		return out, nil

	case "agent_compaction":
		a, err := decode[agentCompactionArgs](raw)
		if err != nil {
			return nil, err
		}
		if err := needAgent(a.AgentID); err != nil {
			return nil, err
		}
		d, err := mgr.AgentCompaction(ctx, a.AgentID, a.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"compaction": d}, nil
	}
	return nil, errors.New("unknown agent command " + name)
}

// startOut adds an agent thread's start to a command's answer: its thread,
// and the error its first message failed with, as create_thread does.
func startOut(out map[string]any, start thread.AgentStart) {
	if start.ThreadID != "" {
		out["threadId"] = start.ThreadID
	}
	if start.PromptError != "" {
		out["promptError"] = start.PromptError
	}
}

func (c *conn) sendAgents() {
	agents, err := c.srv.mgr.Agents(c.ctx)
	if err != nil {
		return
	}
	c.send(serverFrame{Type: "agents", Agents: agents})
}
