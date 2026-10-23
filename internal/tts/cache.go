package tts

import (
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
	return &Pending{f: f, cache: c, key: key}, nil
}

// Pending is an entry being written.
type Pending struct {
	f     *os.File
	cache *Cache
	key   string
}

func (p *Pending) Write(b []byte) (int, error) { return p.f.Write(b) }

// Commit publishes the entry, then brings the cache back under its limit.
func (p *Pending) Commit() error {
	if err := p.f.Close(); err != nil {
		_ = os.Remove(p.f.Name())
		return err
	}
	if err := os.Rename(p.f.Name(), p.cache.path(p.key)); err != nil {
		_ = os.Remove(p.f.Name())
		return err
	}
	return p.cache.prune()
}

// Discard drops the entry.
func (p *Pending) Discard() {
	_ = p.f.Close()
	_ = os.Remove(p.f.Name())
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
