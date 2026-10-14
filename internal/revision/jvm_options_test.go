package revision

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const jitOptionJSON = `{
  "id": "jit-compiler-threads",
  "label": "Hilos del compilador JIT",
  "template": "-XX:CICompilerCount={value}",
  "type": "int", "min": 2, "max": 4,
  "help": "Limita los hilos que la JVM dedica a compilar código (JIT).",
  "java": {"min": 21},
  "vm": ["hotspot"],
  "platforms": {
    "pc": {
      "default_enabled": false,
      "default_value": 2,
      "evidence": {"url": "https://github.com/wachipayox/BootOptim/pull/354", "summary": "CI hospedado, 4 procesadores"}
    }
  }
}`

func TestJVMArgumentSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("../../docs/jvm-options-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		AllowedSystemProperties []string `json:"allowed_system_properties"`
		Accept                  []struct {
			Arg     string `json:"arg"`
			FlagKey string `json:"flag_key"`
		} `json:"accept"`
		Reject []struct {
			Arg string `json:"arg"`
			Why string `json:"why"`
		} `json:"reject"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(append([]string{}, vectors.AllowedSystemProperties...), append([]string{}, AllowedJVMSystemProperties...)) {
		t.Fatalf("vector allowlist %v differs from AllowedJVMSystemProperties %v", vectors.AllowedSystemProperties, AllowedJVMSystemProperties)
	}
	for _, vector := range vectors.Accept {
		key, err := JVMFlagKey(vector.Arg)
		if err != nil {
			t.Errorf("%q rejected: %v", vector.Arg, err)
		} else if key != vector.FlagKey {
			t.Errorf("%q flag key = %q, want %q", vector.Arg, key, vector.FlagKey)
		}
	}
	for _, vector := range vectors.Reject {
		if err := ValidateJVMArgument(vector.Arg); err == nil {
			t.Errorf("%q accepted (%s)", vector.Arg, vector.Why)
		}
	}
}

func schema3Manifest(t *testing.T, jvmOptions string, mutate func(map[string]any)) []byte {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(testManifest("profile_root", "rev_0000000000000001", 1, nil), &manifest); err != nil {
		t.Fatal(err)
	}
	var options any
	if err := json.Unmarshal([]byte(jvmOptions), &options); err != nil {
		t.Fatal(err)
	}
	manifest["schema_version"] = 3
	manifest["config_settings"] = []any{}
	manifest["jvm_options"] = options
	manifest["remove_jvm_options"] = []any{}
	if mutate != nil {
		mutate(manifest)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSchema3JVMOptionsVerifyAndRender(t *testing.T) {
	pub, priv := testKey()
	verified := mustVerify(t, schema3Manifest(t, "["+jitOptionJSON+"]", nil), priv, pub)
	options := verified.Manifest().JVMOptions
	if len(options) != 1 || options[0].ID != "jit-compiler-threads" || options[0].Platforms["pc"].DefaultEnabled {
		t.Fatalf("unexpected options: %+v", options)
	}
	arg, err := RenderJVMOption(options[0], options[0].Platforms["pc"].DefaultValue)
	if err != nil || arg != "-XX:CICompilerCount=2" {
		t.Fatalf("render = %q, %v", arg, err)
	}
	if _, err := RenderJVMOption(options[0], float64(5)); err == nil {
		t.Fatal("out-of-range value rendered")
	}
}

func TestSchema3ShapeRejections(t *testing.T) {
	cases := map[string]func(map[string]any){
		"schema 2 cannot carry jvm options": func(m map[string]any) { m["schema_version"] = 2 },
		"schema 3 requires jvm_options":     func(m map[string]any) { delete(m, "jvm_options") },
		"schema 3 requires removals list":   func(m map[string]any) { delete(m, "remove_jvm_options") },
		"schema 3 requires config_settings": func(m map[string]any) { delete(m, "config_settings") },
		"null inside an option": func(m map[string]any) {
			m["jvm_options"].([]any)[0].(map[string]any)["max"] = nil
		},
		"missing default_enabled": func(m map[string]any) {
			platform := m["jvm_options"].([]any)[0].(map[string]any)["platforms"].(map[string]any)["pc"].(map[string]any)
			delete(platform, "default_enabled")
		},
		"unknown option field": func(m map[string]any) {
			m["jvm_options"].([]any)[0].(map[string]any)["raw_args"] = "-javaagent:x.jar"
		},
		"root removal": func(m map[string]any) { m["remove_jvm_options"] = []any{"jit-compiler-threads-old"} },
	}
	for name, mutate := range cases {
		if _, err := CanonicalizeValidatedManifest(schema3Manifest(t, "["+jitOptionJSON+"]", mutate)); !errors.Is(err, ErrInvalidManifest) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if _, err := CanonicalizeValidatedManifest(schema3Manifest(t, "[]", nil)); err != nil {
		t.Fatalf("schema 3 with no options rejected: %v", err)
	}
}

func jitOption(t *testing.T) JVMOption {
	t.Helper()
	var option JVMOption
	if err := json.Unmarshal([]byte(jitOptionJSON), &option); err != nil {
		t.Fatal(err)
	}
	return option
}

func TestJVMOptionValueRules(t *testing.T) {
	if err := validateJVMOption(jitOption(t)); err != nil {
		t.Fatalf("valid option rejected: %v", err)
	}
	int64p := func(v int64) *int64 { return &v }
	cases := map[string]func(*JVMOption){
		"uppercase id":          func(o *JVMOption) { o.ID = "JIT" },
		"agent template":        func(o *JVMOption) { o.Template = "-javaagent:{value}.jar" },
		"two placeholders":      func(o *JVMOption) { o.Template = "-XX:CICompilerCount={value}{value}" },
		"int without max":       func(o *JVMOption) { o.Max = nil },
		"inverted range":        func(o *JVMOption) { o.Min, o.Max = int64p(4), int64p(2) },
		"bool with placeholder": func(o *JVMOption) { o.Type = JVMOptionTypeBool; o.Min, o.Max = nil, nil },
		"XX without vm":         func(o *JVMOption) { o.VM = nil },
		"unknown vm":            func(o *JVMOption) { o.VM = []string{"graal"} },
		"java before 8":         func(o *JVMOption) { o.Java.Min = 7 },
		"no platforms":          func(o *JVMOption) { o.Platforms = map[string]JVMOptionPlatform{} },
		"unknown platform":      func(o *JVMOption) { o.Platforms["ios"] = o.Platforms["pc"] },
		"default above max":     func(o *JVMOption) { p := o.Platforms["pc"]; p.DefaultValue = float64(9); o.Platforms["pc"] = p },
		"fractional default":    func(o *JVMOption) { p := o.Platforms["pc"]; p.DefaultValue = 2.5; o.Platforms["pc"] = p },
		"missing default":       func(o *JVMOption) { p := o.Platforms["pc"]; p.DefaultValue = nil; o.Platforms["pc"] = p },
		"http evidence": func(o *JVMOption) {
			p := o.Platforms["pc"]
			p.Evidence.URL = "http://example.com"
			o.Platforms["pc"] = p
		},
		"empty help":              func(o *JVMOption) { o.Help = "" },
		"multi-line label":        func(o *JVMOption) { o.Label = "a\nb" },
		"enum with range":         func(o *JVMOption) { o.Type = JVMOptionTypeEnum; o.Values = []string{"2"} },
		"auto without rules":      func(o *JVMOption) { setAuto(o, &JVMOptionAuto{}) },
		"auto unknown fact":       func(o *JVMOption) { setAuto(o, autoWith("gpu_vendor", int64p(1), nil, true, float64(2))) },
		"auto empty range":        func(o *JVMOption) { setAuto(o, autoWith("logical_cpus", nil, nil, true, float64(2))) },
		"auto enabled no value":   func(o *JVMOption) { setAuto(o, autoWith("logical_cpus", nil, int64p(4), true, nil)) },
		"auto value out of range": func(o *JVMOption) { setAuto(o, autoWith("logical_cpus", nil, int64p(4), true, float64(8))) },
	}
	for name, mutate := range cases {
		option := cloneJVMOption(jitOption(t))
		mutate(&option)
		if err := validateJVMOption(option); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	valid := cloneJVMOption(jitOption(t))
	setAuto(&valid, autoWith("logical_cpus", nil, int64p(4), true, float64(2)))
	if err := validateJVMOption(valid); err != nil {
		t.Fatalf("valid auto rule rejected: %v", err)
	}
	flag := JVMOption{
		ID: "string-dedup", Label: "Deduplicación", Template: "-XX:+UseStringDeduplication", Type: JVMOptionTypeBool,
		Help: "Activa la deduplicación.", Java: JavaRange{Min: 18}, VM: []string{"hotspot"},
		Platforms: map[string]JVMOptionPlatform{
			"pc":      {DefaultEnabled: false, Evidence: JVMEvidence{URL: "https://example.com/pc"}},
			"android": {DefaultEnabled: true, Evidence: JVMEvidence{URL: "https://example.com/android"}},
		},
	}
	if err := validateJVMOption(flag); err != nil {
		t.Fatalf("valid two-platform bool rejected: %v", err)
	}
	flag.Platforms["pc"] = JVMOptionPlatform{DefaultValue: true, Evidence: JVMEvidence{URL: "https://example.com/pc"}}
	if err := validateJVMOption(flag); err == nil {
		t.Fatal("bool option accepted a default value")
	}
}

func setAuto(option *JVMOption, auto *JVMOptionAuto) {
	platform := option.Platforms["pc"]
	platform.Auto = auto
	option.Platforms["pc"] = platform
}

func autoWith(fact string, min, max *int64, enabled bool, value any) *JVMOptionAuto {
	return &JVMOptionAuto{
		Rules:     []JVMAutoRule{{When: map[string]JVMFactRange{fact: {Min: min, Max: max}}, Enabled: enabled, Value: value}},
		Otherwise: JVMAutoDecision{Enabled: false},
	}
}

func TestJVMFlagCollisions(t *testing.T) {
	first := jitOption(t)
	second := cloneJVMOption(first)
	second.ID = "jit-compiler-threads-copy"
	if err := checkJVMFlagCollisions([]JVMOption{first, second}); err == nil {
		t.Fatal("two options setting CICompilerCount on pc accepted")
	}
	second.Platforms = map[string]JVMOptionPlatform{"android": first.Platforms["pc"]}
	if err := checkJVMFlagCollisions([]JVMOption{first, second}); err != nil {
		t.Fatalf("same flag on different platforms rejected: %v", err)
	}
}

func TestJVMOptionInheritance(t *testing.T) {
	root := fakeVerified("profile_root", "rev_0000000000000001", strings.Repeat("1", 64), nil, 8)
	root.manifest.SchemaVersion = 3
	root.manifest.JVMOptions = []JVMOption{jitOption(t)}
	pin := &ParentRef{ProfileID: root.ProfileID(), RevisionID: root.RevisionID(), ManifestSHA256: root.ManifestSHA256()}
	parents := map[RevisionKey]*VerifiedRevision{{ProfileID: root.ProfileID(), RevisionID: root.RevisionID()}: root}

	child := fakeVerified("profile_child", "rev_0000000000000002", strings.Repeat("2", 64), pin, 8)
	child.manifest.SchemaVersion = 3
	replaced := cloneJVMOption(jitOption(t))
	platform := replaced.Platforms["pc"]
	platform.DefaultEnabled = true
	replaced.Platforms["pc"] = platform
	child.manifest.JVMOptions = []JVMOption{replaced}
	chain, err := ValidateInheritanceChain(child, parents, 8)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := EffectiveJVMOptions(chain)
	if err != nil || len(effective) != 1 || !effective[0].Platforms["pc"].DefaultEnabled {
		t.Fatalf("child replacement not effective: %+v, %v", effective, err)
	}

	child.manifest.JVMOptions = nil
	child.manifest.RemoveJVMOptions = []string{"jit-compiler-threads"}
	effective, err = EffectiveJVMOptions([]*VerifiedRevision{child, root})
	if err != nil || len(effective) != 0 {
		t.Fatalf("removal not effective: %+v, %v", effective, err)
	}

	child.manifest.RemoveJVMOptions = []string{"not-in-parent"}
	if _, err := ValidateInheritanceChain(child, parents, 8); err == nil {
		t.Fatal("removal of an unknown option accepted")
	}

	conflicting := cloneJVMOption(jitOption(t))
	conflicting.ID = "other-jit"
	child.manifest.RemoveJVMOptions = nil
	child.manifest.JVMOptions = []JVMOption{conflicting}
	if _, err := ValidateInheritanceChain(child, parents, 8); err == nil {
		t.Fatal("inherited flag collision accepted")
	}
}
