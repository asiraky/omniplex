package claudecode

import (
	"errors"
	"strings"
	"testing"
)

func TestPromptResult(t *testing.T) {
	exit1 := errors.New("exit status 1")
	cases := []struct {
		name   string
		out    string
		runErr error
		stderr string
		want   string
		err    string
	}{
		{"an answer", `{"type":"result","subtype":"success","is_error":false,"result":"Said."}`, nil, "", "Said.", ""},
		{"an error reported in the result", `{"subtype":"success","is_error":true,"result":"There's an issue with the selected model."}`, exit1, "", "", "issue with the selected model"},
		{"a run cut short", `{"subtype":"error_max_turns","is_error":false,"result":""}`, nil, "", "", "error_max_turns"},
		{"nothing printed falls back to stderr", ``, exit1, "not logged in\n", "", "not logged in"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := promptResult([]byte(c.out), c.runErr, c.stderr)
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
