// Package ephemeral names the private threads: assistant chats that are never
// written to disk. A private thread's rows live in an in-memory database, and
// the files it and its harness work with, transcript included, sit in a
// scratch folder that is wiped when the thread is closed and again when the
// server starts.
//
// Whether a thread is private is read off its id, so every layer that holds
// an id can tell without asking anyone: the store routes it to memory, the
// attachment and share stores file it under the scratch folder, and
// read-aloud, whose audio is cached on disk, refuses it.
package ephemeral

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const prefix = "private-"

// Unborn stands in for a private thread that does not exist yet, such as the
// one a create command is making: Is reports it private, so whatever is filed
// under it stays in memory, but it names no thread.
const Unborn = prefix

// NewID is a fresh private thread id.
func NewID() string { return prefix + uuid.NewString() }

// Is reports whether the thread with this id is private.
func Is(threadID string) bool { return strings.HasPrefix(threadID, prefix) }

// Bare is the id without its private mark: the uuid under it, for a harness
// that insists its session ids are uuids. Any other id comes back as it is.
func Bare(threadID string) string { return strings.TrimPrefix(threadID, prefix) }

var (
	mu   sync.Mutex
	root string
)

// Init picks this server's scratch folder for private threads and empties it.
// Whatever is there belongs to private threads of a server that has since
// stopped, and those threads are gone with the memory that held them. The
// folder is on a RAM-backed filesystem where the machine has one. Named after
// the database, so two servers on one machine never wipe each other's.
func Init(dbPath string) (string, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		abs = dbPath
	}
	sum := sha256.Sum256([]byte(abs))
	dir := filepath.Join(base(), "omniplex-private-"+hex.EncodeToString(sum[:6]))
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clear private scratch folder: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("make private scratch folder: %w", err)
	}
	// By its real path, which is the one a harness sees as its cwd and names
	// things after: macOS's temp folder sits behind a link.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	mu.Lock()
	root = dir
	mu.Unlock()
	return dir, nil
}

// base is /dev/shm when it is a writable folder, so nothing a private thread
// makes reaches a disk; the system temp folder otherwise.
func base() string {
	const shm = "/dev/shm"
	if info, err := os.Stat(shm); err == nil && info.IsDir() {
		if f, err := os.CreateTemp(shm, ".omniplex-probe-*"); err == nil {
			name := f.Name()
			f.Close()
			os.Remove(name)
			return shm
		}
	}
	return os.TempDir()
}

// Dir is the scratch folder Init chose. Before Init it is a folder under the
// system temp folder, so a test or a tool that never called Init still gets
// somewhere to write rather than the current directory.
func Dir() string {
	mu.Lock()
	defer mu.Unlock()
	if root == "" {
		return filepath.Join(os.TempDir(), "omniplex-private")
	}
	return root
}

// ThreadDir is the working folder of one private thread.
func ThreadDir(threadID string) string {
	return filepath.Join(Dir(), "threads", threadID)
}

// ThreadOf is the private thread whose working folder path is or is inside,
// or "" when path belongs to no private thread. It is how code that is handed
// only a folder (a harness moving a conversation between accounts) tells.
func ThreadOf(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	rel, err := filepath.Rel(filepath.Join(Dir(), "threads"), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	id, _, _ := strings.Cut(rel, string(filepath.Separator))
	if !Is(id) {
		return ""
	}
	return id
}

// StateDir is where a harness keeps a private thread's own state, such as its
// transcript: in the scratch folder, but outside the thread's working folder,
// so the agent does not find it among its files. Removed with the thread.
func StateDir(threadID, name string) string {
	return filepath.Join(Dir(), "state", threadID, name)
}

// links are the symlinks made for each private thread, so Forget can remove
// them: they sit outside the scratch folder, in a harness's own config.
var links = map[string][]string{}

// Link makes path a symlink to target, a folder Link creates if missing, so a
// harness that writes under path writes to target instead. It is how a
// harness that insists on keeping state in its own config folder keeps a
// private thread's in the scratch folder. A link already there to the same
// target is kept. Anything else at path is left alone and an error.
func Link(threadID, path, target string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if got, err := os.Readlink(path); err == nil && got == target {
		remember(threadID, path)
		return nil
	} else if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s is already there; not replacing it", path)
	}
	if err := os.Symlink(target, path); err != nil {
		return err
	}
	remember(threadID, path)
	return nil
}

func remember(threadID, path string) {
	mu.Lock()
	defer mu.Unlock()
	for _, p := range links[threadID] {
		if p == path {
			return
		}
	}
	links[threadID] = append(links[threadID], path)
}

// Forget removes everything a private thread had on the filesystem: its
// working folder, its state, and the links made for it. Every part is tried;
// the first error is returned.
func Forget(threadID string) error {
	mu.Lock()
	paths := links[threadID]
	delete(links, threadID)
	mu.Unlock()
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, p := range paths {
		// Only ever a link this package made: never follow it.
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			keep(os.Remove(p))
		}
	}
	keep(os.RemoveAll(ThreadDir(threadID)))
	keep(os.RemoveAll(filepath.Join(Dir(), "state", threadID)))
	return first
}
