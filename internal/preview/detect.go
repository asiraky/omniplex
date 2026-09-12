package preview

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Found is one candidate service observed on the machine, before it is given
// an identity. Port is on loopback; Label is the best name we have for it.
type Found struct {
	Port  int
	Label string
	// Scheme is how the service must be spoken to on loopback: "http" or
	// "https". It is answered by the probe rather than assumed, because a
	// container serving TLS replies to a plaintext request with a 400 that
	// looks exactly like a working HTTP server.
	Scheme string
	Source Source
}

// Source says how a service came to our attention. It is shown to the user,
// because "the hook told me this is the app" and "something is listening on
// 5050" deserve different amounts of trust.
type Source string

const (
	// SourceDeclared came from the provision hook's resources or the
	// project config: a name someone chose deliberately.
	SourceDeclared Source = "declared"
	// SourceProcess is a listening socket owned by a process running inside
	// the session's checkout.
	SourceProcess Source = "process"
	// SourceDocker is a published port of a Compose project whose working
	// directory is the session's checkout.
	SourceDocker Source = "docker"
)

// detectors are run in order and their results merged, first-wins per port, so
// a Docker container that also has a process listener keeps the better label.
//
// Everything here is best-effort by construction: no lsof, no Docker, a
// wedged daemon, or a machine where neither is installed all yield an empty
// list rather than an error. A missing preview is a small disappointment; a
// failed session is not.

// detectProcesses returns loopback listeners owned by processes whose working
// directory is inside root.
//
// Ownership is decided by the process's cwd rather than by walking the tree
// down from the harness, because the interesting case is a server the agent
// started and detached — `npm run dev &`, a Compose up — which is reparented
// away from the harness immediately and would be invisible to a descendant
// walk.
func detectProcesses(ctx context.Context, root string) []Found {
	out, err := run(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn")
	if err != nil {
		return nil
	}
	byPID := parseListeners(out)
	if len(byPID) == 0 {
		return nil
	}

	pids := make([]string, 0, len(byPID))
	for pid := range byPID {
		pids = append(pids, pid)
	}
	sort.Strings(pids)

	// One batched call rather than one per pid: a busy Mac has a hundred
	// listeners and this runs on a timer.
	cwdOut, err := run(ctx, "lsof", "-a", "-p", strings.Join(pids, ","), "-d", "cwd", "-F", "pn")
	if err != nil {
		return nil
	}
	cwds := parseCwds(cwdOut)

	var found []Found
	for pid, ports := range byPID {
		if !within(root, cwds[pid]) {
			continue
		}
		for _, p := range ports {
			found = append(found, Found{Port: p.port, Label: p.command, Source: SourceProcess})
		}
	}
	return found
}

// detectDocker returns the published TCP ports of Compose projects whose
// working directory is inside root.
//
// The working_dir label is what makes this possible: the listening socket
// belongs to Docker's own networking process, so cwd tells us nothing, but
// Compose records the directory it was invoked from on every container it
// creates.
func detectDocker(ctx context.Context, root string) []Found {
	const format = `{{.Label "com.docker.compose.project.working_dir"}}	{{.Label "com.docker.compose.service"}}	{{.Ports}}`
	out, err := run(ctx, "docker", "ps", "--format", format)
	if err != nil {
		return nil
	}

	var found []Found
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) != 3 || !within(root, parts[0]) {
			continue
		}
		name := parts[1]
		for _, port := range parsePublishedPorts(parts[2]) {
			found = append(found, Found{Port: port, Label: name, Source: SourceDocker})
		}
	}
	return found
}

// ---- parsing ----

type listener struct {
	port    int
	command string
}

// parseListeners reads lsof's field output: a `p` line opens a process, `c`
// names it, and each `n` is one socket. Fields persist until replaced, which
// is why the current pid and command are carried down the loop.
func parseListeners(out string) map[string][]listener {
	byPID := map[string][]listener{}
	var pid, command string
	seen := map[string]bool{}

	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			pid, command = value, ""
		case 'c':
			command = value
		case 'n':
			port, ok := portOf(value)
			if !ok {
				continue
			}
			// A process listening on both IPv4 and IPv6, or on several file
			// descriptors for one socket, is one service.
			key := pid + "/" + strconv.Itoa(port)
			if seen[key] {
				continue
			}
			seen[key] = true
			byPID[pid] = append(byPID[pid], listener{port: port, command: command})
		}
	}
	return byPID
}

// portOf pulls the port from an lsof address, keeping only addresses reachable
// from this machine's loopback: `*:3000` (all interfaces), `127.0.0.1:3000`,
// `[::1]:3000`. A socket bound to a specific non-loopback address is left
// alone — we would proxy to 127.0.0.1 and not reach it.
func portOf(addr string) (int, bool) {
	// lsof writes peer addresses as "local->peer"; a listener has no peer,
	// and anything that does is not one.
	if strings.Contains(addr, "->") {
		return 0, false
	}
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0, false
	}
	host, portStr := addr[:i], addr[i+1:]
	switch host {
	case "*", "127.0.0.1", "[::1]", "[::]", "0.0.0.0":
	default:
		return 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

// parseCwds reads the `p`/`n` pairs of an lsof cwd query.
func parseCwds(out string) map[string]string {
	cwds := map[string]string{}
	var pid string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid = line[1:]
		case 'n':
			if pid != "" {
				cwds[pid] = line[1:]
			}
		}
	}
	return cwds
}

// parsePublishedPorts reads the host-side ports out of Docker's port summary,
// e.g. "0.0.0.0:10026->1025/tcp, [::]:10026->1025/tcp".
//
// Three things are deliberately dropped. UDP, which no browser can open. An
// unpublished port ("3000/tcp", no arrow), which is reachable only from
// inside the Compose network. And a published *range*
// ("20000-20039->20000-20039/udp"), because a forty-port range is an RTP
// media allocation or similar — never a web UI — and expanding it would bury
// the real link under forty rows.
func parsePublishedPorts(summary string) []int {
	var ports []int
	seen := map[int]bool{}
	for _, entry := range strings.Split(summary, ",") {
		entry = strings.TrimSpace(entry)
		if !strings.HasSuffix(entry, "/tcp") {
			continue
		}
		arrow := strings.Index(entry, "->")
		if arrow < 0 {
			continue
		}
		hostSide := entry[:arrow]
		i := strings.LastIndex(hostSide, ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(hostSide[i+1:])
		if err != nil || seen[port] {
			continue
		}
		seen[port] = true
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

// within reports whether path is root or sits inside it. Both sides are
// resolved through symlinks first, because a worktree under /Users is reached
// as /System/Volumes/Data/Users by some processes on macOS and the two spell
// the same directory.
func within(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	root, path = resolve(root), resolve(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

func resolve(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
