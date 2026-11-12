package adapter

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionNumber(t *testing.T) {
	cases := map[string]string{
		"2.1.300 (Claude Code)\n":   "2.1.300",
		"codex-cli 0.162.1\n":       "0.162.1",
		"0.84.4":                    "0.84.4",
		"v24.19.0":                  "24.19.0",
		"tool 1.2.3-beta.1 (build)": "1.2.3-beta.1",
		"tool 1.2.3+abc.5":          "1.2.3+abc.5",
		"  nightly  \n":             "nightly",
		"":                          "",
	}
	for in, want := range cases {
		if got := VersionNumber(in); got != want {
			t.Errorf("VersionNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeHarness writes a CLI into a temp directory that prints its version
// from a state file and runs body for `update`. It returns the binary and a
// version reader of the kind RunUpdate takes.
func fakeHarness(t *testing.T, version, body string) (string, func(context.Context) string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "version")
	if version != "" {
		if err := os.WriteFile(state, []byte(version), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "tool")
	script := "#!/bin/sh\nSTATE='" + state + "'\n" +
		"if [ \"$1\" = --version ]; then [ -f \"$STATE\" ] || exit 1; printf 'tool %s\\n' \"$(cat \"$STATE\")\"; exit 0; fi\n" +
		"if [ \"$1\" = update ]; then\n" + body + "\nfi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context) string {
		out, err := exec.CommandContext(ctx, bin, "--version").Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	return bin, read
}

func TestRunUpdateReportsTheNewVersion(t *testing.T) {
	bin, version := fakeHarness(t, "1.0.0", `printf '%s' "$NEXT" > "$STATE"; echo updated`)
	env := append(os.Environ(), "NEXT=1.1.0")
	got, err := RunUpdate(context.Background(), env, "fake", bin, []string{"update"}, version)
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "1.0.0" || got.To != "1.1.0" || !got.Changed() {
		t.Errorf("result = %+v; want 1.0.0 -> 1.1.0", got)
	}
}

func TestRunUpdateWithNothingNewer(t *testing.T) {
	bin, version := fakeHarness(t, "1.0.0", `echo "already on the latest version"`)
	got, err := RunUpdate(context.Background(), nil, "fake", bin, []string{"update"}, version)
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed() || got.To != "1.0.0" {
		t.Errorf("result = %+v; want no change", got)
	}
}

func TestRunUpdateFailureCarriesTheUpdatersLastWords(t *testing.T) {
	bin, version := fakeHarness(t, "1.0.0", `echo "checking for updates"
echo "downloading 2.0.0"
echo "error: EACCES writing /usr/local/lib" >&2
exit 3`)
	got, err := RunUpdate(context.Background(), nil, "fake", bin, []string{"update"}, version)
	if err == nil {
		t.Fatal("a failing updater reported success")
	}
	if !strings.Contains(err.Error(), "EACCES writing /usr/local/lib") {
		t.Errorf("error %q does not say why", err)
	}
	if got.From != "1.0.0" || got.To != "" {
		t.Errorf("result = %+v; want only From", got)
	}
}

func TestRunUpdateSilentFailureNamesTheStatus(t *testing.T) {
	bin, version := fakeHarness(t, "1.0.0", `exit 7`)
	_, err := RunUpdate(context.Background(), nil, "fake", bin, []string{"update"}, version)
	if err == nil || !strings.Contains(err.Error(), "7") {
		t.Errorf("error = %v; want the exit status", err)
	}
}

func TestRunUpdateFailureNamesTheHarnessNotTheFile(t *testing.T) {
	// Claude Code's binary resolves to versions/<version>; an error that
	// named the file would read "2.1 update failed".
	bin, _ := fakeHarness(t, "1.0.0", `exit 7`)
	versioned := filepath.Join(filepath.Dir(bin), "2.1.300")
	if err := os.Rename(bin, versioned); err != nil {
		t.Fatal(err)
	}
	_, err := RunUpdate(context.Background(), nil, "fake", versioned, []string{"update"}, func(context.Context) string { return "1.0.0" })
	if err == nil || !strings.HasPrefix(err.Error(), "fake update ") {
		t.Errorf("error = %v; want it to name the harness", err)
	}
}

func TestRunUpdateDoesNotWaitOnAPrompt(t *testing.T) {
	// An updater that asks a question must get end-of-input, not hang.
	bin, version := fakeHarness(t, "1.0.0", `read answer || { echo "no answer to: continue?"; exit 1; }
printf '2.0.0' > "$STATE"`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := RunUpdate(ctx, nil, "fake", bin, []string{"update"}, version)
	if err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("error = %v; want the prompt to fail on closed stdin", err)
	}
}

func TestRunUpdateUnreadableVersionBeforeDoesNotUpdate(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	bin, version := fakeHarness(t, "", `touch '`+marker+`'`)
	_, err := RunUpdate(context.Background(), nil, "fake", bin, []string{"update"}, version)
	if err == nil || !strings.Contains(err.Error(), "before") {
		t.Errorf("error = %v; want the version before to be unreadable", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the updater ran without a version to compare against")
	}
}

func TestRunUpdateUnreadableVersionAfter(t *testing.T) {
	bin, version := fakeHarness(t, "1.0.0", `rm "$STATE"`)
	got, err := RunUpdate(context.Background(), nil, "fake", bin, []string{"update"}, version)
	if err == nil || !strings.Contains(err.Error(), "after") {
		t.Errorf("error = %v; want the version after to be unreadable", err)
	}
	if got.From != "1.0.0" {
		t.Errorf("From = %q", got.From)
	}
}

func TestRunUpdateStuckUpdaterIsKilledAtTheDeadline(t *testing.T) {
	bin, version := fakeHarness(t, "1.0.0", `echo "waiting for lock"; sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := RunUpdate(ctx, nil, "fake", bin, []string{"update"}, version)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("took %s; the deadline did not stop the updater", elapsed)
	}
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "waiting for lock") {
		t.Errorf("error = %v; want a timeout that says what the updater was doing", err)
	}
}

func TestRunUpdateCanceledByCaller(t *testing.T) {
	bin, version := fakeHarness(t, "1.0.0", `sleep 30`)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	_, err := RunUpdate(ctx, nil, "fake", bin, []string{"update"}, version)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v; want context.Canceled", err)
	}
}

func TestOutputTail(t *testing.T) {
	long := strings.Repeat("x", 500)
	cases := []struct {
		name, in, want string
	}{
		{"last lines only", "a\nb\nc\nd\n\n", "b c d"},
		{"colour stripped", "\x1b[31merror:\x1b[0m bad\n", "error: bad"},
		{"progress frames", "10%\r50%\r100%\nfailed\n", "50% 100% failed"},
		{"blank", " \n\n", ""},
	}
	for _, c := range cases {
		if got := outputTail(c.in); got != c.want {
			t.Errorf("%s: outputTail = %q, want %q", c.name, got, c.want)
		}
	}
	if got := outputTail("before\n" + long); len([]rune(got)) > failureTail || !strings.HasPrefix(got, "xxx") {
		t.Errorf("long line: got %d runes starting %q", len([]rune(got)), got[:10])
	}
}
