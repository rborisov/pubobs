package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ComputeChallenge computes the PKCE S256 code challenge from a verifier.
func ComputeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type pkceSession struct {
	CodeChallenge string
	RedirectURI   string
	PluginState   string
	ExpiresAt     time.Time
}

type authCode struct {
	UserID        string
	CodeChallenge string
	ExpiresAt     time.Time
}

// SessionStore holds in-memory PKCE sessions and short-lived auth codes.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*pkceSession
	codes    map[string]*authCode
	codeTTL  time.Duration
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*pkceSession),
		codes:    make(map[string]*authCode),
		codeTTL:  5 * time.Minute,
	}
}

// SetCodeTTL overrides the default 5-minute TTL (used in tests).
func (ss *SessionStore) SetCodeTTL(d time.Duration) {
	ss.mu.Lock()
	ss.codeTTL = d
	ss.mu.Unlock()
}

// StoreSession saves an incoming plugin auth request and returns a random session ID.
func (ss *SessionStore) StoreSession(codeChallenge, redirectURI, pluginState string) string {
	id := randomBase64(16)
	ss.mu.Lock()
	ss.sessions[id] = &pkceSession{
		CodeChallenge: codeChallenge,
		RedirectURI:   redirectURI,
		PluginState:   pluginState,
		ExpiresAt:     time.Now().Add(10 * time.Minute),
	}
	ss.mu.Unlock()
	return id
}

// GetSession retrieves and deletes a session (single-use).
func (ss *SessionStore) GetSession(id string) (*pkceSession, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[id]
	if !ok || time.Now().After(s.ExpiresAt) {
		delete(ss.sessions, id)
		return nil, false
	}
	delete(ss.sessions, id)
	return s, true
}

// StoreAuthCode saves an auth code tied to a user + code_challenge.
func (ss *SessionStore) StoreAuthCode(userID, codeChallenge string) string {
	code := randomBase64(32)
	ss.mu.Lock()
	ss.codes[code] = &authCode{
		UserID:        userID,
		CodeChallenge: codeChallenge,
		ExpiresAt:     time.Now().Add(ss.codeTTL),
	}
	ss.mu.Unlock()
	return code
}

// ConsumeAuthCode verifies the PKCE code exchange and returns the userID.
// The code is deleted after the first call.
func (ss *SessionStore) ConsumeAuthCode(code, codeVerifier string) (string, error) {
	ss.mu.Lock()
	ac, ok := ss.codes[code]
	delete(ss.codes, code)
	ss.mu.Unlock()

	if !ok {
		return "", errors.New("invalid or already-used auth code")
	}
	if time.Now().After(ac.ExpiresAt) {
		return "", errors.New("auth code expired")
	}
	if ComputeChallenge(codeVerifier) != ac.CodeChallenge {
		return "", fmt.Errorf("PKCE verification failed")
	}
	return ac.UserID, nil
}

func randomBase64(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
