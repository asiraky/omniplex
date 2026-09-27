package artefact

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Token kinds. Each is signed separately so one can never stand in for
// another: a preview token is not a share link, and neither lets an agent
// publish.
const (
	// KindPreview opens an artefact's live files inside the app's sandboxed
	// viewer. Short lived: the viewer asks for another when it needs one.
	KindPreview = "p"
	// KindShare is a link for someone outside the app. It opens the share's
	// snapshot while the share exists and its nonce matches.
	KindShare = "s"
	// KindAgent lets the omniplex MCP server a harness runs show files in its
	// own thread, and nothing else.
	KindAgent = "a"
)

var ErrBadToken = errors.New("bad or expired token")

// Claims is what a token grants.
type Claims struct {
	Kind     string
	Thread   string
	Artefact string
	// Nonce ties a share link to one share, so stopping it kills the link.
	Nonce     string
	ExpiresAt int64 // unix millis; 0 never expires
}

// Signer mints and checks tokens with a key kept beside the artefacts, so a
// share link survives a server restart.
type Signer struct{ key []byte }

// LoadSigner reads the key at <dir>/.key, creating it on first use.
func LoadSigner(dir string) (*Signer, error) {
	p := filepath.Join(dir, ".key")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 32 {
		return &Signer{key: b}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		// Another process won the race; use its key.
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		return &Signer{key: b}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(key); err != nil {
		return nil, err
	}
	return &Signer{key: key}, nil
}

// NewSigner is for tests.
func NewSigner(key []byte) *Signer { return &Signer{key: key} }

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

var b64 = base64.RawURLEncoding

// Mint signs claims into a token safe to put in a URL path segment.
func (s *Signer) Mint(c Claims) string {
	payload := strings.Join([]string{c.Kind, c.Thread, c.Artefact, c.Nonce, strconv.FormatInt(c.ExpiresAt, 10)}, "|")
	return b64.EncodeToString([]byte(payload)) + "." + b64.EncodeToString(s.mac(payload))
}

// Check verifies a token of the wanted kind and returns its claims.
func (s *Signer) Check(token, kind string, now time.Time) (Claims, error) {
	enc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return Claims{}, ErrBadToken
	}
	raw, err := b64.DecodeString(enc)
	if err != nil {
		return Claims{}, ErrBadToken
	}
	got, err := b64.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(string(raw))) {
		return Claims{}, ErrBadToken
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 5 || parts[0] != kind {
		return Claims{}, ErrBadToken
	}
	exp, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return Claims{}, ErrBadToken
	}
	if exp != 0 && now.UnixMilli() > exp {
		return Claims{}, ErrBadToken
	}
	return Claims{Kind: parts[0], Thread: parts[1], Artefact: parts[2], Nonce: parts[3], ExpiresAt: exp}, nil
}
