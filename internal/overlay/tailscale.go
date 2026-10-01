// Package overlay detects a WireGuard overlay network — currently Tailscale —
// and reports the stable hostname it gives this machine.
//
// The hostname is the point. A LAN address comes from DHCP and changes; a
// tailnet name does not, so it is the one URL a phone can bookmark and have
// work at home and away. Everything here is best-effort: the server must start
// whether or not any of it succeeds.
package overlay

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Status is what we learned about the overlay.
type Status struct {
	// Running is true only when the backend is up and logged in. Installed
	// but signed out is a real state, and advertising a URL for it would
	// send someone to a host that does not resolve.
	Running bool
	// DNSName is the MagicDNS name without its trailing dot, e.g.
	// "my-laptop.tailnet-name.ts.net". Empty when MagicDNS is off or the CLI
	// could not be reached.
	DNSName string
	// Suffix is the tailnet's DNS suffix, e.g. "tailnet-name.ts.net".
	Suffix string
	// IPs are the addresses this machine holds on the overlay.
	IPs []string
	// CLI is the executable we found, empty when there is none.
	CLI string
}

// cliCandidates lists where the Tailscale CLI is found, most specific first.
//
// PATH alone is not enough: the macOS App Store build keeps its binary inside
// the app bundle and never adds it to PATH, so `which tailscale` finds nothing
// on a machine where Tailscale is running perfectly well.
func cliCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
			"/Applications/Tailscale.app/Contents/MacOS/tailscale",
			"/usr/local/bin/tailscale",
			"/opt/homebrew/bin/tailscale",
			os.ExpandEnv("$HOME/Applications/Tailscale.app/Contents/MacOS/Tailscale"),
		}
	case "windows":
		return []string{
			`C:\Program Files\Tailscale\tailscale.exe`,
			`C:\Program Files (x86)\Tailscale\tailscale.exe`,
		}
	default:
		return []string{
			"/usr/bin/tailscale",
			"/usr/local/bin/tailscale",
			"/opt/homebrew/bin/tailscale",
			"/snap/bin/tailscale",
		}
	}
}

// candidates is a seam: a machine with Tailscale installed cannot otherwise
// exercise the not-installed path, because the search deliberately looks
// beyond PATH and will find the real binary.
var candidates = cliCandidates

// FindCLI returns the Tailscale executable, or "" when there is none.
func FindCLI() string {
	if env := os.Getenv("OMNIPLEX_TAILSCALE_PATH"); env != "" {
		if isExecutable(env) {
			return env
		}
		// An explicit path that is wrong is an instruction we could not
		// follow, not a hint to go looking elsewhere.
		return ""
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, c := range candidates() {
		if c != "" && isExecutable(c) {
			return c
		}
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// statusJSON is the subset of `tailscale status --json` we rely on.
type statusJSON struct {
	BackendState   string `json:"BackendState"`
	MagicDNSSuffix string `json:"MagicDNSSuffix"`
	Self           struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
	} `json:"Self"`
}

// Detect asks the CLI what it knows. It never blocks startup for long: a
// wedged or prompting binary is treated as absent.
func Detect(ctx context.Context) Status {
	cli := FindCLI()
	if cli == "" {
		return Status{}
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, cli, "status", "--json").Output()
	if err != nil {
		// Installed but not answering — signed out, daemon down, or the
		// binary needs privileges it does not have.
		return Status{CLI: cli}
	}

	var s statusJSON
	if err := json.Unmarshal(out, &s); err != nil {
		return Status{CLI: cli}
	}

	return Status{
		Running: s.BackendState == "Running",
		// MagicDNS names are fully qualified and carry a trailing dot. Left
		// on, every URL built from it is subtly wrong.
		DNSName: strings.TrimSuffix(s.Self.DNSName, "."),
		Suffix:  s.MagicDNSSuffix,
		IPs:     s.Self.TailscaleIPs,
		CLI:     cli,
	}
}

// ServeCommand is what turns the plain HTTP endpoint into an HTTPS one with a
// publicly-trusted certificate on the tailnet name.
//
// Traffic over a tailnet is already WireGuard-encrypted end to end, so this
// buys browser chrome rather than confidentiality: a secure context, which is
// what unlocks home-screen install and notifications.
func ServeCommand(cli string, port int) []string {
	return []string{cli, "serve", "--bg", "--https=443", httpTarget(port)}
}

// ServeOffCommand undoes ServeCommand.
func ServeOffCommand(cli string) []string {
	return []string{cli, "serve", "--https=443", "off"}
}

func httpTarget(port int) string {
	return "http://127.0.0.1:" + itoa(port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ServeStatus reports whether `tailscale serve` is already publishing us.
type ServeStatus struct {
	Enabled bool
	URL     string
}

// serveStatusJSON is the subset of `tailscale serve status --json` we need.
// The shape is a map of "host:port" to a handler config.
type serveStatusJSON struct {
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

// CheckServe reports whether serve is already forwarding to our port, so the
// UI can offer to enable it or to turn it off rather than guessing.
func CheckServe(ctx context.Context, cli string, port int, dnsName string) ServeStatus {
	if cli == "" || dnsName == "" {
		return ServeStatus{}
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, cli, "serve", "status", "--json").Output()
	if err != nil {
		return ServeStatus{}
	}

	var s serveStatusJSON
	if err := json.Unmarshal(out, &s); err != nil {
		return ServeStatus{}
	}

	target := httpTarget(port)
	for host, web := range s.Web {
		// Keys are "name:port"; a mapping on any port but 443 is only
		// reachable with that port in the address.
		name, port, ok := strings.Cut(host, ":")
		if !ok || name != dnsName {
			continue
		}
		for _, h := range web.Handlers {
			if h.Proxy == target {
				addr := "https://" + dnsName
				if port != "443" {
					addr += ":" + port
				}
				return ServeStatus{Enabled: true, URL: addr}
			}
		}
	}
	return ServeStatus{}
}
