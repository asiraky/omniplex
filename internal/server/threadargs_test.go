package server

import (
	"testing"

	"github.com/asiraky/omniplex/internal/ephemeral"
)

func TestThreadOfArgs(t *testing.T) {
	for args, want := range map[string]string{
		``:                                   "",
		`not json`:                           "",
		`{"threadId":"t1"}`:                  "t1",
		`{"threadId":"t1","private":true}`:   "t1",
		`{"private":true,"text":"hi"}`:       ephemeral.Unborn,
		`{"private":false,"projectId":"p1"}`: "",
	} {
		if got := threadOfArgs([]byte(args)); got != want {
			t.Errorf("threadOfArgs(%s) = %q, want %q", args, got, want)
		}
	}
}
