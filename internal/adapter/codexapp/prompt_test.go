package codexapp

import (
	"strings"
	"testing"
)

func TestExecAnswer(t *testing.T) {
	cases := []struct {
		name, stream, want, err string
	}{
		{"the last message is the answer", `{"type":"thread.started"}
{"type":"item.completed","item":{"type":"agent_message","text":"first"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Said."}}
{"type":"turn.completed"}`, "Said.", ""},
		{"an item error is only a warning", `{"type":"item.completed","item":{"type":"error","message":"no metadata"}}
not json
{"type":"item.completed","item":{"type":"agent_message","text":"Said."}}`, "Said.", ""},
		{"a failed turn is the upstream message", `{"type":"error","message":"{\"error\":{\"message\":\"model not supported\"}}"}
{"type":"turn.failed","error":{"message":"{\"type\":\"error\",\"error\":{\"message\":\"model not supported\"}}"}}`, "", "model not supported"},
		{"no answer", `{"type":"turn.completed"}`, "", "no answer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := execAnswer(strings.NewReader(c.stream))
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want one saying %q", err, c.err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}
