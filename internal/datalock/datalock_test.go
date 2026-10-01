package datalock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The lock has to hold across processes, which is the only case it exists
// for, so the second taker is a child process.
func TestSecondProcessIsTurnedAway(t *testing.T) {
	if os.Getenv("DATALOCK_CHILD") != "" {
		_, err := Acquire(os.Getenv("DATALOCK_CHILD"))
		if errors.Is(err, ErrHeld) {
			os.Exit(7)
		}
		if err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	db := filepath.Join(t.TempDir(), "omniplex.db")
	child := func() int {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSecondProcessIsTurnedAway$")
		cmd.Env = append(os.Environ(), "DATALOCK_CHILD="+db)
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	}

	l, err := Acquire(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := child(); got != 7 {
		t.Fatalf("second process while held: exit %d, want 7 (ErrHeld)", got)
	}
	l.Release()
	if got := child(); got != 0 {
		t.Fatalf("second process after release: exit %d, want 0", got)
	}
}
