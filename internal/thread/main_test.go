package thread

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives the package a throwaway home folder. A project with no home
// gets one made in <home>/Omniplex on first use, and the user config is read
// from <home>/.omniplex, so without this every run left p1-N and compat-N
// folders in the real projects folder and read the real settings.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "omniplex-thread-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Unsetenv("OMNIPLEX_CONFIG")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
