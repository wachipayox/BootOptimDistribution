package revision

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Java runtime choice of a profile (profile.java_runtime, 2026-11-10).
//
// The field lives inside the signed "profile" object instead of the manifest
// root so it needs no new schema_version: the Android launcher 1.0.0 rejects
// unknown root fields and schema versions above 4, but does not restrict the
// keys of "profile". A launcher that does not know the field keeps using the
// Java its version manifest asks for, which is the documented default.
//
// Each platform key is resolved independently along the inheritance chain:
// the nearest revision (child first) that names the platform wins; when none
// does, the launcher uses Mojang's default Java.
const (
	JavaRuntimeSourceMojang  = "mojang"
	JavaRuntimeSourceProfile = "profile"

	JavaRuntimePlatformPC      = "pc"
	JavaRuntimePlatformAndroid = "android"

	// Java feature-release numbers accepted for a recommended runtime.
	MinJavaRuntimeMajor = 8
	MaxJavaRuntimeMajor = 99
)

type JavaRuntime struct {
	PC      *JavaRuntimeChoice `json:"pc,omitempty"`
	Android *JavaRuntimeChoice `json:"android,omitempty"`
}

// JavaRuntimeChoice is {"source":"mojang"} or {"source":"profile","major":N}.
type JavaRuntimeChoice struct {
	Source string `json:"source"`
	Major  int    `json:"major,omitempty"`
}

// EffectiveJavaRuntimeChoice is the resolved value for one platform.
// FromProfileID is empty when no revision of the chain names the platform.
type EffectiveJavaRuntimeChoice struct {
	Source        string `json:"source"`
	Major         int    `json:"major,omitempty"`
	FromProfileID string `json:"from_profile_id,omitempty"`
}

type EffectiveJavaRuntime struct {
	PC      EffectiveJavaRuntimeChoice `json:"pc"`
	Android EffectiveJavaRuntimeChoice `json:"android"`
}

func (r *JavaRuntime) clone() *JavaRuntime {
	if r == nil {
		return nil
	}
	out := &JavaRuntime{}
	if r.PC != nil {
		pc := *r.PC
		out.PC = &pc
	}
	if r.Android != nil {
		android := *r.Android
		out.Android = &android
	}
	return out
}

func (r *JavaRuntime) choice(platform string) *JavaRuntimeChoice {
	if r == nil {
		return nil
	}
	switch platform {
	case JavaRuntimePlatformPC:
		return r.PC
	case JavaRuntimePlatformAndroid:
		return r.Android
	}
	return nil
}

func validateJavaRuntime(r *JavaRuntime) error {
	if r == nil {
		return nil
	}
	if r.PC == nil && r.Android == nil {
		return errors.New("profile.java_runtime must name pc and/or android; omit it to inherit")
	}
	for _, platform := range []string{JavaRuntimePlatformPC, JavaRuntimePlatformAndroid} {
		if err := validateJavaRuntimeChoice(r.choice(platform)); err != nil {
			return fmt.Errorf("profile.java_runtime.%s: %v", platform, err)
		}
	}
	return nil
}

func validateJavaRuntimeChoice(c *JavaRuntimeChoice) error {
	if c == nil {
		return nil
	}
	switch c.Source {
	case JavaRuntimeSourceMojang:
		if c.Major != 0 {
			return errors.New("source mojang cannot carry major")
		}
	case JavaRuntimeSourceProfile:
		if c.Major < MinJavaRuntimeMajor || c.Major > MaxJavaRuntimeMajor {
			return fmt.Errorf("major must be %d..%d", MinJavaRuntimeMajor, MaxJavaRuntimeMajor)
		}
	default:
		return errors.New(`source must be "mojang" or "profile"`)
	}
	return nil
}

// javaRuntimeShape checks the raw JSON of profile.java_runtime before the
// strict decode, so "major": 25.0, nulls and unknown keys are rejected with a
// clear message.
func javaRuntimeShape(raw json.RawMessage) error {
	if string(bytes.TrimSpace(raw)) == "null" {
		return errors.New("profile.java_runtime cannot be null; omit it to inherit")
	}
	platforms, err := objectShape(raw, nil, []string{JavaRuntimePlatformPC, JavaRuntimePlatformAndroid})
	if err != nil {
		return fmt.Errorf("profile.java_runtime: %w", err)
	}
	for name, value := range platforms {
		choice, err := objectShape(value, []string{"source"}, []string{"major"})
		if err != nil {
			return fmt.Errorf("profile.java_runtime.%s: %w", name, err)
		}
		if err := requireString(choice["source"], "profile.java_runtime."+name+".source"); err != nil {
			return err
		}
		if major, ok := choice["major"]; ok {
			if err := requireInteger(major, "profile.java_runtime."+name+".major"); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResolveJavaRuntime resolves the Java choice of every platform. chain is
// ordered as ValidateInheritanceChain returns it: target first, root last.
func ResolveJavaRuntime(chain []*VerifiedRevision) EffectiveJavaRuntime {
	manifests := make([]Manifest, 0, len(chain))
	for _, item := range chain {
		manifests = append(manifests, item.manifest)
	}
	return ResolveJavaRuntimeManifests(manifests)
}

// ResolveJavaRuntimeManifests is ResolveJavaRuntime over plain manifests,
// target first, root last.
func ResolveJavaRuntimeManifests(chain []Manifest) EffectiveJavaRuntime {
	resolve := func(platform string) EffectiveJavaRuntimeChoice {
		for _, manifest := range chain {
			if c := manifest.Profile.JavaRuntime.choice(platform); c != nil {
				return EffectiveJavaRuntimeChoice{Source: c.Source, Major: c.Major, FromProfileID: manifest.Profile.ID}
			}
		}
		return EffectiveJavaRuntimeChoice{Source: JavaRuntimeSourceMojang}
	}
	return EffectiveJavaRuntime{PC: resolve(JavaRuntimePlatformPC), Android: resolve(JavaRuntimePlatformAndroid)}
}
