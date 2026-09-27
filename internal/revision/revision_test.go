package revision

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
)

func TestCanonicalizeRFC8785Vector(t *testing.T) {
	raw := []byte(`{"z":1,"a":"\u20ac","n":1E+2}`)
	got, err := CanonicalizeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"€","n":100,"z":1}`
	if string(got) != want {
		t.Fatalf("canonical JSON = %s, want %s", got, want)
	}
}

func TestRevisionSignatureAndTampering(t *testing.T) {
	pub, priv := testKey()
	root := testManifest("profile_root", "rev_0000000000000001", 10, nil)
	envelope := signRevisionEnvelope(t, root, priv)
	verified, err := ParseAndVerifyRevisionEnvelope(envelope, pub)
	if err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	var wire map[string]interface{}
	if err := json.Unmarshal(envelope, &wire); err != nil {
		t.Fatal(err)
	}
	wire["signature"].(map[string]interface{})["value"] = base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	alteredSignature, _ := json.Marshal(wire)
	if _, err := ParseAndVerifyRevisionEnvelope(alteredSignature, pub); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("altered signature error = %v", err)
	}

	if err := json.Unmarshal(envelope, &wire); err != nil {
		t.Fatal(err)
	}
	wire["manifest_sha256"] = strings.Repeat("0", 64)
	alteredDigest, _ := json.Marshal(wire)
	if _, err := ParseAndVerifyRevisionEnvelope(alteredDigest, pub); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("altered digest error = %v", err)
	}

	if verified.Manifest().Revision.Sequence != 10 {
		t.Fatal("verified revision did not retain signed sequence")
	}
}

func TestPinnedParentChangedIsRejected(t *testing.T) {
	pub, priv := testKey()
	rootRaw := testManifest("profile_root", "rev_0000000000000001", 1, nil)
	root := mustVerify(t, rootRaw, priv, pub)
	pin := &ParentRef{ProfileID: root.ProfileID(), RevisionID: root.RevisionID(), ManifestSHA256: root.ManifestSHA256()}
	child := mustVerify(t, testManifest("profile_child", "rev_0000000000000002", 2, pin), priv, pub)

	changedRaw := bytesReplaceOnce(rootRaw, `"name":"Test profile"`, `"name":"Changed profile"`)
	changed := mustVerify(t, changedRaw, priv, pub)
	parents := map[RevisionKey]*VerifiedRevision{{ProfileID: changed.ProfileID(), RevisionID: changed.RevisionID()}: changed}
	if _, err := ValidateInheritanceChain(child, parents, 8); !errors.Is(err, ErrParentPinMismatch) {
		t.Fatalf("changed parent error = %v", err)
	}
}

func TestInheritanceCycleAndDepth(t *testing.T) {
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	a := fakeVerified("profile_cycle_a", "rev_0000000000000001", digestA, &ParentRef{ProfileID: "profile_cycle_b", RevisionID: "rev_0000000000000002", ManifestSHA256: digestB}, 8)
	b := fakeVerified("profile_cycle_b", "rev_0000000000000002", digestB, &ParentRef{ProfileID: "profile_cycle_a", RevisionID: "rev_0000000000000001", ManifestSHA256: digestA}, 8)
	parents := map[RevisionKey]*VerifiedRevision{
		{ProfileID: b.ProfileID(), RevisionID: b.RevisionID()}: b,
		{ProfileID: a.ProfileID(), RevisionID: a.RevisionID()}: a,
	}
	if _, err := ValidateInheritanceChain(a, parents, 8); !errors.Is(err, ErrInheritanceCycle) {
		t.Fatalf("cycle error = %v", err)
	}

	chain := make([]*VerifiedRevision, 10)
	parents = make(map[RevisionKey]*VerifiedRevision)
	for i := 9; i >= 0; i-- {
		id := fmt.Sprintf("rev_%016d", i+1)
		profile := fmt.Sprintf("profile_depth_%02d", i)
		digest := fmt.Sprintf("%064x", i+1)
		var pin *ParentRef
		if i < 9 {
			pin = &ParentRef{ProfileID: chain[i+1].ProfileID(), RevisionID: chain[i+1].RevisionID(), ManifestSHA256: chain[i+1].ManifestSHA256()}
		}
		chain[i] = fakeVerified(profile, id, digest, pin, 8)
		parents[RevisionKey{ProfileID: profile, RevisionID: id}] = chain[i]
	}
	if _, err := ValidateInheritanceChain(chain[0], parents, 8); !errors.Is(err, ErrInheritanceTooDeep) {
		t.Fatalf("depth error = %v", err)
	}
}

func TestConfigSettingValidationAndInheritedOverridePermissions(t *testing.T) {
	digest := strings.Repeat("a", 64)
	root := fakeVerified("profile_root", "rev_0000000000000001", strings.Repeat("1", 64), nil, 8)
	root.manifest.SchemaVersion = 2
	root.manifest.Configs = []ConfigEntry{{
		Path: "config/example.toml", Object: ObjectRef{SHA256: digest, Size: 10}, Policy: "default_once",
	}}
	root.manifest.ConfigSettings = []ConfigSetting{{
		Path: "config/example.toml", Format: "toml", Key: "video.render_distance", Value: float64(12), Policy: "enforced",
	}}
	root.manifest.Permissions.Configs.OverrideEnforced = true

	child := fakeVerified("profile_child", "rev_0000000000000002", strings.Repeat("2", 64), &ParentRef{
		ProfileID: root.ProfileID(), RevisionID: root.RevisionID(), ManifestSHA256: root.ManifestSHA256(),
	}, 8)
	child.manifest.SchemaVersion = 2
	child.manifest.ConfigSettings = []ConfigSetting{{
		Path: "config/example.toml", Format: "toml", Key: "video.render_distance", Value: float64(16), Policy: "enforced",
	}}
	parents := map[RevisionKey]*VerifiedRevision{{ProfileID: root.ProfileID(), RevisionID: root.RevisionID()}: root}
	if _, err := ValidateInheritanceChain(child, parents, 8); err != nil {
		t.Fatalf("permitted inherited config override rejected: %v", err)
	}

	root.manifest.Permissions.Configs.OverrideEnforced = false
	if _, err := ValidateInheritanceChain(child, parents, 8); err == nil || !strings.Contains(err.Error(), "does not permit") {
		t.Fatalf("unpermitted inherited config override error = %v", err)
	}

	child.manifest.ConfigSettings[0].Path = "config/missing.toml"
	if _, err := ValidateInheritanceChain(child, parents, 8); err == nil || !strings.Contains(err.Error(), "absent from the effective profile") {
		t.Fatalf("missing config setting path error = %v", err)
	}
}

func TestConfigSettingSelectorsRejectUnsupportedShapes(t *testing.T) {
	valid := ConfigSetting{Path: "config/options.toml", Format: "toml", Key: "graphics.enabled", Value: true, Policy: "enforced"}
	if err := validateConfigSetting(valid); err != nil {
		t.Fatalf("valid TOML rule rejected: %v", err)
	}
	invalid := []ConfigSetting{
		{Path: "config/options.toml", Format: "toml", Key: "graphics..enabled", Value: true, Policy: "enforced"},
		{Path: "config/options.toml", Format: "toml", Key: "graphics.enabled", Value: map[string]any{"nested": true}, Policy: "enforced"},
		{Path: "config/options.properties", Format: "properties", Key: "escaped\\ key", Value: "value", Policy: "default_once"},
		{Path: "config/readme.txt", Format: "text_lines", Key: "line:0", Value: "value", Policy: "enforced"},
		{Path: "config/readme.txt", Format: "text_lines", Key: "line:01", Value: "value", Policy: "enforced"},
	}
	for _, setting := range invalid {
		if err := validateConfigSetting(setting); err == nil {
			t.Errorf("invalid selector accepted: %+v", setting)
		}
	}
}

func TestDowngradeRequiresValidSignedRollback(t *testing.T) {
	pub, priv := testKey()
	known := &ChannelState{ProfileID: "profile_root", Channel: "stable", RevisionID: "rev_0000000000000010", Sequence: 10, ManifestSHA256: strings.Repeat("a", 64)}
	candidate := ChannelState{ProfileID: "profile_root", Channel: "stable", RevisionID: "rev_0000000000000007", Sequence: 7, ManifestSHA256: strings.Repeat("b", 64)}
	if err := DecideChannelTransition(known, candidate, nil); !errors.Is(err, ErrRollbackRequired) {
		t.Fatalf("downgrade without event error = %v", err)
	}

	eventRaw := signRollbackEvent(t, RollbackStatement{
		ProfileID: known.ProfileID, Channel: known.Channel,
		FromRevisionID: known.RevisionID, FromSequence: known.Sequence,
		ToRevisionID: candidate.RevisionID, Reason: "known bad release", IssuedAt: "2026-09-17T00:00:00Z",
	}, priv)
	event, err := ParseAndVerifyRollbackEvent(eventRaw, pub)
	if err != nil {
		t.Fatalf("valid rollback rejected: %v", err)
	}
	if err := DecideChannelTransition(known, candidate, event); err != nil {
		t.Fatalf("signed rollback transition rejected: %v", err)
	}

	candidate.RevisionID = "rev_0000000000000006"
	if err := DecideChannelTransition(known, candidate, event); !errors.Is(err, ErrAmbiguousHead) {
		t.Fatalf("event reused for different target error = %v", err)
	}
}

func testKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := sha256.Sum256([]byte("BootOptimDistribution agent191 deterministic test key only"))
	priv := ed25519.NewKeyFromSeed(seed[:])
	return priv.Public().(ed25519.PublicKey), priv
}

func testManifest(profileID, revisionID string, sequence int64, base *ParentRef) []byte {
	manifest := Manifest{
		SchemaVersion: 1,
		Revision:      Revision{ID: revisionID, Sequence: sequence, CreatedAt: "2026-09-17T00:00:00Z"},
		Profile:       Profile{ID: profileID, Name: "Test profile", Official: true},
		Game:          Game{Minecraft: "1.21.1", NeoForge: "21.1.0"},
		Base:          base,
		Permissions:   Permissions{DeriveLocal: true, Mods: ModPermissions{Add: true, Remove: true}, Configs: ConfigPermissions{OverrideEnforced: false, OverrideDefaultOnce: true}, MaxInheritanceDepth: 8},
		Mods:          []ModEntry{}, RemoveMods: []RemoveMod{}, Configs: []ConfigEntry{}, RemoveConfigs: []RemoveConfig{}, Objects: []ObjectEntry{},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	return raw
}

func signRevisionEnvelope(t *testing.T, manifest []byte, priv ed25519.PrivateKey) []byte {
	t.Helper()
	canonical, err := jcs.Transform(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	envelope := SignedRevisionEnvelope{
		Canonicalization: CanonicalizationRFC8785,
		ManifestSHA256:   hex.EncodeToString(digest[:]),
		Manifest:         manifest,
		Signature:        Signature{KeyID: "test-release-key", Algorithm: SignatureAlgorithm, Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, canonical))},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustVerify(t *testing.T, manifest []byte, priv ed25519.PrivateKey, pub ed25519.PublicKey) *VerifiedRevision {
	t.Helper()
	verified, err := ParseAndVerifyRevisionEnvelope(signRevisionEnvelope(t, manifest, priv), pub)
	if err != nil {
		t.Fatal(err)
	}
	return verified
}

func signRollbackEvent(t *testing.T, statement RollbackStatement, priv ed25519.PrivateKey) []byte {
	t.Helper()
	statementRaw, err := json.Marshal(statement)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(statementRaw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	event := RollbackEvent{
		EventID: "rollback-test-0001", StatementSHA256: hex.EncodeToString(digest[:]), Statement: statementRaw,
		Signature: Signature{KeyID: "test-release-key", Algorithm: SignatureAlgorithm, Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, canonical))},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fakeVerified(profileID, revisionID, digest string, base *ParentRef, maxDepth int) *VerifiedRevision {
	return &VerifiedRevision{manifest: Manifest{
		SchemaVersion: 1,
		Profile:       Profile{ID: profileID, Official: true},
		Revision:      Revision{ID: revisionID, Sequence: 1},
		Game:          Game{Minecraft: "1.21.1", NeoForge: "21.1.0"},
		Base:          base, Permissions: Permissions{MaxInheritanceDepth: maxDepth},
	}, digest: digest}
}

func bytesReplaceOnce(raw []byte, old, replacement string) []byte {
	return []byte(strings.Replace(string(raw), old, replacement, 1))
}
