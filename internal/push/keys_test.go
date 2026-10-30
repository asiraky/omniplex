package push

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKeysGeneratesOnceAndReturnsTheSameKeysAfter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")

	first, err := LoadKeys(dir)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	pub, err := base64.RawURLEncoding.DecodeString(first.Public)
	if err != nil || len(pub) != 65 || pub[0] != 4 {
		t.Fatalf("public key is not an uncompressed P-256 point: %q (%v)", first.Public, err)
	}
	if first.Private == "" {
		t.Fatalf("no private key: %+v", first)
	}

	second, err := LoadKeys(dir)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if second != first {
		t.Fatalf("keys changed between loads: %+v then %+v", first, second)
	}

	info, err := os.Stat(filepath.Join(dir, "vapid.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key file mode %o, want 600", mode)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".vapid-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestLoadKeysReplacesAFileLeftHalfWritten(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vapid.json")
	if err := os.WriteFile(p, []byte(`{"publicKey":"BAA`), 0o600); err != nil {
		t.Fatal(err)
	}

	k, err := LoadKeys(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !k.valid() {
		t.Fatalf("got incomplete keys: %+v", k)
	}
	again, err := LoadKeys(dir)
	if err != nil || again != k {
		t.Fatalf("replacement keys not kept: %+v then %+v (%v)", k, again, err)
	}
}
