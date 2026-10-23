package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/userconfig"
)

const (
	secretName = "api-key"
	// voicesTTL keeps the picker from costing an upstream round trip every
	// time Settings opens. A voice added on elevenlabs.io shows up within it.
	voicesTTL = 10 * time.Minute
	// lookupTimeout bounds the calls that are not audio: a voice list or a
	// key check that takes longer has failed.
	lookupTimeout = 20 * time.Second
)

var (
	ErrNoKey       = errors.New("no ElevenLabs API key is stored")
	ErrKeyRejected = errors.New("ElevenLabs rejected that key")
	ErrEmptyText   = errors.New("there is nothing to read aloud")
	ErrTextTooLong = fmt.Errorf("too long to read aloud: ElevenLabs takes at most %d characters at a time", MaxChars)
)

// Service is text-to-speech for the server: the key, the voice list and the
// audio cache.
type Service struct {
	secrets *provider.SecretStore
	client  client
	cache   *Cache

	mu        sync.Mutex
	voices    []Voice
	voicesKey string
	voicesAt  time.Time
}

// New keeps its cache beside the user config, so a dev server running on
// OMNIPLEX_CONFIG never shares audio with the live one.
func New(secrets *provider.SecretStore) (*Service, error) {
	cfg, err := userconfig.Path()
	if err != nil {
		return nil, err
	}
	cache, err := OpenCache(filepath.Join(filepath.Dir(cfg), "tts-cache"), cacheLimit)
	if err != nil {
		return nil, err
	}
	return &Service{secrets: secrets, client: newClient(), cache: cache}, nil
}

// Cache is where the audio is kept.
func (s *Service) Cache() *Cache { return s.cache }

func (s *Service) key() (string, bool) {
	if s == nil || s.secrets == nil {
		return "", false
	}
	key, ok := s.secrets.Get(provider.ElevenLabsSecretID, secretName)
	return key, ok && key != ""
}

// HasKey reports whether a key is stored. A nil Service has none, so a
// server without text-to-speech reports it the same way.
func (s *Service) HasKey() bool {
	_, ok := s.key()
	return ok
}

// SetKey stores a key after ElevenLabs has accepted it, or deletes the stored
// one when key is empty. Only an outright refusal stops the save: ElevenLabs
// being slow or down says nothing about the key, and the user should not have
// to come back and paste it again.
func (s *Service) SetKey(ctx context.Context, key string) error {
	if s == nil || s.secrets == nil {
		return errors.New("text-to-speech is not available on this server")
	}
	key = strings.TrimSpace(key)
	defer s.forgetVoices()
	if key == "" {
		return s.secrets.Delete(provider.ElevenLabsSecretID, secretName)
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	_, err := s.client.voicePage(ctx, key, 1, "")
	var up *UpstreamError
	if errors.As(err, &up) && up.Rejected() {
		return fmt.Errorf("%w: %s", ErrKeyRejected, up.Message)
	}
	return s.secrets.Put(provider.ElevenLabsSecretID, secretName, key)
}

func (s *Service) forgetVoices() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.voices = nil
}

// Voices lists the voices the key can use. The list is held per key, so a key
// swapped behind the server's back is not answered with the old account's.
func (s *Service) Voices(ctx context.Context) ([]Voice, error) {
	key, ok := s.key()
	if !ok {
		return nil, ErrNoKey
	}
	s.mu.Lock()
	if s.voices != nil && s.voicesKey == key && time.Since(s.voicesAt) < voicesTTL {
		voices := s.voices
		s.mu.Unlock()
		return voices, nil
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	voices, err := s.client.voices(ctx, key)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.voices, s.voicesKey, s.voicesAt = voices, key, time.Now()
	s.mu.Unlock()
	return voices, nil
}

// Stream starts synthesis and returns the audio as ElevenLabs sends it. The
// caller closes it; cancelling ctx cancels the upstream request.
func (s *Service) Stream(ctx context.Context, voiceID, text string) (io.ReadCloser, error) {
	key, ok := s.key()
	if !ok {
		return nil, ErrNoKey
	}
	return s.client.stream(ctx, key, voiceID, text)
}

// PrepareText trims text and checks it fits one request. Splitting a longer
// message is not attempted: the joins would be audible.
func PrepareText(text string) (string, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return "", ErrEmptyText
	case utf8.RuneCountInString(text) > MaxChars:
		return "", ErrTextTooLong
	}
	return text, nil
}
