package claudecode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// fakeHost stands in for the session's host services. RequestPermission fails
// the test if it is ever reached for AskUserQuestion — that generic path is
// exactly the bug.
type fakeHost struct {
	elicit     func(adapter.ElicitationRequest) (adapter.ElicitationResult, error)
	permission func(adapter.PermissionRequest) (adapter.PermissionOutcome, error)
}

func (h *fakeHost) RequestPermission(_ context.Context, req adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	if h.permission == nil {
		return adapter.PermissionOutcome{}, nil
	}
	return h.permission(req)
}

func (h *fakeHost) Elicit(_ context.Context, req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	if h.elicit == nil {
		return adapter.ElicitationResult{}, nil
	}
	return h.elicit(req)
}

func (h *fakeHost) Logf(string, ...any) {}

// decode pulls the {behavior, updatedInput} shape out of handleRequest's result.
func decodePermissionResult(t *testing.T, v any) (string, map[string]any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Behavior     string         `json:"behavior"`
		UpdatedInput map[string]any `json:"updatedInput"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got.Behavior, got.UpdatedInput
}

const askUserQuestionInput = `{
  "questions": [
    {
      "question": "Which colour?",
      "header": "Colour",
      "multiSelect": false,
      "options": [
        {"label": "Red", "description": "the warm one"},
        {"label": "Blue", "description": "the cool one"}
      ]
    }
  ]
}`

// The bug: AskUserQuestion rendered as a generic Allow-Once permission and, once
// allowed, echoed the input back with no answers, so the tool parked and the
// model hung. The fix routes it through a durable elicitation and feeds the
// selection back as `answers`. This asserts the whole round-trip: an elicitation
// is raised (not a permission), and the chosen label lands in updatedInput.answers
// keyed by the question text — the shape the SDK reads to resolve the call.
func TestAskUserQuestionRoutesThroughElicitation(t *testing.T) {
	var raisedElicitation bool
	host := &fakeHost{
		permission: func(adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
			t.Fatal("AskUserQuestion must not raise a generic permission prompt")
			return adapter.PermissionOutcome{}, nil
		},
		elicit: func(req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
			raisedElicitation = true

			// The schema must offer the two options as an enum on the question.
			var schema struct {
				Properties map[string]struct {
					Title              string            `json:"title"`
					Enum               []string          `json:"enum"`
					AllowOther         bool              `json:"x-allowOther"`
					MultiSelect        bool              `json:"x-multiSelect"`
					OptionDescriptions map[string]string `json:"x-optionDescriptions"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(req.Schema, &schema); err != nil {
				t.Fatalf("schema not valid JSON: %v", err)
			}
			field, ok := schema.Properties["q0"]
			if !ok {
				t.Fatalf("expected a q0 property, got %+v", schema.Properties)
			}
			if field.Title != "Which colour?" {
				t.Errorf("question title = %q, want %q", field.Title, "Which colour?")
			}
			if len(field.Enum) != 2 || field.Enum[0] != "Red" || field.Enum[1] != "Blue" {
				t.Errorf("enum = %v, want [Red Blue]", field.Enum)
			}
			// AskUserQuestion always carries a free-text escape and its option
			// rationales; the presenter needs both.
			if !field.AllowOther {
				t.Error("expected x-allowOther on the question field")
			}
			if field.OptionDescriptions["Red"] != "the warm one" {
				t.Errorf("x-optionDescriptions = %v, want Red->'the warm one'", field.OptionDescriptions)
			}

			return adapter.ElicitationResult{
				Action: "accept",
				Value:  json.RawMessage(`{"q0": "Blue"}`),
			}, nil
		},
	}
	s := &session{host: host}

	params, _ := json.Marshal(map[string]any{
		"toolName": "AskUserQuestion",
		"input":    json.RawMessage(askUserQuestionInput),
	})
	res, err := s.handleRequest(context.Background(), "permission", params)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}
	if !raisedElicitation {
		t.Fatal("no elicitation was raised")
	}

	behavior, updated := decodePermissionResult(t, res)
	if behavior != "allow" {
		t.Fatalf("behavior = %q, want allow", behavior)
	}
	answers, ok := updated["answers"].(map[string]any)
	if !ok {
		t.Fatalf("updatedInput.answers missing or wrong type: %#v", updated["answers"])
	}
	if answers["Which colour?"] != "Blue" {
		t.Errorf("answer = %v, want Blue (keyed by question text)", answers["Which colour?"])
	}
	// The original questions must survive so the tool still has its full input.
	if _, ok := updated["questions"]; !ok {
		t.Error("updatedInput dropped the original questions field")
	}
}

