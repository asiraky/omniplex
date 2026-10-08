package thread

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/asiraky/omniplex/internal/proto"
)

// A card is a proposal the agent raised through omniplex's own tools: an MCP
// server, a skill, a sign-in. It is an elicitation the server holds rather
// than the harness, so it is not tied to the turn or the harness process: a
// stopped turn leaves it answerable, and so does a harness that has exited.
// What the user decides is applied by the server, which then resolves it here.

// RedactToolInput, when set, rewrites a tool call's input before it is
// stored, so a secret the agent passed to a tool never reaches the log. It
// gets the tool's name as the harness reports it. Nil stores inputs as they
// are.
var RedactToolInput func(toolName string, input json.RawMessage) json.RawMessage

// ErrNoCard is answering a card that is not waiting: answered already, or
// never raised on this thread.
var ErrNoCard = errors.New("already answered")

type cardCmd struct {
	requestID string
	prompt    string
	card      json.RawMessage
	action    string // resolve: accept | decline | cancel
	outcome   json.RawMessage
}

// RaiseCard puts a card on the thread under requestID, which the caller
// chooses so it can hold the card before anyone can answer it. prompt is a
// one-line summary. The card must not carry a secret value.
func (a *Actor) RaiseCard(ctx context.Context, requestID, prompt string, card json.RawMessage) error {
	_, err := a.call(ctx, command{kind: cmdRaiseCard, card: &cardCmd{requestID: requestID, prompt: prompt, card: card}})
	return err
}

// ResolveCard records what became of a card that is still waiting: action
// accept, decline or cancel, and its outcome. ErrNoCard when it is not
// waiting. It works on a thread whose harness is not running.
func (a *Actor) ResolveCard(ctx context.Context, requestID, action string, outcome json.RawMessage) error {
	_, err := a.call(ctx, command{kind: cmdResolveCard, card: &cardCmd{requestID: requestID, action: action, outcome: outcome}})
	return err
}

func (a *Actor) handleCard(c command) (any, error) {
	if c.card == nil || c.card.requestID == "" {
		return nil, errors.New("a card needs a request id")
	}
	switch c.kind {
	case cmdRaiseCard:
		if a.state.Closed {
			return nil, ErrClosed
		}
		for _, p := range a.state.Elicitations {
			if p.RequestID == c.card.requestID {
				return nil, errors.New("that card is already raised")
			}
		}
		return nil, a.append(proto.Emit(proto.ElicitationRequested, proto.ElicitationRequestedPayload{
			RequestID: c.card.requestID, TurnID: a.turnActive, Prompt: c.card.prompt, Card: c.card.card,
		}))
	case cmdResolveCard:
		waiting := false
		for _, p := range a.state.Elicitations {
			if p.RequestID == c.card.requestID && p.IsCard() {
				waiting = true
				break
			}
		}
		if !waiting {
			return nil, ErrNoCard
		}
		return nil, a.append(proto.Emit(proto.ElicitationResolved, proto.ElicitationResolvedPayload{
			RequestID: c.card.requestID, Action: c.card.action, Value: c.card.outcome,
		}))
	}
	return nil, nil
}

// liveCard reports whether the server still holds a card.
func (a *Actor) liveCard(requestID string) bool {
	a.mu.Lock()
	live := a.cardLive
	a.mu.Unlock()
	return live != nil && live(requestID)
}

// SetCardLive tells the manager how to ask whether the server still holds a
// card. Resuming a thread's harness cancels the cards it does not hold:
// those a restart lost.
func (m *Manager) SetCardLive(live func(requestID string) bool) {
	m.mu.Lock()
	m.cardLive = live
	m.mu.Unlock()
}

func (m *Manager) liveCard(requestID string) bool {
	m.mu.RLock()
	live := m.cardLive
	m.mu.RUnlock()
	return live != nil && live(requestID)
}

// redact applies RedactToolInput to the inputs of tool calls and of the
// permission asked for one, the places a tool's arguments are stored.
func (a *Actor) redact(em proto.Emission) proto.Emission {
	redact := RedactToolInput
	if redact == nil {
		return em
	}
	switch p := em.Payload.(type) {
	case proto.ToolCallStartedPayload:
		if len(p.RawInput) > 0 {
			p.RawInput = redact(p.Title, p.RawInput)
			em.Payload = p
		}
	case proto.ToolCallUpdatedPayload:
		if len(p.RawInput) > 0 {
			name := p.Title
			if it, ok := a.state.ItemByID(p.ToolCallID); ok && it.Title != "" {
				// The name the call started under: an update's title can be
				// a summary of its input.
				name = it.Title
			}
			p.RawInput = redact(name, p.RawInput)
			em.Payload = p
		}
	case proto.PermissionRequestedPayload:
		if len(p.RawInput) > 0 {
			name := p.ToolName
			if name == "" {
				name = p.Title
			}
			p.RawInput = redact(name, p.RawInput)
			em.Payload = p
		}
	}
	return em
}

// Driver is a harness this build drives, by id and display name.
type Driver struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Drivers lists the harnesses this build drives, in registration order.
func (m *Manager) Drivers() []Driver {
	out := make([]Driver, 0, len(m.driverOrder))
	for _, id := range m.driverOrder {
		out = append(out, Driver{ID: id, Name: m.drivers[id].Meta().Name})
	}
	return out
}
