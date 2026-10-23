package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelHasLauncherReleaseSection(t *testing.T) {
	handler := NewHandler(EmptyReadModel{}, BuildInfo{Version: "test", Commit: "test"})
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	for _, fragment := range []string{
		"/admin/assets/launcher-releases.js?refresh=", `data-view="launcher"`, `id="launcher-form"`,
		`id="launcher-artifact-rows"`, `id="launcher-release-lists"`, `id="launcher-edit"`, "Versiones del launcher",
	} {
		if !strings.Contains(page.Body.String(), fragment) {
			t.Errorf("panel is missing %q", fragment)
		}
	}
	script := httptest.NewRecorder()
	handler.ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/admin/assets/launcher-releases.js", nil))
	for _, fragment := range []string{"/v1/admin/launcher-releases", "/v1/admin/objects/sha256/", "hashFile", "window.confirm"} {
		if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), fragment) {
			t.Errorf("launcher-releases.js is missing %q (status %d)", fragment, script.Code)
		}
	}
}
