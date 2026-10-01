package overlay

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A MagicDNS name is fully qualified and ends in a dot. Leaving it on produces
// URLs that mostly work and occasionally do not, which is worse than failing.
func TestDetectStripsTheTrailingDot(t *testing.T) {
	cli := fakeCLI(t, `{
	  "BackendState": "Running",
	  "MagicDNSSuffix": "tailb9bafe.ts.net",
	  "Self": {
	    "DNSName": "aarons-macbook-pro.tailb9bafe.ts.net.",
	    "TailscaleIPs": ["100.91.44.16", "fd7a:115c:a1e0::cb01:2cc0"],
	    "Online": true
	  }
	}`)
	t.Setenv("OMNIPLEX_TAILSCALE_PATH", cli)

	got := Detect(context.Background())
	if got.DNSName != "aarons-macbook-pro.tailb9bafe.ts.net" {
		t.Fatalf("DNSName = %q; trailing dot not stripped", got.DNSName)
	}
	if !got.Running {
		t.Fatal("Running = false for BackendState Running")
	}
	if len(got.IPs) != 2 {
		t.Fatalf("IPs = %v", got.IPs)
	}
}

// Installed but signed out is a real state. Advertising a URL for it sends the
// user to a host that does not resolve.
func TestDetectRefusesWhenBackendIsNotRunning(t *testing.T) {
	for _, state := range []string{"NeedsLogin", "Stopped", "NoState", "Starting"} {
		cli := fakeCLI(t, `{"BackendState":"`+state+`","Self":{"DNSName":"box.tail1.ts.net."}}`)
		t.Setenv("OMNIPLEX_TAILSCALE_PATH", cli)

		got := Detect(context.Background())
		if got.Running {
			t.Errorf("BackendState %q reported as running", state)
		}
		if got.CLI == "" {
			t.Errorf("BackendState %q lost the CLI path", state)
		}
	}
}

// A CLI that fails, hangs, or prints nonsense must degrade to "no overlay",
// never to a panic or a stall.
func TestDetectSurvivesABrokenCLI(t *testing.T) {
	t.Run("non-zero exit", func(t *testing.T) {
		cli := failingCLI(t)
		t.Setenv("OMNIPLEX_TAILSCALE_PATH", cli)
		got := Detect(context.Background())
		if got.Running || got.DNSName != "" {
			t.Fatalf("got %+v; want an empty status", got)
		}
		if got.CLI != cli {
			t.Fatalf("CLI = %q; a failing binary is still installed", got.CLI)
		}
	})

	t.Run("garbage output", func(t *testing.T) {
		cli := fakeCLI(t, "not json at all")
		t.Setenv("OMNIPLEX_TAILSCALE_PATH", cli)
		got := Detect(context.Background())
		if got.Running {
			t.Fatalf("garbage parsed as running: %+v", got)
		}
	})
}

func TestDetectWithNoCLIAtAll(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	restore := candidates
	candidates = func() []string { return nil }
	t.Cleanup(func() { candidates = restore })

	got := Detect(context.Background())
	if got.CLI != "" || got.Running {
		t.Fatalf("got %+v; want nothing found", got)
	}
}

// An explicit path that does not exist must fail rather than quietly falling
// back to a different binary than the operator named.
func TestExplicitPathIsNotSecondGuessed(t *testing.T) {
	t.Setenv("OMNIPLEX_TAILSCALE_PATH", filepath.Join(t.TempDir(), "does-not-exist"))
	if got := FindCLI(); got != "" {
		t.Fatalf("FindCLI() = %q; want empty for a bad explicit path", got)
	}
}

// The macOS App Store build never puts its binary on PATH, so a PATH-only
// lookup reports "not installed" on a machine where Tailscale is running.
func TestCLICandidatesCoverTheMacAppBundle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific paths")
	}
	var sawBundle bool
	for _, c := range cliCandidates() {
		if strings.Contains(c, "Tailscale.app/Contents/MacOS") {
			sawBundle = true
		}
	}
	if !sawBundle {
		t.Fatal("the app bundle path is missing from the candidate list")
	}
}

func TestServeCommandShape(t *testing.T) {
	got := ServeCommand("/usr/local/bin/tailscale", 8787)
	want := []string{"/usr/local/bin/tailscale", "serve", "--bg", "--https=443", "http://127.0.0.1:8787"}
	if len(got) != len(want) {
		t.Fatalf("ServeCommand() = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ServeCommand()[%d] = %q; want %q", i, got[i], want[i])
		}
	}

	off := ServeOffCommand("/usr/local/bin/tailscale")
	if off[len(off)-1] != "off" {
		t.Fatalf("ServeOffCommand() = %v", off)
	}
}

func TestCheckServeDetectsOurProxy(t *testing.T) {
	status := serveStatusJSON{}
	if err := json.Unmarshal([]byte(`{
	  "Web": {
	    "box.tail1.ts.net:443": {
	      "Handlers": { "/": { "Proxy": "http://127.0.0.1:8787" } }
	    }
	  }
	}`), &status); err != nil {
		t.Fatal(err)
	}

	cli := fakeCLI(t, `{"Web":{"box.tail1.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}}}}`)
	got := CheckServe(context.Background(), cli, 8787, "box.tail1.ts.net")
	if !got.Enabled {
		t.Fatal("serve mapping to our port was not recognised")
	}
	if got.URL != "https://box.tail1.ts.net" {
		t.Fatalf("URL = %q", got.URL)
	}
}

// A mapping on another https port is ours at that port, not at the default
// one, which may well be another server's.
func TestCheckServeKeepsTheHTTPSPort(t *testing.T) {
	cli := fakeCLI(t, `{"Web":{
	  "box.tail1.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}},
	  "box.tail1.ts.net:9801":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8801"}}}}}`)
	got := CheckServe(context.Background(), cli, 8801, "box.tail1.ts.net")
	if !got.Enabled || got.URL != "https://box.tail1.ts.net:9801" {
		t.Fatalf("got %+v", got)
	}
}

// A serve mapping pointing somewhere else must not be claimed as ours, or the
// UI offers to turn off something it did not turn on.
func TestCheckServeIgnoresAnotherServicesMapping(t *testing.T) {
	cli := fakeCLI(t, `{"Web":{"box.tail1.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`)
	if got := CheckServe(context.Background(), cli, 8787, "box.tail1.ts.net"); got.Enabled {
		t.Fatalf("claimed another service's mapping: %+v", got)
	}
}

// fakeCLI writes a script that prints the given JSON on stdout.
func fakeCLI(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tailscale")
	script := "#!/bin/sh\ncat <<'JSON'\n" + output + "\nJSON\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func failingCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tailscale")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
