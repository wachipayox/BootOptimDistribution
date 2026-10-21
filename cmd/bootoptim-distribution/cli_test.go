package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/profileapi"
	"github.com/wachipayox/BootOptimDistribution/internal/releasesigning"
	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
	"github.com/wachipayox/BootOptimDistribution/internal/testauth"
)

// TestPerfilesPruebaCLI drives tools/perfiles_prueba.py against a TLS server
// wired like main (LAN filter with public reads and the token-protected test
// API). The client address is loopback, i.e. outside the configured LAN.
func TestPerfilesPruebaCLI(t *testing.T) {
	python := findPython(t)
	root := t.TempDir()
	cas, err := storage.OpenCAS(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenSQLite(filepath.Join(root, "metadata.sqlite3"), cas)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	signer, err := releasesigning.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := testauth.New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	keys := releaseKeySet{}
	for id, public := range signer.PublicKeys() {
		keys[id] = public
	}
	passthrough := func(next http.Handler) http.Handler { return next }
	profiles, err := profileapi.New(profileapi.Dependencies{
		Objects: cas, Store: store, Keys: keys, Signer: signer, PublicKeys: keys.Encoded(), TestTokens: tokens,
		DiskUsage: func() (uint64, uint64, error) { return storage.DiskUsage(root) },
	}, profileapi.Options{ReadMiddleware: passthrough, AdminMiddleware: passthrough, TestMiddleware: tokens.Middleware,
		TestQuotas: profileapi.TestQuotas{DiskMarginBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	// In-process admin calls stand in for the LAN panel.
	admin := func(method, path string, body []byte) []byte {
		t.Helper()
		rec := httptest.NewRecorder()
		profiles.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewReader(body)))
		if rec.Code >= 300 {
			t.Fatalf("admin %s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	_, lan, _ := net.ParseCIDR("192.168.77.0/24")
	server := httptest.NewTLSServer(restrictToCIDR(newHandlerWithConfig(handlerConfig{ProfileAPI: profiles}), lan, true))
	defer server.Close()
	caFile := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}

	cli := filepath.Join("..", "..", "tools", "perfiles_prueba.py")
	run := func(token string, args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command(python, append([]string{cli}, args...)...)
		cmd.Env = append(os.Environ(), "WBOMBO_DIST_URL="+server.URL, "WBOMBO_DIST_TOKEN="+token,
			"WBOMBO_AGENT=agente-cli", "WBOMBO_DIST_CA_FILE="+caFile, "PYTHONIOENCODING=utf-8")
		out, err := cmd.CombinedOutput()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		text := string(out)
		if strings.Contains(text, server.URL) || (token != "" && strings.Contains(text, token)) {
			t.Fatalf("CLI printed the URL or the token:\n%s", text)
		}
		return code, text
	}
	expect := func(want int, token string, args ...string) string {
		t.Helper()
		code, out := run(token, args...)
		if code != want || !strings.Contains(out, "[WBOMBO-TESTPROFILE] ") {
			t.Fatalf("%v: exit %d, want %d\n%s", args, code, want, out)
		}
		return out
	}

	// Before the admin generates the token, writes are refused (exit 4).
	expect(4, "wbt_none", "create", "x", "--parent", "profile_base")
	var rotated struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(admin("POST", "/v1/admin/test-api/token", nil), &rotated)
	token := rotated.Token

	// A normal parent (the local stand-in for the Beta), published by the admin.
	publishBase := func(revisionID string, sequence int, content string) {
		sum := sha256.Sum256([]byte(content))
		digest := hex.EncodeToString(sum[:])
		admin("POST", "/v1/admin/objects/sha256/"+digest, []byte(content))
		manifest := map[string]any{
			"schema_version": 1,
			"revision":       map[string]any{"id": revisionID, "sequence": sequence, "created_at": "2026-10-22T10:00:00Z"},
			"profile":        map[string]any{"id": "profile_base", "name": "Base local", "official": true},
			"game":           map[string]any{"minecraft": "1.21.1", "neoforge": "21.1.0"},
			"base":           nil,
			"permissions": map[string]any{"derive_local": true, "mods": map[string]any{"add": true, "remove": true},
				"configs": map[string]any{"override_enforced": false, "override_default_once": true}, "max_inheritance_depth": 8},
			"mods":        []any{map[string]any{"id": "mod_base", "path": "mods/base.jar", "object": map[string]any{"sha256": digest, "size": len(content)}}},
			"remove_mods": []any{}, "configs": []any{}, "remove_configs": []any{}, "objects": []any{},
		}
		raw, _ := json.Marshal(manifest)
		canonical, err := revision.CanonicalizeValidatedManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		sum = sha256.Sum256(canonical)
		body, _ := json.Marshal(map[string]any{"canonicalization": "RFC8785-JCS", "manifest_sha256": hex.EncodeToString(sum[:]), "manifest": json.RawMessage(canonical)})
		admin("POST", "/v1/admin/publications", body)
	}
	publishBase("rev_base000000000001", 1, "base v1")
	publishBase("rev_base000000000002", 2, "base v2")

	expect(0, "", "list")
	expect(4, "wbt_wrong", "create", "x", "--parent", "profile_base")
	out := expect(0, token, "create", "cli", "--parent", "profile_base", "--parent-rev", "rev_base000000000001")
	if !regexp.MustCompile(`OK profile=profile_test-cli revision=rev_[0-9a-f]{32} manifest_sha256=[0-9a-f]{64}`).MatchString(out) {
		t.Fatalf("create output:\n%s", out)
	}
	jar := filepath.Join(root, "agente.jar")
	if err := os.WriteFile(jar, []byte("agent jar bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	out = expect(0, token, "add-mod", "profile_test-cli", jar, "--notes", "probar mod")
	if !strings.Contains(out, "subido") {
		t.Fatalf("add-mod did not upload:\n%s", out)
	}
	if out = expect(0, token, "add-mod", "profile_test-cli", jar); !strings.Contains(out, "ya estaba") {
		t.Fatalf("add-mod re-uploaded an object the server had:\n%s", out)
	}
	expect(0, token, "repin", "profile_test-cli", "--parent-rev", "latest")
	out = expect(0, "", "show", "profile_test-cli", "--effective")
	if !strings.Contains(out, "mods/agente.jar") || !strings.Contains(out, "rev_base000000000002") {
		t.Fatalf("show:\n%s", out)
	}
	expect(0, token, "follow", "profile_test-cli", "off")
	before := regexp.MustCompile(`revision=(rev_[0-9a-f]+)`).FindStringSubmatch(expect(0, "", "show", "profile_test-cli"))[1]
	publishBase("rev_base000000000003", 3, "base v3")
	after := regexp.MustCompile(`revision=(rev_[0-9a-f]+)`).FindStringSubmatch(expect(0, "", "show", "profile_test-cli"))[1]
	if before != after {
		t.Fatalf("pinned test profile moved: %s -> %s", before, after)
	}
	expect(0, "", "list", "--include-test")
	expect(0, token, "usage")
	// Writes to a normal profile are always rejected with exit 3, client- and server-side.
	if out = expect(3, token, "remove-mod", "profile_base", "base.jar"); !strings.Contains(out, "REJECTED profile=profile_base") {
		t.Fatalf("normal profile write:\n%s", out)
	}
	expect(2, token, "delete", "profile_test-cli", "--confirm", "otro")
	expect(0, token, "delete", "profile_test-cli", "--confirm", "profile_test-cli")
	expect(5, token, "repin", "profile_test-cli")
	expect(6, token, "create", "cli", "--parent", "profile_base")
}

func findPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			if exec.Command(path, "-c", "import sys; sys.exit(0 if sys.version_info >= (3, 8) else 1)").Run() == nil {
				return path
			}
		}
	}
	t.Skip("python 3.8+ not available")
	return ""
}
