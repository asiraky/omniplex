// Package push sends Web Push notifications to the browsers that subscribed.
package push

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/SherClockHolmes/webpush-go"
)

// Keys is the server's VAPID keypair, base64url. Browsers subscribe against
// the public key, and every push is signed with the private one, so it must
// survive restarts or every subscription goes dead.
type Keys struct {
	Public  string `json:"publicKey"`
	Private string `json:"privateKey"`
}

func (k Keys) valid() bool { return k.Public != "" && k.Private != "" }

func readKeys(p string) (Keys, error) {
	var k Keys
	b, err := os.ReadFile(p)
	if err != nil {
		return k, err
	}
	if err := json.Unmarshal(b, &k); err != nil {
		return Keys{}, err
	}
	return k, nil
}

// LoadKeys reads <dir>/vapid.json, generating a P-256 VAPID keypair on first
// use.
func LoadKeys(dir string) (Keys, error) {
	p := filepath.Join(dir, "vapid.json")
	if k, err := readKeys(p); err == nil && k.valid() {
		return k, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Keys{}, err
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return Keys{}, err
	}
	k := Keys{Public: pub, Private: priv}
	b, err := json.Marshal(k)
	if err != nil {
		return Keys{}, err
	}
	// Written whole under a temp name (CreateTemp makes it 0600), then linked
	// into place, so a crash mid-write or a racing reader never sees half a
	// file. Link fails if another process got there first, and its keys win.
	tmp, err := os.CreateTemp(dir, ".vapid-*")
	if err != nil {
		return Keys{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return Keys{}, err
	}
	if err := tmp.Close(); err != nil {
		return Keys{}, err
	}
	if err := os.Link(tmp.Name(), p); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return Keys{}, err
		}
		if theirs, err := readKeys(p); err == nil && theirs.valid() {
			return theirs, nil
		}
		// What is there is unreadable, so nothing can have subscribed with
		// it. Ours replaces it.
		if err := os.Rename(tmp.Name(), p); err != nil {
			return Keys{}, err
		}
	}
	return k, nil
}
