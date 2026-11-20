package profileapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

func TestConfigPolicyRulesRoundTrip(t *testing.T) {
	env := newChangelogEnv(t)
	ref := env.stage("mod bytes")
	env.publish(clManifest("profile_reglas", "rev_g000000000000001", 1, nil, "", clModEntry("mod_a", "a.jar", ref)))

	rec := env.call("GET", "/v1/admin/profiles/profile_reglas/config-policies", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"rules":[]`) {
		t.Fatalf("empty rules: %d %s", rec.Code, rec.Body.String())
	}
	body := `{"rules":[{"path":"config/fancymenu/","policy":"enforced"},{"path":"config/fancymenu/user.txt","policy":"default_once"},{"path":"config/rrls.toml","policy":"enforced"}]}`
	if rec := env.call("PUT", "/v1/admin/profiles/profile_reglas/config-policies", body); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	rec = env.call("GET", "/v1/admin/profiles/profile_reglas/config-policies", "")
	var got configPolicyBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rules) != 3 ||
		got.Rules[0] != (storage.ConfigPolicyRule{Path: "config/fancymenu/", Policy: "enforced"}) || got.UpdatedAt == "" {
		t.Fatalf("stored rules: %v %s", err, rec.Body.String())
	}
	if rec := env.call("PUT", "/v1/admin/profiles/profile_nadie/config-policies", `{"rules":[]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown profile: %d", rec.Code)
	}
}

func TestConfigPolicyRulesRejectInvalid(t *testing.T) {
	env := newChangelogEnv(t)
	ref := env.stage("mod bytes")
	env.publish(clManifest("profile_reglas", "rev_g000000000000001", 1, nil, "", clModEntry("mod_a", "a.jar", ref)))
	for _, body := range []string{
		`{"rules":[{"path":"mods/","policy":"enforced"}]}`,
		`{"rules":[{"path":"config/../x/","policy":"enforced"}]}`,
		`{"rules":[{"path":"config//x","policy":"enforced"}]}`,
		`{"rules":[{"path":"config/x","policy":"user_owned"}]}`,
		`{"rules":[{"path":"config/x","policy":"enforced"},{"path":"config/x","policy":"default_once"}]}`,
		`{"rules":[{"path":"config/x","policy":"enforced","extra":1}]}`,
	} {
		if rec := env.call("PUT", "/v1/admin/profiles/profile_reglas/config-policies", body); rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("%s accepted: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := env.call("PUT", "/v1/admin/profiles/profile_reglas/config-policies", `{"rules":[{"path":"config/","policy":"enforced"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("config/ root folder rule: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDeletedProfilesListed(t *testing.T) {
	env := newChangelogEnv(t)
	ref := env.stage("mod bytes")
	env.publish(clManifest("profile_borrado", "rev_b000000000000001", 1, nil, "", clModEntry("mod_a", "a.jar", ref)))
	rec := env.call("GET", "/v1/admin/deleted-profiles", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted_profile_ids":[]`) {
		t.Fatalf("before delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.call("DELETE", "/v1/admin/profiles/profile_borrado", `{"confirm_profile_id":"profile_borrado"}`); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = env.call("GET", "/v1/admin/deleted-profiles", "")
	if !strings.Contains(rec.Body.String(), `"deleted_profile_ids":["profile_borrado"]`) {
		t.Fatalf("after delete: %s", rec.Body.String())
	}
	if rec := env.call("PUT", "/v1/admin/profiles/profile_borrado/config-policies", `{"rules":[]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("rules on deleted profile: %d", rec.Code)
	}
}

// The panel expands a folder rule into per-file policies. For an inherited
// config whose bytes did not change it re-lists the same object with the new
// policy; the server must accept that policy-only override.
func TestChildPolicyOnlyConfigOverride(t *testing.T) {
	env := newChangelogEnv(t)
	mod := env.stage("mod bytes")
	cfg := env.stage("menu = 1\n")
	root := clManifest("profile_madre", "rev_m000000000000001", 1, nil, "", clModEntry("mod_a", "a.jar", mod))
	root.Configs = []revision.ConfigEntry{{Path: "config/fancymenu/menu.txt", Object: cfg, Policy: "default_once"}}
	published := env.publish(root)
	expected := cfg.SHA256
	child := clManifest("profile_hija", "rev_h000000000000001", 1, pin(published, "profile_madre"), "")
	child.Configs = []revision.ConfigEntry{{Path: "config/fancymenu/menu.txt", Object: cfg, Policy: "enforced", ExpectBaseObjectSHA256: &expected}}
	env.publish(child)
}
