package adminauth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testPasswordHash(password string) string {
	salt := []byte("0123456789abcdef")
	rounds := minPBKDF2Rounds
	digest := pbkdf2SHA256([]byte(password), salt, rounds, sha256.Size)
	return fmt.Sprintf("$bootoptim$pbkdf2-sha256$%d$%s$%s", rounds,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(digest))
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(Config{Username: "operator", PasswordHash: testPasswordHash("correct horse"), SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRequireAdminRejectsAnonymous(t *testing.T) {
	recorder := httptest.NewRecorder()
	RequireAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("anonymous request reached protected handler")
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/admin/profiles", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestLoginSessionAndCSRF(t *testing.T) {
	m := newTestManager(t)

	loginGet := httptest.NewRecorder()
	m.LoginHandler("/admin/").ServeHTTP(loginGet, httptest.NewRequest(http.MethodGet, "https://admin.test/admin/login", nil))
	if loginGet.Code != http.StatusOK {
		t.Fatalf("GET login status = %d", loginGet.Code)
	}
	var loginCookie *http.Cookie
	for _, cookie := range loginGet.Result().Cookies() {
		if cookie.Name == loginCSRFCookieName {
			loginCookie = cookie
		}
	}
	if loginCookie == nil || !loginCookie.Secure || !loginCookie.HttpOnly || loginCookie.SameSite != http.SameSiteStrictMode || loginCookie.Path != "/" {
		t.Fatalf("login CSRF cookie missing secure attributes: %#v", loginCookie)
	}

	form := url.Values{
		"username":   {"operator"},
		"password":   {"correct horse"},
		"csrf_token": {loginCookie.Value},
	}
	request := httptest.NewRequest(http.MethodPost, "https://admin.test/admin/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://admin.test")
	request.AddCookie(loginCookie)
	loginPost := httptest.NewRecorder()
	m.LoginHandler("/admin/").ServeHTTP(loginPost, request)
	if loginPost.Code != http.StatusSeeOther {
		t.Fatalf("POST login status = %d, body=%q", loginPost.Code, loginPost.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range loginPost.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.MaxAge > 0 {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Path != "/" {
		t.Fatalf("session cookie missing secure attributes: %#v", sessionCookie)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "https://admin.test/admin/api/session", nil)
	sessionRequest.AddCookie(sessionCookie)
	sessionRecorder := httptest.NewRecorder()
	m.Authenticate(RequireAdmin(m.SessionHandler())).ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK {
		t.Fatalf("session endpoint status = %d, body=%q", sessionRecorder.Code, sessionRecorder.Body.String())
	}
	body := sessionRecorder.Body.String()
	marker := `"csrf_token":"`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("session response lacks CSRF token: %q", body)
	}
	start += len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("malformed CSRF token response: %q", body)
	}
	csrfToken := body[start : start+end]

	protected := m.Authenticate(RequireAdmin(m.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))))
	withoutCSRF := httptest.NewRequest(http.MethodPost, "https://admin.test/v1/admin/revisions", nil)
	withoutCSRF.AddCookie(sessionCookie)
	withoutCSRF.Header.Set("Origin", "https://admin.test")
	withoutRecorder := httptest.NewRecorder()
	protected.ServeHTTP(withoutRecorder, withoutCSRF)
	if withoutRecorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want %d", withoutRecorder.Code, http.StatusForbidden)
	}

	withCSRF := httptest.NewRequest(http.MethodPost, "https://admin.test/v1/admin/revisions", nil)
	withCSRF.AddCookie(sessionCookie)
	withCSRF.Header.Set("Origin", "https://admin.test")
	withCSRF.Header.Set(CSRFHeader, csrfToken)
	withRecorder := httptest.NewRecorder()
	protected.ServeHTTP(withRecorder, withCSRF)
	if withRecorder.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF status = %d, want %d", withRecorder.Code, http.StatusNoContent)
	}
}

func TestLoginErrorsDoNotIdentifyBadField(t *testing.T) {
	m := newTestManager(t)
	loginGet := httptest.NewRecorder()
	m.LoginHandler("/admin/").ServeHTTP(loginGet, httptest.NewRequest(http.MethodGet, "https://admin.test/admin/login", nil))
	var loginCookie *http.Cookie
	for _, cookie := range loginGet.Result().Cookies() {
		if cookie.Name == loginCSRFCookieName {
			loginCookie = cookie
		}
	}
	if loginCookie == nil {
		t.Fatal("missing login CSRF cookie")
	}

	attempt := func(username, password string) string {
		form := url.Values{"username": {username}, "password": {password}, "csrf_token": {loginCookie.Value}}
		req := httptest.NewRequest(http.MethodPost, "https://admin.test/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://admin.test")
		req.AddCookie(loginCookie)
		rec := httptest.NewRecorder()
		m.LoginHandler("/admin/").ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		return rec.Body.String()
	}
	badUser := attempt("someone-else", "correct horse")
	badPassword := attempt("operator", "wrong password")
	if badUser != badPassword {
		t.Fatal("bad username and bad password produced distinguishable response bodies")
	}
}

func TestParsePasswordHashRejectsWeakRounds(t *testing.T) {
	salt := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	digest := base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))
	if _, err := parsePasswordHash("$bootoptim$pbkdf2-sha256$1000$" + salt + "$" + digest); err == nil {
		t.Fatal("weak PBKDF2 rounds accepted")
	}
}