const multiSelectInput = `{
  "questions": [
    {
      "question": "Which features?",
      "header": "Features",
      "multiSelect": true,
      "options": [
        {"label": "Caching", "description": ""},
        {"label": "Metrics", "description": ""},
        {"label": "Tracing", "description": ""}
      ]
    }
  ]
}`

// A multi-select question flags x-multiSelect and its several chosen labels come
// back as an array, which must be joined comma-separated — the tool's answer
// contract for multi-select.
func TestAskUserQuestionMultiSelectJoinsAnswers(t *testing.T) {
	host := &fakeHost{
		elicit: func(req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
			var schema struct {
				Properties map[string]struct {
					MultiSelect bool `json:"x-multiSelect"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(req.Schema, &schema); err != nil {
				t.Fatalf("schema not valid JSON: %v", err)
			}
			if !schema.Properties["q0"].MultiSelect {
				t.Error("expected x-multiSelect on the question field")
			}
			return adapter.ElicitationResult{
				Action: "accept",
				Value:  json.RawMessage(`{"q0": ["Caching", "Tracing"]}`),
			}, nil
		},
	}
	s := &session{host: host}

	params, _ := json.Marshal(map[string]any{
		"toolName": "AskUserQuestion",
		"input":    json.RawMessage(multiSelectInput),
	})
	res, err := s.handleRequest(context.Background(), "permission", params)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}
	behavior, updated := decodePermissionResult(t, res)
	if behavior != "allow" {
		t.Fatalf("behavior = %q, want allow", behavior)
	}
	answers, _ := updated["answers"].(map[string]any)
	if answers["Which features?"] != "Caching, Tracing" {
		t.Errorf("answer = %v, want 'Caching, Tracing'", answers["Which features?"])
	}
}

// A declined elicitation still resolves the tool — with no answers — so the model
// continues instead of hanging.
func TestAskUserQuestionDeclineResolvesWithoutAnswers(t *testing.T) {
	host := &fakeHost{
		elicit: func(adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
			return adapter.ElicitationResult{Action: "decline"}, nil
		},
	}
	s := &session{host: host}

	params, _ := json.Marshal(map[string]any{
		"toolName": "AskUserQuestion",
		"input":    json.RawMessage(askUserQuestionInput),
	})
	res, err := s.handleRequest(context.Background(), "permission", params)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}
	behavior, updated := decodePermissionResult(t, res)
	if behavior != "allow" {
		t.Fatalf("behavior = %q, want allow (empty answers is the tool's skip path)", behavior)
	}
	answers, ok := updated["answers"].(map[string]any)
	if !ok {
		t.Fatalf("updatedInput.answers missing: %#v", updated["answers"])
	}
	if len(answers) != 0 {
		t.Errorf("answers = %v, want empty", answers)
	}
}

// A permission prompt names the tool call it gates, so the host can record the
// approval on that call's card rather than as a row of its own.
func TestPermissionCarriesToolCallID(t *testing.T) {
	var got adapter.PermissionRequest
	host := &fakeHost{
		permission: func(req adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
			got = req
			return adapter.PermissionOutcome{Outcome: proto.OutcomeAllowOnce}, nil
		},
	}
	s := &session{host: host}

	params, _ := json.Marshal(map[string]any{
		"toolName":  "Bash",
		"toolUseId": "toolu_01ABC",
		"input":     map[string]any{"command": "ls"},
	})
	res, err := s.handleRequest(context.Background(), "permission", params)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}
	if behavior, _ := decodePermissionResult(t, res); behavior != "allow" {
		t.Fatalf("behavior = %q, want allow", behavior)
	}
	if got.ToolCallID != "toolu_01ABC" {
		t.Errorf("ToolCallID = %q, want toolu_01ABC", got.ToolCallID)
	}
}
