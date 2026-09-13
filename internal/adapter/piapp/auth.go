package piapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
)

// This file implements the structured-auth capability over the Node bridge.
// Pi authenticates per model provider — an OpenRouter key, a Claude
// subscription — and each (provider, auth type) pair becomes one AuthMethod
// with id "provider:type". Credentials live entirely in pi's own auth.json;
// nothing is stored on this side.

// bridgeMethod / bridgeStatus mirror the bridge's result rows.
type bridgeMethod struct {
	Provider     string `json:"provider"`
	Type         string `json:"type"` // api_key | oauth
	Label        string `json:"label"`
	LoginLabel   string `json:"loginLabel"`
	Subscription bool   `json:"subscription"`
}

type bridgeStatus struct {
	Provider  string `json:"provider"`
	Connected bool   `json:"connected"`
	Type      string `json:"type"`   // credential type backing the connection
	Source    string `json:"source"` // pi's prose: "OPENROUTER_API_KEY", "OAuth", …
	Stored    bool   `json:"stored"` // a credential exists in pi's auth.json
}

func methodID(provider, authType string) string { return provider + ":" + authType }

// splitMethodID undoes methodID. The split is on the last colon because
// provider ids are pi's own and only the auth type suffix is ours.
func splitMethodID(id string) (provider, authType string, err error) {
	i := strings.LastIndex(id, ":")
	if i <= 0 || i == len(id)-1 {
		return "", "", fmt.Errorf("malformed pi auth method id %q", id)
	}
	return id[:i], id[i+1:], nil
}

func (a *Adapter) AuthMethods(ctx context.Context, env map[string]string) ([]adapter.AuthMethod, error) {
	var res struct {
		Methods []bridgeMethod `json:"methods"`
	}
	if err := a.runBridge(ctx, env, nil, &res, "methods"); err != nil {
		return nil, err
	}
	out := make([]adapter.AuthMethod, 0, len(res.Methods))
	for _, m := range res.Methods {
		method := adapter.AuthMethod{
			ID:    methodID(m.Provider, m.Type),
			Label: m.Label,
		}
		switch m.Type {
		case "oauth":
			method.Kind = adapter.AuthKindOAuth
			method.Subscription = m.Subscription
			// loginLabel is pi's selector prose ("Sign in with SuperGrok or X
			// Premium"); it reads as a description next to the shorter label.
			method.Description = m.LoginLabel
		default:
			method.Kind = adapter.AuthKindSecret
		}
		out = append(out, method)
	}
	return out, nil
}

func (a *Adapter) AuthStatuses(ctx context.Context, env map[string]string) ([]adapter.AuthStatus, error) {
	var res struct {
		Statuses []bridgeStatus `json:"statuses"`
	}
	if err := a.runBridge(ctx, env, nil, &res, "status"); err != nil {
		return nil, err
	}
	out := make([]adapter.AuthStatus, 0, len(res.Statuses))
	for _, st := range res.Statuses {
		if !st.Connected {
			continue
		}
		// checkAuth answers per provider, naming the credential type that
		// backs the connection — which is exactly one method's id.
		status := adapter.AuthStatus{
			MethodID: methodID(st.Provider, st.Type),
			State:    adapter.AuthConnected,
			Detail:   st.Source,
		}
		// A credential in pi's auth.json is native storage; a connection
		// without one is riding an environment variable (possibly this
		// instance's own overlay). The distinction matters because logout can
		// only revoke the former.
		if st.Stored {
			status.Source = adapter.SourceNative
		} else {
			status.Source = adapter.SourceEnvironment
		}
		out = append(out, status)
	}
	return out, nil
}

func (a *Adapter) BeginAuth(ctx context.Context, env map[string]string, id string, ia adapter.AuthInteraction) error {
	provider, authType, err := splitMethodID(id)
	if err != nil {
		return err
	}
	if authType != "api_key" && authType != "oauth" {
		return fmt.Errorf("pi has no %q auth flow", authType)
	}
	// The bridge returns success only once pi's login() resolved and the
	// credential is in pi's storage, which is the contract BeginAuth promises.
	return a.runBridge(ctx, env, ia, nil, "login", provider, authType)
}

func (a *Adapter) Logout(ctx context.Context, env map[string]string, id string) error {
	provider, _, err := splitMethodID(id)
	if err != nil {
		return err
	}
	// Pi stores one credential per provider, so logout is per provider no
	// matter which of its methods was asked.
	return a.runBridge(ctx, env, nil, nil, "logout", provider)
}
