package revision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type javaRuntimeVectors struct {
	Accept []struct {
		JavaRuntime json.RawMessage `json:"java_runtime"`
	} `json:"accept"`
	Reject []struct {
		JavaRuntime json.RawMessage `json:"java_runtime"`
		Why         string          `json:"why"`
	} `json:"reject"`
	Resolve []struct {
		Name  string `json:"name"`
		Chain []struct {
			ProfileID   string       `json:"profile_id"`
			JavaRuntime *JavaRuntime `json:"java_runtime"`
		} `json:"chain"`
		PC      EffectiveJavaRuntimeChoice `json:"pc"`
		Android EffectiveJavaRuntimeChoice `json:"android"`
	} `json:"resolve"`
}

func loadJavaRuntimeVectors(t *testing.T) javaRuntimeVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "java-runtime-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors javaRuntimeVectors
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors
}

// javaRuntimeManifest puts value as profile.java_runtime of the baseline manifest.
func javaRuntimeManifest(t *testing.T, schema int, value json.RawMessage) []byte {
	t.Helper()
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(testManifest("profile_root", "rev_0000000000000001", 1, nil), &wire); err != nil {
		t.Fatal(err)
	}
	var profile map[string]json.RawMessage
	if err := json.Unmarshal(wire["profile"], &profile); err != nil {
		t.Fatal(err)
	}
	profile["java_runtime"] = value
	wire["profile"], _ = json.Marshal(profile)
	wire["schema_version"] = json.RawMessage([]byte{byte('0' + schema)})
	if schema >= 2 {
		wire["config_settings"] = json.RawMessage("[]")
	}
	if schema >= 3 {
		wire["jvm_options"] = json.RawMessage("[]")
		wire["remove_jvm_options"] = json.RawMessage("[]")
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestJavaRuntimeVectors(t *testing.T) {
	vectors := loadJavaRuntimeVectors(t)
	if len(vectors.Accept) == 0 || len(vectors.Reject) == 0 || len(vectors.Resolve) == 0 {
		t.Fatal("vector file is incomplete")
	}
	for _, schema := range []int{1, 3} {
		for _, vector := range vectors.Accept {
			manifest, _, err := parseAndValidateManifest(javaRuntimeManifest(t, schema, vector.JavaRuntime))
			if err != nil {
				t.Fatalf("schema %d rejected %s: %v", schema, vector.JavaRuntime, err)
			}
			if manifest.SchemaVersion != schema || manifest.Profile.JavaRuntime == nil {
				t.Fatalf("schema %d lost java_runtime %s", schema, vector.JavaRuntime)
			}
		}
		for _, vector := range vectors.Reject {
			if _, _, err := parseAndValidateManifest(javaRuntimeManifest(t, schema, vector.JavaRuntime)); err == nil {
				t.Fatalf("schema %d accepted %s (%s)", schema, vector.JavaRuntime, vector.Why)
			}
		}
	}
	for _, vector := range vectors.Resolve {
		chain := make([]Manifest, 0, len(vector.Chain))
		for _, item := range vector.Chain {
			chain = append(chain, Manifest{Profile: Profile{ID: item.ProfileID, JavaRuntime: item.JavaRuntime}})
		}
		got := ResolveJavaRuntimeManifests(chain)
		if got.PC != vector.PC || got.Android != vector.Android {
			t.Fatalf("%s: got %+v, want pc %+v android %+v", vector.Name, got, vector.PC, vector.Android)
		}
	}
}

// The field must not change the canonical bytes of manifests that omit it, so
// every revision published before it keeps its digest and signature.
func TestJavaRuntimeOmittedKeepsCanonicalBytes(t *testing.T) {
	raw := testManifest("profile_root", "rev_0000000000000001", 1, nil)
	manifest, canonical, err := parseAndValidateManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Profile.JavaRuntime != nil {
		t.Fatal("java_runtime appeared from nowhere")
	}
	again, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	recanonical, err := CanonicalizeJSON(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(recanonical) != string(canonical) {
		t.Fatalf("round trip changed bytes:\n%s\n%s", canonical, recanonical)
	}
}

func TestJavaRuntimeSurvivesCloneAndRoundTrip(t *testing.T) {
	raw := javaRuntimeManifest(t, 1, json.RawMessage(`{"pc":{"source":"profile","major":25},"android":{"source":"mojang"}}`))
	manifest, canonical, err := parseAndValidateManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	clone := cloneManifest(manifest)
	clone.Profile.JavaRuntime.PC.Major = 21
	if manifest.Profile.JavaRuntime.PC.Major != 25 {
		t.Fatal("clone shares java_runtime with the original")
	}
	again, _ := json.Marshal(manifest)
	recanonical, err := CanonicalizeValidatedManifest(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(recanonical) != string(canonical) {
		t.Fatalf("round trip changed bytes:\n%s\n%s", canonical, recanonical)
	}
}
