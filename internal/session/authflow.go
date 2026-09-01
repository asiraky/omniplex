package session

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
)

// This file runs structured authentication flows. A flow is one adapter's
// BeginAuth call, narrated to whichever client started it: display events out,
// prompt answers in. Flows are deliberately ephemeral and connection-scoped —
// they are never written to the event log, because their traffic sits next to
// secrets and because an abandoned half-finished OAuth dance is worthless to
// replay. A client that disconnects mid-flow simply starts a new one; the
// durable outcome lives where it belongs, in the harness's own credential
// store, and is re-observed by the next probe.

// authFlowTimeout bounds one flow. OAuth involves a human and a browser, so
// it is generous; without a bound, an abandoned flow would hold its goroutine
// and its harness process forever.
const authFlowTimeout = 15 * time.Minute

// TerminalAuthMethod is the reserved method id for the embedded-terminal
// fallback. Beginning it is a client-side act — opening the login terminal —
// so BeginAuthFlow refuses it rather than running anything.
const TerminalAuthMethod = "terminal"

// AuthFlowEvent is one frame of flow narration, pushed only to the client
// that began the flow. Exactly one of Event, Prompt, or Done is meaningful.
type AuthFlowEvent struct {
	FlowID string `json:"flowId"`
	// Event is a display-only notification.
	Event *adapter.AuthEvent `json:"event,omitempty"`
	// Prompt asks the user for a value; the client answers with
	// RespondAuthFlow naming PromptID. A secret prompt's answer must never be
	// echoed anywhere.
	Prompt *AuthFlowPrompt `json:"prompt,omitempty"`
	// Done marks the end of the flow; Err says how it failed, empty on
	// success.
	Done bool   `json:"done,omitempty"`
	Err  string `json:"error,omitempty"`
}

type AuthFlowPrompt struct {
	ID string `json:"id"`
	adapter.AuthPrompt
}

type authFlow struct {
	id         string
	instanceID string
	cancel     context.CancelFunc
	events     chan AuthFlowEvent

	mu      sync.Mutex
	pending map[string]chan string
	seq     int
}

// interaction adapts one flow to adapter.AuthInteraction.
type interaction struct{ f *authFlow }

func (ia interaction) Notify(ev adapter.AuthEvent) {
	e := ev
	ia.f.events <- AuthFlowEvent{FlowID: ia.f.id, Event: &e}
}

func (ia interaction) Prompt(ctx context.Context, p adapter.AuthPrompt) (string, error) {
	ia.f.mu.Lock()
	ia.f.seq++
	id := strconv.Itoa(ia.f.seq)
	answer := make(chan string, 1)
	ia.f.pending[id] = answer
	ia.f.mu.Unlock()
	defer func() {
		ia.f.mu.Lock()
		delete(ia.f.pending, id)
		ia.f.mu.Unlock()
	}()

	ia.f.events <- AuthFlowEvent{FlowID: ia.f.id, Prompt: &AuthFlowPrompt{ID: id, AuthPrompt: p}}
	select {
	case v := <-answer:
		return v, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// BeginAuthFlow starts one method's authentication flow for one instance and
// returns the channel its narration arrives on. The channel closes when the
// flow ends, after a final Done event. The caller owns delivery: if it stops
// reading, it must cancel the flow, or the flow's goroutine would block on
// send.
func (m *Manager) BeginAuthFlow(instanceID, methodID string) (string, <-chan AuthFlowEvent, error) {
	if methodID == TerminalAuthMethod {
		return "", nil, fmt.Errorf("the terminal method runs in the login terminal, not a structured flow")
	}
	reg, ok := m.lookup(instanceID)
	if !ok || reg.ad == nil {
		return "", nil, fmt.Errorf("unknown provider instance %q", instanceID)
	}
	flows, ok := reg.ad.(adapter.AuthFlows)
	if !ok {
		return "", nil, fmt.Errorf("%s has no structured sign-in", reg.inst.DisplayName)
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return "", nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), authFlowTimeout)
	f := &authFlow{
		id:         uuid.NewString(),
		instanceID: instanceID,
		cancel:     cancel,
		events:     make(chan AuthFlowEvent, 16),
		pending:    map[string]chan string{},
	}
	m.authMu.Lock()
	m.authFlows[f.id] = f
	m.authMu.Unlock()

	go func() {
		defer close(f.events)
		defer func() {
			m.authMu.Lock()
			delete(m.authFlows, f.id)
			m.authMu.Unlock()
			cancel()
		}()
		err := flows.BeginAuth(ctx, env, methodID, interaction{f})
		if err == nil {
			// The harness now holds (or has revoked) a credential the cached
			// probe knows nothing about; models may differ under the new
			// identity too.
			m.forgetInstance(instanceID)
		}
		done := AuthFlowEvent{FlowID: f.id, Done: true}
		if err != nil {
			done.Err = err.Error()
		}
		// The final event must not block forever if the client is gone; the
		// deferred close is what actually ends the pump.
		select {
		case f.events <- done:
		case <-time.After(5 * time.Second):
		}
	}()
	return f.id, f.events, nil
}

// RespondAuthFlow delivers a prompt answer into a running flow. The value is
// handed to the adapter and forgotten; it never appears in a result, an
// event, or a log line.
func (m *Manager) RespondAuthFlow(flowID, promptID, value string) error {
	m.authMu.Lock()
	f, ok := m.authFlows[flowID]
	m.authMu.Unlock()
	if !ok {
		return fmt.Errorf("no running authentication flow %q", flowID)
	}
	f.mu.Lock()
	answer, ok := f.pending[promptID]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("flow %q is not waiting on prompt %q", flowID, promptID)
	}
	select {
	case answer <- value:
		return nil
	default:
		return fmt.Errorf("prompt %q was already answered", promptID)
	}
}

