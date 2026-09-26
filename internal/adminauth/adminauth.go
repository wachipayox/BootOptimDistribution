package adminauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	RoleAdmin  Role = "admin"
	CSRFHeader      = "X-CSRF-Token"

	sessionCookieName   = "__Host-bootoptim_admin"
	loginCSRFCookieName = "__Host-bootoptim_login_csrf"

	defaultSessionTTL = 8 * time.Hour
	minPBKDF2Rounds   = 200000
	maxPBKDF2Rounds   = 2000000
)

type Role string

type Principal struct {
	Username string `json:"username"`
	Role     Role   `json:"role"`
}

type Config struct {
	Username     string
	PasswordHash string
	SessionTTL   time.Duration
}

type contextKey uint8

const (
	principalKey contextKey = iota
	csrfKey
	sessionIDKey
)

type session struct {
	Principal Principal
	CSRFToken string
	ExpiresAt time.Time
}

type Manager struct {
	username     string
	passwordHash passwordHash
	sessionTTL   time.Duration
	now          func() time.Time
	random       io.Reader

	mu       sync.Mutex
	sessions map[string]session
}

type passwordHash struct {
	rounds int
	salt   []byte
	digest []byte
}

var loginPage = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>BootOptim Distribution admin login</title>
</head>
<body>
<main>
<h1>BootOptim Distribution</h1>
<p>Administrator sign in</p>
{{if .Error}}<p role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/admin/login">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label>Username <input name="username" autocomplete="username" required></label><br>
<label>Password <input name="password" type="password" autocomplete="current-password" required></label><br>
<button type="submit">Sign in</button>
</form>
</main>
</body>
</html>`))

func New(cfg Config) (*Manager, error) {
	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		return nil, errors.New("administrator username is required")
	}
	if len(username) > 128 {
		return nil, errors.New("administrator username is too long")
	}
	hash, err := parsePasswordHash(strings.TrimSpace(cfg.PasswordHash))
	if err != nil {
		return nil, fmt.Errorf("parse administrator password hash: %w", err)
	}
	ttl := cfg.SessionTTL
	if ttl == 0 {
		ttl = defaultSessionTTL
	}
	if ttl < 5*time.Minute || ttl > 24*time.Hour {
		return nil, errors.New("administrator session TTL must be between 5 minutes and 24 hours")
	}
	return &Manager{
		username:     username,
		passwordHash: hash,
		sessionTTL:   ttl,
		now:          time.Now,
		random:       rand.Reader,
		sessions:     make(map[string]session),
	}, nil
}

// ReadPasswordHashFile reads the local administrator password verifier. The
// file must be regular, small and accessible only to its owner (mode 0600 or
// stricter). The verifier is not a release-signing key and must never contain
// an Ed25519 private key.
func ReadPasswordHashFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("administrator password hash file is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("administrator password hash path must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("administrator password hash file permissions %04o are too broad; require 0600 or stricter", info.Mode().Perm())
	}
	if info.Size() <= 0 || info.Size() > 4096 {
		return "", errors.New("administrator password hash file has invalid size")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New("administrator password hash file is empty")
	}
	return value, nil
}

// PrincipalFromContext returns the authenticated Distribution principal, if
// one was attached by Manager.Authenticate.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey).(Principal)
	return principal, ok
}

func IsAdmin(ctx context.Context) bool {
	principal, ok := PrincipalFromContext(ctx)
	return ok && principal.Role == RoleAdmin
}

// RequireAdmin is the small authorization boundary intended for admin API
// handlers. It trusts only the authenticated principal already present in the
// request context; CIDR checks are a separate outer defense and do not grant a
// role.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdminPage redirects unauthenticated browser reads to the local login
// page. API handlers should use RequireAdmin instead so they receive 401.
func RequireAdminPage(loginPath string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				http.Redirect(w, r, loginPath, http.StatusSeeOther)
				return
			}
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Authenticate resolves the opaque secure session cookie and attaches an admin
// principal plus its CSRF token to the request context. Invalid or expired
// cookies are treated as anonymous without exposing why authentication failed.
func (m *Manager) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		m.mu.Lock()
		sess, ok := m.sessions[cookie.Value]
		if ok && !m.now().Before(sess.ExpiresAt) {
			delete(m.sessions, cookie.Value)
			ok = false
		}
		m.mu.Unlock()
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), principalKey, sess.Principal)
		ctx = context.WithValue(ctx, csrfKey, sess.CSRFToken)
		ctx = context.WithValue(ctx, sessionIDKey, cookie.Value)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireCSRF protects unsafe methods by requiring the per-session token in
// X-CSRF-Token. It is designed to be composed with RequireAdmin after
// Manager.Authenticate. Safe GET/HEAD/OPTIONS requests pass through unchanged.
func (m *Manager) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !sameOriginIfPresent(r) {
			forbidden(w)
			return
		}
		expected, ok := r.Context().Value(csrfKey).(string)
		provided := r.Header.Get(CSRFHeader)
		if !ok || expected == "" || provided == "" || !constantTimeStringEqual(expected, provided) {
			forbidden(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) LoginHandler(successPath string) http.Handler {
	if successPath == "" || successPath[0] != '/' {
		successPath = "/admin/"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		setLoginSecurityHeaders(w)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if IsAdmin(r.Context()) {
				http.Redirect(w, r, successPath, http.StatusSeeOther)
				return
			}
			token, err := m.randomToken()
			if err != nil {
				http.Error(w, "login unavailable", http.StatusServiceUnavailable)
				return
			}
			setLoginCSRFCookie(w, token, m.now().Add(10*time.Minute))
			m.writeLoginPage(w, r.Method == http.MethodHead, http.StatusOK, token, "")
		case http.MethodPost:
			m.handleLoginPost(w, r, successPath)
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func (m *Manager) LogoutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if sessionID, ok := r.Context().Value(sessionIDKey).(string); ok && sessionID != "" {
			m.mu.Lock()
			delete(m.sessions, sessionID)
			m.mu.Unlock()
		}
		clearSessionCookie(w)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

func (m *Manager) SessionHandler() http.Handler {
	type response struct {
		Principal Principal `json:"principal"`
		CSRFToken string    `json:"csrf_token"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		principal, ok := PrincipalFromContext(r.Context())
		csrf, csrfOK := r.Context().Value(csrfKey).(string)
		if !ok || !csrfOK {
			unauthorized(w)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(response{Principal: principal, CSRFToken: csrf})
	})
}

