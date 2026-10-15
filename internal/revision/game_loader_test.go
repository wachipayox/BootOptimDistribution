package revision

import (
	"encoding/json"
	"strings"
	"testing"
)

// loaderManifest rewrites the baseline test manifest to the given schema and game object.
func loaderManifest(t *testing.T, schema int, game map[string]interface{}) []byte {
	t.Helper()
	var wire map[string]interface{}
	if err := json.Unmarshal(testManifest("profile_root", "rev_0000000000000001", 1, nil), &wire); err != nil {
		t.Fatal(err)
	}
	wire["schema_version"] = schema
	wire["game"] = game
	if schema >= 2 {
		wire["config_settings"] = []interface{}{}
	}
	if schema >= 3 {
		wire["jvm_options"] = []interface{}{}
		wire["remove_jvm_options"] = []interface{}{}
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSchema4GameLoader(t *testing.T) {
	loader := func(kind, version string) map[string]interface{} {
		return map[string]interface{}{"minecraft": "1.20.1", "loader": map[string]interface{}{"kind": kind, "version": version}}
	}
	for _, tc := range []struct {
		name    string
		schema  int
		game    map[string]interface{}
		wantErr string
	}{
		{"fabric", 4, loader("fabric", "0.16.9"), ""},
		{"forge", 4, loader("forge", "47.3.0"), ""},
		{"old forge", 4, loader("forge", "10.13.4.1614-1.7.10"), ""},
		{"neoforge", 4, loader("neoforge", "21.1.1"), ""},
		{"unknown kind", 4, loader("quilt", "1.0.0"), "not supported"},
		{"unsafe version", 4, loader("fabric", "../x"), "unsafe"},
		{"empty version", 4, loader("fabric", ""), "unsafe"},
		{"schema 4 with neoforge field", 4, map[string]interface{}{"minecraft": "1.21.1", "neoforge": "21.1.1"}, "game"},
		{"loader on schema 3", 3, loader("fabric", "0.16.9"), "game"},
		{"neoforge schema 3", 3, map[string]interface{}{"minecraft": "1.21.1", "neoforge": "21.1.1"}, ""},
		{"neoforge schema 1", 1, map[string]interface{}{"minecraft": "1.21.1", "neoforge": "21.1.1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseAndValidateManifest(loaderManifest(t, tc.schema, tc.game))
			if tc.wantErr == "" && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestSameGameAcrossSchemas(t *testing.T) {
	old := Game{Minecraft: "1.21.1", NeoForge: "21.1.1"}
	upgraded := Game{Minecraft: "1.21.1", Loader: &GameLoader{Kind: LoaderNeoForge, Version: "21.1.1"}}
	if !SameGame(old, upgraded) {
		t.Fatal("equivalent NeoForge games must match across schema versions")
	}
	if SameGame(old, Game{Minecraft: "1.21.1", Loader: &GameLoader{Kind: LoaderFabric, Version: "21.1.1"}}) {
		t.Fatal("different loader kinds must not match")
	}
}