// CancelAuthFlow abandons a running flow. Unknown ids are fine: the flow may
// have finished on its own in the meantime.
func (m *Manager) CancelAuthFlow(flowID string) {
	m.authMu.Lock()
	f, ok := m.authFlows[flowID]
	m.authMu.Unlock()
	if ok {
		f.cancel()
	}
}

// forgetInstance drops one instance's cached probe and model listing, so the
// next look re-asks under whatever credentials now exist, and tells clients.
func (m *Manager) forgetInstance(instanceID string) {
	m.probeMu.Lock()
	delete(m.probes, instanceID)
	m.probeMu.Unlock()
	m.forgetModels(instanceID)
}

// InstanceAuth is the on-demand authentication picture of one instance: how
// it could sign in and where each method stands. Fetched by command rather
// than carried on every harness listing because answering may spawn the
// harness.
type InstanceAuth struct {
	Methods  []adapter.AuthMethod `json:"methods"`
	Statuses []adapter.AuthStatus `json:"statuses,omitempty"`
}

// AuthOverview answers for one instance. Adapters with structured flows are
// asked live; adapters with only a CLI login get a synthesised terminal
// method whose status is read from the cached probe, which already knows
// whether the harness considers itself signed in.
func (m *Manager) AuthOverview(ctx context.Context, instanceID string) (InstanceAuth, error) {
	reg, ok := m.lookup(instanceID)
	if !ok || reg.ad == nil {
		return InstanceAuth{}, fmt.Errorf("unknown provider instance %q", instanceID)
	}
	if flows, ok := reg.ad.(adapter.AuthFlows); ok {
		env, err := m.envFor(reg.inst)
		if err != nil {
			return InstanceAuth{}, err
		}
		methods, err := flows.AuthMethods(ctx, env)
		if err != nil {
			return InstanceAuth{}, err
		}
		statuses, err := flows.AuthStatuses(ctx, env)
		if err != nil {
			// Methods without statuses still let the user connect; say so
			// rather than blanking the whole surface.
			statuses = nil
		}
		out := InstanceAuth{Methods: methods, Statuses: statuses}
		if _, ok := reg.ad.(adapter.Authenticator); ok {
			out.Methods = append(out.Methods, terminalMethod())
		}
		return out, nil
	}
	if _, ok := reg.ad.(adapter.Authenticator); ok {
		avail := m.availability(ctx, reg)
		status := adapter.AuthStatus{MethodID: TerminalAuthMethod, State: adapter.AuthDisconnected}
		if avail.OK() {
			status.State = adapter.AuthConnected
			status.Account = avail.Facts["account"]
			status.Detail = avail.Facts["plan"]
			if avail.Facts["auth"] != "" {
				status.Source = adapter.SourceNative
			}
		}
		return InstanceAuth{
			Methods:  []adapter.AuthMethod{terminalMethod()},
			Statuses: []adapter.AuthStatus{status},
		}, nil
	}
	return InstanceAuth{}, fmt.Errorf("%s has no sign-in", reg.inst.DisplayName)
}

func terminalMethod() adapter.AuthMethod {
	return adapter.AuthMethod{
		ID:          TerminalAuthMethod,
		Label:       "Sign in via terminal",
		Description: "Runs the harness's own sign-in command in an embedded terminal.",
		Kind:        adapter.AuthKindTerminal,
	}
}

// LogoutInstance disconnects one method's credential through the adapter's
// own flow, then re-probes.
func (m *Manager) LogoutInstance(ctx context.Context, instanceID, methodID string) error {
	reg, ok := m.lookup(instanceID)
	if !ok || reg.ad == nil {
		return fmt.Errorf("unknown provider instance %q", instanceID)
	}
	flows, ok := reg.ad.(adapter.AuthFlows)
	if !ok {
		return fmt.Errorf("%s cannot disconnect credentials from here", reg.inst.DisplayName)
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return err
	}
	if err := flows.Logout(ctx, env, methodID); err != nil {
		return err
	}
	m.forgetInstance(instanceID)
	return nil
}
