package piapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The whole point of the walk: a server started outside a login shell has no
// nvm bin directory on its PATH, and that is where npm puts pi.
func TestFindPiLooksInNvmWhenPathIsBare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, "empty"))
	t.Setenv("OMNIPLEX_PI_PATH", "")
	want := filepath.Join(home, ".nvm", "versions", "node", "v24.19.0", "bin", "pi")
	writeExecutable(t, want)

	got, ok := New("").findPi()
	if !ok || got != want {
		t.Fatalf("findPi() = %q, %v; want %q", got, ok, want)
	}
}

// String ordering would put v9 above v24 and launch pi under a node that
// predates it.
func TestFindPiPrefersNewestNodeVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, "empty"))
	t.Setenv("OMNIPLEX_PI_PATH", "")
	newest := filepath.Join(home, ".nvm", "versions", "node", "v24.19.0", "bin", "pi")
	writeExecutable(t, newest)
	writeExecutable(t, filepath.Join(home, ".nvm", "versions", "node", "v9.1.0", "bin", "pi"))

	got, _ := New("").findPi()
	if got != newest {
		t.Errorf("findPi() = %q; want the newest node's pi %q", got, newest)
	}
}

// An explicitly configured path that is wrong is an error, not a hint to go
// looking for some other pi.
func TestFindPiExplicitPathIsNotSecondGuessed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeExecutable(t, filepath.Join(home, ".nvm", "versions", "node", "v24.19.0", "bin", "pi"))

	if _, ok := (&Adapter{Bin: filepath.Join(home, "nowhere", "pi")}).findPi(); ok {
		t.Error("a configured path that does not exist must not fall back to a discovered install")
	}
}

// pi's shebang is `#!/usr/bin/env node`, so a pi found off the PATH would
// start only to have the kernel fail on a node that is off the PATH too.
func TestWithBinDirPrependsSiblingDirectory(t *testing.T) {
	env := withBinDir("/opt/node/v24/bin/pi", []string{"HOME=/home/x", "PATH=/usr/bin:/bin"})
	var path string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			path = kv
		}
	}
	if path != "PATH=/opt/node/v24/bin:/usr/bin:/bin" {
		t.Errorf("PATH = %q; want the pi directory first", path)
	}
}

func TestWithBinDirAddsPathWhenAbsent(t *testing.T) {
	env := withBinDir("/opt/node/v24/bin/pi", []string{"HOME=/home/x"})
	if len(env) != 2 || env[1] != "PATH=/opt/node/v24/bin" {
		t.Errorf("env = %v; want a PATH entry appended", env)
	}
}
