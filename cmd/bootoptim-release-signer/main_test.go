package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
)

func TestKeygenThenSignRequiresIdentityConfirmation(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.json")
	publicPath := filepath.Join(dir, "trusted-public-keys.json")
	if err := runKeygen([]string{"--key-id", "test-recovery-key", "--private-out", privatePath, "--public-out", publicPath}); err != nil {
		t.Fatal(err)
	}
	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	var key privateKeyFile
	if err := decodeStrict(privateRaw, &key); err != nil {
		t.Fatal(err)
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(key.PrivateKeyBase64URL)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := base64.RawURLEncoding.DecodeString(key.PublicKeyBase64URL)
	if err != nil {
		t.Fatal(err)
	}

	manifestValue := revision.Manifest{
		SchemaVersion: 1,
		Profile:       revision.Profile{ID: "profile_synthetic", Name: "Synthetic", Official: true},
		Revision:      revision.Revision{ID: "rev_0123456789abcdef", Sequence: 1, CreatedAt: "2026-09-28T00:00:00Z"},
		Game:          revision.Game{Minecraft: "1.21.1", NeoForge: "21.1.0"},
		Permissions:   revision.Permissions{DeriveLocal: true, MaxInheritanceDepth: 8},
		Mods:          []revision.ModEntry{}, RemoveMods: []revision.RemoveMod{},
		Configs: []revision.ConfigEntry{}, RemoveConfigs: []revision.RemoveConfig{}, Objects: []revision.ObjectEntry{},
	}
	manifestBytes, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest := json.RawMessage(manifestBytes)
	canonical, err := revision.CanonicalizeJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	request, err := json.Marshal(signingRequest{
		Canonicalization: revision.CanonicalizationRFC8785,
		ManifestSHA256:   hex.EncodeToString(digest[:]),
		Manifest:         manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(requestPath, request, 0600); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "signed.json")
	var diagnostics bytes.Buffer
	wrongConfirmation := bytes.NewBufferString("profile_other\n")
	if err := runSign([]string{"--key", privatePath, "--request", requestPath, "--out", outPath}, wrongConfirmation, &diagnostics); err == nil {
		t.Fatal("expected mismatched confirmation to reject signing")
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatalf("signed file exists after rejected confirmation: %v", err)
	}

	diagnostics.Reset()
	if err := runSign([]string{"--key", privatePath, "--request", requestPath, "--out", outPath}, bytes.NewBufferString("profile_synthetic\n"), &diagnostics); err != nil {
		t.Fatal(err)
	}
	var envelope signedEnvelope
	envelopeRaw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeStrict(envelopeRaw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Signature.KeyID != "test-recovery-key" || envelope.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("unexpected envelope metadata: %+v", envelope)
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(ed25519.PublicKey(publicBytes), canonical, signature) {
		t.Fatal("generated envelope signature did not verify")
	}
	if !bytes.Equal(ed25519.PrivateKey(privateBytes).Public().(ed25519.PublicKey), publicBytes) {
		t.Fatal("generated key pair does not match")
	}
}

func TestKeygenRefusesToOverwritePrivateKey(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.json")
	publicPath := filepath.Join(dir, "public.json")
	if err := os.WriteFile(privatePath, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runKeygen([]string{"--key-id", "test-key", "--private-out", privatePath, "--public-out", publicPath}); err == nil {
		t.Fatal("expected existing private key to be protected")
	}
	contents, err := os.ReadFile(privatePath)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("existing private key changed: contents=%q err=%v", contents, err)
	}
}