func (m *Manager) handleLoginPost(w http.ResponseWriter, r *http.Request, successPath string) {
	if !sameOriginIfPresent(r) {
		m.renderRejectedLogin(w, http.StatusBadRequest, "Request could not be accepted.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		m.renderRejectedLogin(w, http.StatusBadRequest, "Request could not be accepted.")
		return
	}
	csrfCookie, err := r.Cookie(loginCSRFCookieName)
	providedCSRF := r.PostForm.Get("csrf_token")
	if err != nil || providedCSRF == "" || csrfCookie.Value == "" || !constantTimeStringEqual(providedCSRF, csrfCookie.Value) {
		m.renderRejectedLogin(w, http.StatusBadRequest, "Request could not be accepted.")
		return
	}

	username := r.PostForm.Get("username")
	password := r.PostForm.Get("password")
	usernameOK := constantTimeStringEqual(username, m.username)
	passwordOK := m.passwordHash.verify(password)
	if !usernameOK || !passwordOK {
		m.writeLoginPage(w, false, http.StatusUnauthorized, providedCSRF, "Invalid credentials.")
		return
	}

	sessionID, err := m.randomToken()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	csrfToken, err := m.randomToken()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	expires := m.now().Add(m.sessionTTL)
	m.mu.Lock()
	m.sessions[sessionID] = session{
		Principal: Principal{Username: m.username, Role: RoleAdmin},
		CSRFToken: csrfToken,
		ExpiresAt: expires,
	}
	m.pruneExpiredLocked()
	m.mu.Unlock()

	setSessionCookie(w, sessionID, expires, m.sessionTTL)
	clearLoginCSRFCookie(w)
	http.Redirect(w, r, successPath, http.StatusSeeOther)
}

func (m *Manager) renderRejectedLogin(w http.ResponseWriter, status int, message string) {
	token, err := m.randomToken()
	if err != nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	setLoginCSRFCookie(w, token, m.now().Add(10*time.Minute))
	m.writeLoginPage(w, false, status, token, message)
}

func (m *Manager) writeLoginPage(w http.ResponseWriter, head bool, status int, csrfToken, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if head {
		return
	}
	_ = loginPage.Execute(w, struct {
		CSRFToken string
		Error     string
	}{CSRFToken: csrfToken, Error: message})
}

func (m *Manager) randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(m.random, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (m *Manager) pruneExpiredLocked() {
	now := m.now()
	for id, sess := range m.sessions {
		if !now.Before(sess.ExpiresAt) {
			delete(m.sessions, id)
		}
	}
}

func setSessionCookie(w http.ResponseWriter, value string, expires time.Time, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func setLoginCSRFCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginCSRFCookieName,
		Value:    value,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   600,
	})
}

func clearLoginCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginCSRFCookieName,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func setLoginSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func sameOriginIfPresent(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && parsed.Host == r.Host
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func forbidden(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "request forbidden", http.StatusForbidden)
}

func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		_ = subtle.ConstantTimeCompare([]byte(a), []byte(a))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func parsePasswordHash(encoded string) (passwordHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "bootoptim" || parts[2] != "pbkdf2-sha256" {
		return passwordHash{}, errors.New("expected $bootoptim$pbkdf2-sha256$<rounds>$<salt>$<digest>")
	}
	rounds, err := strconv.Atoi(parts[3])
	if err != nil || rounds < minPBKDF2Rounds || rounds > maxPBKDF2Rounds {
		return passwordHash{}, fmt.Errorf("PBKDF2 rounds must be between %d and %d", minPBKDF2Rounds, maxPBKDF2Rounds)
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return passwordHash{}, errors.New("PBKDF2 salt must be 16 to 64 bytes of base64url data")
	}
	digest, err := base64.RawURLEncoding.DecodeString(parts[5])
	if err != nil || len(digest) != sha256.Size {
		return passwordHash{}, errors.New("PBKDF2 digest must be 32 bytes of base64url data")
	}
	return passwordHash{rounds: rounds, salt: salt, digest: digest}, nil
}

func (p passwordHash) verify(password string) bool {
	derived := pbkdf2SHA256([]byte(password), p.salt, p.rounds, len(p.digest))
	return subtle.ConstantTimeCompare(derived, p.digest) == 1
}

func pbkdf2SHA256(password, salt []byte, rounds, keyLen int) []byte {
	const hashLen = sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	counter := make([]byte, 4)
	for block := 1; block <= blocks; block++ {
		binary.BigEndian.PutUint32(counter, uint32(block))
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write(counter)
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < rounds; i++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
