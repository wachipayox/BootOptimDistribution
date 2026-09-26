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

func TestAdminHTTPSRequiresPrivateBindAndCIDR(t *testing.T) {
	if _, err := validateAdminExposureModes(false, false, true, "192.168.1.20:8443", "192.168.1.0/24"); err != nil {
		t.Fatalf("valid HTTPS LAN exposure rejected: %v", err)
	}
	for _, tc := range []struct {
		listen string
		cidr   string
	}{
		{"0.0.0.0:8443", "192.168.1.0/24"},
		{"127.0.0.1:8443", "127.0.0.0/8"},
		{"192.168.1.20:8443", ""},
		{"192.168.1.20:8443", "10.0.0.0/8"},
		{"localhost:8443", "192.168.1.0/24"},
	} {
		if _, err := validateAdminExposureModes(false, false, true, tc.listen, tc.cidr); err == nil {
			t.Fatalf("HTTPS exposure %q / %q succeeded, want error", tc.listen, tc.cidr)
		}
	}
}

func TestAdminModesAreMutuallyExclusive(t *testing.T) {
	if _, err := validateAdminExposureModes(false, true, true, "192.168.1.20:8443", "192.168.1.0/24"); err == nil {
		t.Fatal("legacy LAN HTTP and authenticated HTTPS modes were accepted together")
	}
}

func TestAdminHTTPSOptionsAreExplicit(t *testing.T) {
	if err := validateAdminHTTPSOptions(true, "server.crt", "server.key", "operator", "/etc/bootoptim/admin-password.hash"); err != nil {
		t.Fatalf("valid HTTPS options rejected: %v", err)
	}
	for _, tc := range []struct {
		cert, key, user, hash string
	}{
		{"", "server.key", "operator", "/etc/bootoptim/admin-password.hash"},
		{"server.crt", "", "operator", "/etc/bootoptim/admin-password.hash"},
		{"server.crt", "server.key", "", "/etc/bootoptim/admin-password.hash"},
		{"server.crt", "server.key", "operator", ""},
	} {
		if err := validateAdminHTTPSOptions(true, tc.cert, tc.key, tc.user, tc.hash); err == nil {
			t.Fatalf("incomplete HTTPS options accepted: %#v", tc)
		}
	}
	if err := validateAdminHTTPSOptions(false, "server.crt", "", "", ""); err == nil {
		t.Fatal("TLS certificate option accepted without --admin-ui-https")
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

func TestHTTPSAddsHSTSOnlyInSecureMode(t *testing.T) {
	plain := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := plain.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("plain handler unexpectedly sets HSTS: %q", got)
	}

	secure := httptest.NewRecorder()
	securityHeadersForHTTPS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }), true).ServeHTTP(secure, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := secure.Header().Get("Strict-Transport-Security"); got == "" {
		t.Fatal("secure handler did not set HSTS")
	}
}
