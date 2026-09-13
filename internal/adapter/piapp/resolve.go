package piapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// This file finds the pi binary. PATH alone is not enough: pi is installed by
// npm, and an npm run under nvm puts it in ~/.nvm/versions/node/<v>/bin, a
// directory that only exists on the PATH of an interactive login shell. The
// server is usually not started from one — under a service manager, or by a
// dev script — so a machine with a perfectly good pi install would report the
// CLI as missing. Claude Code has the same problem and solves it the same way,
// by walking the places an install can be.

// findPi returns the pi binary's path, most explicit source first.
func (a *Adapter) findPi() (string, bool) {
	// A configured path that is wrong is an error, not a hint to go looking:
	// the operator said which pi they meant.
	if strings.ContainsRune(a.Bin, filepath.Separator) {
		if isExecutable(a.Bin) {
			return a.Bin, true
		}
		return "", false
	}
	if env := os.Getenv("OMNIPLEX_PI_PATH"); env != "" && isExecutable(env) {
		return env, true
	}
	if p, err := exec.LookPath(a.Bin); err == nil {
		return p, true
	}
	for _, candidate := range piCandidates() {
		if isExecutable(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// piCandidates lists install locations off the PATH, newest node version
// first. Homebrew and /usr/local are included for a machine whose npm prefix
// is system-wide; they cost a stat each and answer for installs this list
// would otherwise miss.
func piCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	// nvm keeps one bin directory per installed node version.
	versions, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*"))
	sort.Slice(versions, func(i, j int) bool {
		return newerVersion(filepath.Base(versions[i]), filepath.Base(versions[j]))
	})
	for _, v := range versions {
		out = append(out, filepath.Join(v, "bin", "pi"))
	}
	for _, dir := range []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, "node_modules", ".bin"),
		"/usr/local/bin",
		"/opt/homebrew/bin",
	} {
		out = append(out, filepath.Join(dir, "pi"))
	}
	return out
}

// newerVersion orders two node version directory names ("v24.19.0"). A string
// compare would put v9 above v24, which is how you end up launching pi under
// a node that predates it.
func newerVersion(a, b string) bool {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		if aerr != nil || berr != nil {
			break
		}
		if an != bn {
			return an > bn
		}
	}
	return a > b
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// withBinDir puts the pi binary's own directory on the child's PATH.
//
// pi is a node script whose shebang is `#!/usr/bin/env node`, so exec'ing it
// resolves node through the child's PATH, not ours. Having found a pi the
// PATH did not know about, we would otherwise start it and have the kernel
// fail on a node the PATH does not know about either. Its sibling node — the
// one that installed it — is the right one to run it, so prepending its
// directory both fixes the shebang and pins the runtime.
func withBinDir(binPath string, env []string) []string {
	dir := filepath.Dir(binPath)
	if dir == "" || dir == "." {
		return env
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok && name == "PATH" {
			found = true
			kv = "PATH=" + dir + string(os.PathListSeparator) + value
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}
