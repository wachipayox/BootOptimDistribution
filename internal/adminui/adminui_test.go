package adminui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOverviewIsEmptyWithoutDomainAdapter(t *testing.T) {
	handler := NewHandler(EmptyReadModel{}, BuildInfo{Version: "test-version", Commit: "test-commit"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/overview", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response overviewResponse
	if err := json.NewDecoder(recorder.Result().Body).Decode(&response); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if len(response.Overview.Profiles) != 0 {
		t.Fatalf("profiles = %#v, want empty", response.Overview.Profiles)
	}
	if len(response.Actions) != 0 {
		t.Fatalf("actions = %#v, want empty", response.Actions)
	}
}

func TestHandlerHasNoMutatingMethods(t *testing.T) {
	handler := NewHandler(EmptyReadModel{}, BuildInfo{})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, "/admin/api/overview", strings.NewReader("{}")))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want %d", method, recorder.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestPageSecurityPolicyAndNoCrossOriginAccess(t *testing.T) {
	handler := NewHandler(EmptyReadModel{}, BuildInfo{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected CORS header: %q", got)
	}
	csp := recorder.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "form-action 'none'") {
		t.Fatalf("unexpected CSP: %q", csp)
	}
}
