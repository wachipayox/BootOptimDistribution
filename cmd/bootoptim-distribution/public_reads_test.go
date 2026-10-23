package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRestrictToCIDRPublicReads(t *testing.T) {
	_, lan, _ := net.ParseCIDR("192.168.1.0/24")
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	cases := []struct {
		method, path, remote string
		public               bool
		want                 int
	}{
		{"GET", "/v1/profiles", "203.0.113.7:5000", true, 200},
		{"HEAD", "/v1/profiles/p/revisions/r/manifest", "203.0.113.7:5000", true, 200},
		{"GET", "/v1/signing-keys", "203.0.113.7:5000", true, 200},
		{"GET", "/healthz", "203.0.113.7:5000", true, 200},
		{"GET", "/v1/launcher/pandora/releases", "203.0.113.7:5000", true, 200},
		{"POST", "/v1/admin/launcher-releases", "203.0.113.7:5000", true, 403},
		{"POST", "/v1/profiles", "203.0.113.7:5000", true, 403},
		{"GET", "/v1/admin/profiles", "203.0.113.7:5000", true, 403},
		{"GET", "/v1/../admin/", "203.0.113.7:5000", true, 403},
		{"GET", "/admin/", "203.0.113.7:5000", true, 403},
		{"GET", "/admin/login", "203.0.113.7:5000", true, 403},
		{"GET", "/v1/profiles", "203.0.113.7:5000", false, 403},
		{"POST", "/v1/admin/publications", "192.168.1.20:5000", true, 200},
		// The token-protected test API is reachable outside the LAN in HTTPS mode only.
		{"POST", "/v1/test/profiles", "203.0.113.7:5000", true, 200},
		{"DELETE", "/v1/test/profiles/profile_test-x", "203.0.113.7:5000", true, 200},
		{"POST", "/v1/test/profiles", "203.0.113.7:5000", false, 403},
		{"POST", "/v1/test/../admin/publications", "203.0.113.7:5000", true, 403},
		{"POST", "/v1/admin/test-api/token", "203.0.113.7:5000", true, 403},
		{"DELETE", "/v1/admin/profiles/profile_test-x", "203.0.113.7:5000", true, 403},
		{"GET", "/admin/", "192.168.1.20:5000", false, 200},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "http://x"+c.path, nil)
		req.URL.Path = c.path
		req.RemoteAddr = c.remote
		rec := httptest.NewRecorder()
		restrictToCIDR(ok, lan, c.public).ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s from %s public=%v: got %d, want %d", c.method, c.path, c.remote, c.public, rec.Code, c.want)
		}
	}
}
