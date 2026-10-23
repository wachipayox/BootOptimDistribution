package profileapi

import (
	"net/http"
	"testing"
)

func TestAndroidCompatibleDefaultsToFalseAndIsNotInherited(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.seedNormal()
	for _, id := range []string{"profile_root", "profile_child"} {
		if v, ok := env.publicEntry(id)["android_compatible"]; !ok || v != false {
			t.Fatalf("%s: android_compatible = %v (present %v), want false", id, v, ok)
		}
	}
	if rec := env.put("profile_root", `{"android_compatible":true}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if env.publicEntry("profile_root")["android_compatible"] != true {
		t.Fatal("flag not saved")
	}
	if env.catalogEntry("/v1/admin/profiles", "profile_root")["android_compatible"] != true {
		t.Fatal("admin catalog misses the flag")
	}
	own, _ := env.catalogEntry("/v1/admin/profiles", "profile_root")["own_presentation"].(map[string]any)
	if own["android_compatible"] != true {
		t.Fatalf("own_presentation: %v", own)
	}
	// The child does not inherit it, and other edits keep it.
	if env.publicEntry("profile_child")["android_compatible"] != false {
		t.Fatal("android_compatible was inherited")
	}
	if env.put("profile_root", `{"description":"otra"}`).Code != 200 || env.publicEntry("profile_root")["android_compatible"] != true {
		t.Fatal("an absent android_compatible changed the flag")
	}
	if env.put("profile_root", `{"android_compatible":false}`).Code != 200 || env.publicEntry("profile_root")["android_compatible"] != false {
		t.Fatal("flag not cleared")
	}
}

func TestAndroidCompatibleMustBeBoolean(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.seedNormal()
	for _, value := range []string{`"true"`, `1`, `0`, `null`, `[]`, `{}`} {
		rec := env.put("profile_root", `{"android_compatible":`+value+`}`)
		if rec.Code != 422 || errorCode(rec) != "invalid_presentation" {
			t.Errorf("%s: %d %s", value, rec.Code, rec.Body.String())
		}
	}
}

func TestAndroidCompatibleThroughTestAPI(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	env.seedNormal()
	decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"a","parent_profile_id":"profile_root"}`), http.StatusCreated)
	if rec := env.test("PUT", "/v1/test/profiles/profile_test-a/presentation", `{"android_compatible":true}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if env.publicEntry("profile_test-a")["android_compatible"] != true {
		t.Fatal("test profile flag not saved")
	}
	if rec := env.test("PUT", "/v1/test/profiles/profile_test-a/presentation", `{"android_compatible":"yes"}`); rec.Code != 422 {
		t.Fatalf("%d", rec.Code)
	}
}
