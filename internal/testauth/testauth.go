// Package testauth guards the test-profile API with one shared bearer token.
//
// The administrator generates and rotates the token from the panel. Only its
// SHA-256 is stored; the plaintext is returned once and never logged. There is
// no cookie and no CSRF: callers are terminals and CI, not browsers.
package testauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	tokenPrefix = "wbt_"
	// DefaultAgent is the owner label when X-Wbombo-Agent is absent.
	DefaultAgent = "sin-etiqueta"
	// AgentHeader carries the informative owner label.
	AgentHeader = "X-Wbombo-Agent"

	defaultMaxFailures   = 20
	defaultFailureWindow = 10 * time.Minute
)

// TokenStore persists the token verifier.
type TokenStore interface {
	TestTokenHash(context.Context) (string, time.Time, bool, error)
	SetTestTokenHash(context.Context, string, time.Time) error
}

type Status struct {
	Configured bool      `json:"configured"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

type Manager struct {
	store  TokenStore
	random io.Reader
	now    func() time.Time

	mu        sync.RWMutex
	hash      [sha256.Size]byte
	createdAt time.Time
	ready     bool

	limitMu       sync.Mutex
	failures      map[string]failureWindow
	maxFailures   int
	failureWindow time.Duration
}

type failureWindow struct {
	start time.Time
	count int
}

type contextKey struct{}

// New loads the current verifier so rotation survives restarts.
func New(ctx context.Context, store TokenStore) (*Manager, error) {
	if store == nil {
		return nil, errors.New("test token store is required")
	}
	m := &Manager{store: store, random: rand.Reader, now: time.Now, failures: make(map[string]failureWindow),
		maxFailures: defaultMaxFailures, failureWindow: defaultFailureWindow}
	hash, created, ok, err := store.TestTokenHash(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("stored test token verifier is malformed")
		}
		copy(m.hash[:], decoded)
		m.createdAt, m.ready = created, true
	}
	return m, nil
}

// Rotate creates a new token, stores only its hash and invalidates the
// previous one immediately. The plaintext is for the caller to show once.
func (m *Manager) Rotate(ctx context.Context) (string, time.Time, error) {
	var raw [32]byte
	if _, err := io.ReadFull(m.random, raw[:]); err != nil {
		return "", time.Time{}, err
	}
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(token))
	created := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.SetTestTokenHash(ctx, hex.EncodeToString(hash[:]), created); err != nil {
		return "", time.Time{}, err
	}
	m.hash, m.createdAt, m.ready = hash, created, true
	return token, created, nil
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.ready {
		return Status{}
	}
	return Status{Configured: true, CreatedAt: m.createdAt}
}

// Agent returns the owner label attached by Middleware.
func Agent(ctx context.Context) string {
	if agent, ok := ctx.Value(contextKey{}).(string); ok {
		return agent
	}
	return DefaultAgent
}

// WithAgent is for in-process callers and tests that bypass Middleware.
func WithAgent(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, contextKey{}, agent)
}

// Middleware requires the shared token on every request it wraps.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := clientKey(r)
		if m.blocked(client) {
			w.Header().Set("Retry-After", "600")
			writeError(w, http.StatusTooManyRequests, "too_many_auth_failures", "too many failed token attempts from this address; wait and retry")
			return
		}
		m.mu.RLock()
		ready, expected := m.ready, m.hash
		m.mu.RUnlock()
		if !ready {
			writeError(w, http.StatusServiceUnavailable, "test_token_not_configured", "the administrator has not generated the test API token yet")
			return
		}
		presented, ok := bearer(r.Header.Get("Authorization"))
		got := sha256.Sum256([]byte(presented))
		if !ok || subtle.ConstantTimeCompare(got[:], expected[:]) != 1 {
			m.fail(client)
			w.Header().Set("WWW-Authenticate", `Bearer realm="wbombo-test"`)
			writeError(w, http.StatusUnauthorized, "invalid_token", "missing or invalid test API token")
			return
		}
		agent, ok := agentLabel(r.Header.Get(AgentHeader))
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_agent", "X-Wbombo-Agent must be 1..64 characters of [A-Za-z0-9._-]")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithAgent(r.Context(), agent)))
	})
}

func (m *Manager) blocked(client string) bool {
	m.limitMu.Lock()
	defer m.limitMu.Unlock()
	window, ok := m.failures[client]
	if !ok {
		return false
	}
	if m.now().Sub(window.start) >= m.failureWindow {
		delete(m.failures, client)
		return false
	}
	return window.count >= m.maxFailures
}

func (m *Manager) fail(client string) {
	m.limitMu.Lock()
	defer m.limitMu.Unlock()
	now := m.now()
	if len(m.failures) > 4096 {
		for key, window := range m.failures {
			if now.Sub(window.start) >= m.failureWindow {
				delete(m.failures, key)
			}
		}
	}
	window, ok := m.failures[client]
	if !ok || now.Sub(window.start) >= m.failureWindow {
		window = failureWindow{start: now}
	}
	window.count++
	m.failures[client] = window
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearer(header string) (string, bool) {
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	token := strings.TrimSpace(header[len(scheme):])
	return token, token != "" && len(token) <= 256
}

func agentLabel(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultAgent, true
	}
	if len(value) > 64 {
		return "", false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return "", false
		}
	}
	return value, true
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schema_version": 1, "protocol_version": 1,
		"error": map[string]string{"code": code, "message": message},
	})
}
