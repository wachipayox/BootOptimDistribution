package testauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	hash    string
	created time.Time
}

func (m *memoryStore) TestTokenHash(context.Context) (string, time.Time, bool, error) {
	return m.hash, m.created, m.hash != "", nil
}

func (m *memoryStore) SetTestTokenHash(_ context.Context, hash string, created time.Time) error {
	m.hash, m.created = hash, created
	return nil
}

func TestMiddlewareTokenAgentAndThrottle(t *testing.T) {
	store := &memoryStore{}
	manager, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var seenAgent string
	handler := manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAgent = Agent(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	call := func(remote, auth, agent string) int {
		req := httptest.NewRequest("POST", "/v1/test/profiles", nil)
		req.RemoteAddr = remote
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if agent != "" {
			req.Header.Set(AgentHeader, agent)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := call("198.51.100.1:1", "Bearer x", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured: %d", code)
	}
	token, _, err := manager.Rotate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(store.hash, token) || len(store.hash) != 64 {
		t.Fatal("store must keep only the SHA-256 of the token")
	}
	if code := call("198.51.100.1:1", "Bearer "+token, ""); code != http.StatusNoContent || seenAgent != DefaultAgent {
		t.Fatalf("valid token: %d agent %q", code, seenAgent)
	}
	if code := call("198.51.100.1:1", "bearer "+token, "agente-269"); code != http.StatusNoContent || seenAgent != "agente-269" {
		t.Fatalf("agent label: %d %q", code, seenAgent)
	}
	if code := call("198.51.100.1:1", "Bearer "+token, "bad label!"); code != http.StatusBadRequest {
		t.Fatalf("invalid label: %d", code)
	}
	for _, auth := range []string{"", "Basic abc", "Bearer ", "Bearer " + token + "x"} {
		if code := call("198.51.100.2:1", auth, ""); code != http.StatusUnauthorized {
			t.Fatalf("%q: %d", auth, code)
		}
	}
	for i := 0; i < defaultMaxFailures; i++ {
		call("198.51.100.3:1", "Bearer nope", "")
	}
	if code := call("198.51.100.3:9", "Bearer "+token, ""); code != http.StatusTooManyRequests {
		t.Fatalf("throttle: %d", code)
	}
	if code := call("198.51.100.4:1", "Bearer "+token, ""); code != http.StatusNoContent {
		t.Fatalf("other address throttled: %d", code)
	}
	manager.now = func() time.Time { return time.Now().Add(defaultFailureWindow + time.Second) }
	if code := call("198.51.100.3:9", "Bearer "+token, ""); code != http.StatusNoContent {
		t.Fatalf("window did not expire: %d", code)
	}
}
