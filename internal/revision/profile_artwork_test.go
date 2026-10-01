package revision

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSignedProfileArtwork(t *testing.T) {
	pub, priv := testKey()
	var manifest Manifest
	if err := json.Unmarshal(testManifest("profile_artwork", "rev_0000000000000001", 1, nil), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Profile.Description = "A profile description"
	manifest.Profile.Icon = &ObjectRef{SHA256: strings.Repeat("a", 64), Size: 100, MediaType: "image/png"}
	raw, _ := json.Marshal(manifest)
	verified, err := ParseAndVerifyRevisionEnvelope(signRevisionEnvelope(t, raw, priv), pub)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Manifest().Profile.Icon.SHA256 != manifest.Profile.Icon.SHA256 || verified.Manifest().Profile.Description != manifest.Profile.Description {
		t.Fatal("signed artwork metadata lost")
	}
	for _, icon := range []ObjectRef{
		{SHA256: strings.Repeat("a", 64), Size: 2*1024*1024 + 1, MediaType: "image/png"},
		{SHA256: strings.Repeat("a", 64), Size: 100, MediaType: "image/svg+xml"},
		{SHA256: "invalid", Size: 100, MediaType: "image/png"},
	} {
		manifest.Profile.Icon = &icon
		if err := validateManifestValues(manifest); err == nil {
			t.Fatalf("accepted invalid icon: %+v", icon)
		}
	}
}
