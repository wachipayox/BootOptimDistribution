package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionEndpoint(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/meta/version", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response versionResponse
	if err := json.NewDecoder(recorder.Result().Body).Decode(&response); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if response.ProtocolSchema != 1 || response.Service != "bootoptim-distribution" {
		t.Fatalf("unexpected version response: %#v", response)
	}
}

func TestHealthEndpoint(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestAdminUIDisabledByDefault(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestAdminUIRequiresLiteralLoopbackWhenEnabled(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:8088", "[::1]:8088"} {
		if err := validateAdminUIExposure(true, listen); err != nil {
			t.Fatalf("validateAdminUIExposure(true, %q) = %v", listen, err)
		}
	}
	for _, listen := range []string{"0.0.0.0:8088", "[::]:8088", "localhost:8088", ":8088"} {
		if err := validateAdminUIExposure(true, listen); err == nil {
			t.Fatalf("validateAdminUIExposure(true, %q) succeeded, want error", listen)
		}
	}
	if err := validateAdminUIExposure(false, "0.0.0.0:8088"); err != nil {
		t.Fatalf("disabled UI unexpectedly constrained listener: %v", err)
	}
}

func TestAdminUIActivationContainsNoSensitiveData(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandlerWithConfig(handlerConfig{AdminUIEnabled: true}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := strings.ToLower(recorder.Body.String())
	for _, forbidden := range []string{"bearer ", "authorization:", "private key", "client_secret", "access_token"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("admin UI contains forbidden sensitive marker %q", forbidden)
		}
	}
}
