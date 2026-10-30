package tts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// cacheLimit is what the cache may hold on disk: roughly nine hours of
	// speech at Format.
	cacheLimit = 256 << 20
	// pendingPrefix marks a file still being written. Prune leaves young ones
	// alone and sweeps up any a crash abandoned.
	pendingPrefix = ".pending-"
	pendingMaxAge = time.Hour
	entrySuffix   = ".mp3"
)

// CacheKey names the audio for one text in one voice. The model and format
// are part of it so that changing either never serves audio made the old way.
func CacheKey(voiceID, text string) string {
	sum := sha256.Sum256([]byte(Model + "|" + Format + "|" + voiceID + "|" + text))
	return hex.EncodeToString(sum[:])
}

// Cache holds synthesised audio on disk, one file per key, evicting the least
// recently played once it outgrows its limit.
type Cache struct {
	dir   string
	limit int64
	// mu serialises pruning, so two finishing streams do not both delete
	// down to the limit and take twice what they should.
	mu sync.Mutex
}

func OpenCache(dir string, limit int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Cache{dir: dir, limit: limit}, nil
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key+entrySuffix)
}

// Open returns the cached audio for a key. A hit counts as use, so the file's
// mtime moves forward and eviction, which goes oldest first, passes it over.
func (c *Cache) Open(key string) (*os.File, bool) {
	f, err := os.Open(c.path(key))
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(c.path(key), now, now)
	return f, true
}

// Create starts an entry. Nothing is visible under the key until Commit, so
// a stream that dies halfway never becomes a truncated clip played forever.
func (c *Cache) Create(key string) (*Pending, error) {
	f, err := os.CreateTemp(c.dir, pendingPrefix+"*")
	if err != nil {
		return nil, err
	}
	return &Pending{f: f, path: f.Name(), cache: c, key: key, changed: make(chan struct{})}, nil
}

// ErrDiscarded is what a reader of a Pending sees when the entry was dropped
// before it was finished.
var ErrDiscarded = errors.New("the audio was dropped before it was finished")

// Pending is an entry being written. It can be read while it grows: Open
// gives a reader its own handle, and Wait blocks until the bytes it wants
// have been written. That lets every request for a clip share one synthesis.
type Pending struct {
	f     *os.File
	cache *Cache
	key   string

	mu sync.Mutex
	// path is where the file is now: it moves on Commit.
	path    string
	size    int64
	done    bool
	err     error
	changed chan struct{}
}

// wake tells everyone in Wait that something changed. Callers hold mu.
func (p *Pending) wake() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// Write is only ever called by the one writer.
func (p *Pending) Write(b []byte) (int, error) {
	n, err := p.f.Write(b)
	p.mu.Lock()
	p.size += int64(n)
	p.wake()
	p.mu.Unlock()
	return n, err
}

// Open opens the entry for reading. The handle keeps working after Commit
// moves the file and after Fail or Discard removes it, so a reader never has
// to open twice.
func (p *Pending) Open() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	return os.Open(p.path)
}

// Wait blocks until at least n bytes have been written or the entry is
// finished whole, and reports how much there is. A failed entry short of n bytes
// returns its error; a cancelled ctx returns ctx's.
func (p *Pending) Wait(ctx context.Context, n int64) (size int64, done bool, err error) {
	for {
		p.mu.Lock()
		// done is only a whole clip: a failed one is never finished.
		size, done, err, changed := p.size, p.done && p.err == nil, p.err, p.changed
		p.mu.Unlock()
		// Bytes already written are handed over even from a failed entry,
		// so a reader sends what there is before it reports the failure.
		if size >= n {
			return size, done, nil
		}
		if err != nil || done {
			return size, done, err
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return size, done, ctx.Err()
		}
	}
}

// finish marks the writing over: err nil for a whole clip.
func (p *Pending) finish(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	p.done, p.err = true, err
	p.wake()
}

// Commit publishes the entry, then brings the cache back under its limit. If
// it cannot be published the entry is still finished and readable, as by
// Finish, and the caller discards it once it is no longer wanted.
func (p *Pending) Commit() error {
	if err := p.f.Close(); err != nil {
		p.finish(nil)
		return err
	}
	p.mu.Lock()
	err := os.Rename(p.path, p.cache.path(p.key))
	if err == nil {
		p.path = p.cache.path(p.key)
	}
	p.mu.Unlock()
	p.finish(nil)
	if err != nil {
		return err
	}
	return p.cache.prune()
}

// Finish ends a whole clip that is not to be kept: it stays readable until
// Discard.
func (p *Pending) Finish() {
	_ = p.f.Close()
	p.finish(nil)
}

// Fail ends the entry with err, which every reader in Wait gets, and drops it.
func (p *Pending) Fail(err error) {
	p.finish(err)
	p.remove()
}

// Discard drops the entry. Readers still waiting on one that was not
// finished get ErrDiscarded.
func (p *Pending) Discard() {
	p.finish(ErrDiscarded)
	p.remove()
}

func (p *Pending) remove() {
	_ = p.f.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.path != p.cache.path(p.key) {
		_ = os.Remove(p.path)
	}
}

func (c *Cache) prune() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var files []entry
	var total int64
	var errs []error
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(c.dir, e.Name())
		if strings.HasPrefix(e.Name(), pendingPrefix) {
			if time.Since(info.ModTime()) > pendingMaxAge {
				errs = append(errs, removeMissingOK(path))
			}
			continue
		}
		if !strings.HasSuffix(e.Name(), entrySuffix) {
			continue
		}
		files = append(files, entry{path: path, size: info.Size(), mod: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= c.limit {
			break
		}
		if err := removeMissingOK(f.path); err != nil {
			errs = append(errs, err)
			continue
		}
		total -= f.size
	}
	return errors.Join(errs...)
}

func removeMissingOK(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
