//go:build bundled_sidecar

package claudecode

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// In a bundled build the sidecar is compiled to a standalone executable that
// carries its own JS runtime and Anthropic's SDK, so the host needs neither
// Node nor Bun. Claude Code itself is still the user's own install — we never
// ship one.
//
//go:embed sidecar/dist/omniplex-claude-sidecar
var bundledFS embed.FS

var (
	bundledOnce sync.Once
	bundledAt   string
)

// bundledSidecarPath extracts the bundled executable once per version and
// returns its path. The content hash is in the filename, so an upgraded binary
// never reuses a stale extraction.
func bundledSidecarPath() string {
	bundledOnce.Do(func() {
		data, err := bundledFS.ReadFile("sidecar/dist/omniplex-claude-sidecar")
		if err != nil {
			return
		}
		sum := sha256.Sum256(data)
		name := "omniplex-claude-sidecar-" + hex.EncodeToString(sum[:8])
		// Windows runs nothing without an executable extension. The release
		// build embeds Bun's .exe under the extension-less name.
		if runtime.GOOS == "windows" {
			name += ".exe"
		}

		base, err := os.UserCacheDir()
		if err != nil {
			return
		}
		dir := filepath.Join(base, "omniplex", "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		path := filepath.Join(dir, name)

		if info, err := os.Stat(path); err == nil && info.Size() == int64(len(data)) {
			bundledAt = path
			return
		}
		// Write to a temp name and rename, so a torn write is never executed.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return
		}
		bundledAt = path
	})
	return bundledAt
}
