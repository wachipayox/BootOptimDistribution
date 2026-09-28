// bootoptim-release-signer is an offline tool for creating release keys and
// signing Distribution revision requests. It is intentionally separate from
// the long-running Distribution service.
package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
)

type signingRequest struct {
	Canonicalization string          `json:"canonicalization"`
	ManifestSHA256   string          `json:"manifest_sha256"`
	Manifest         json.RawMessage `json:"manifest"`
}

type privateKeyFile struct {
	SchemaVersion       int    `json:"schema_version"`
	KeyID               string `json:"key_id"`
	Algorithm           string `json:"algorithm"`
	PrivateKeyBase64URL string `json:"private_key_base64url"`
	PublicKeyBase64URL  string `json:"public_key_base64url"`
}

type signedEnvelope struct {
	Canonicalization string             `json:"canonicalization"`
	ManifestSHA256   string             `json:"manifest_sha256"`
	Manifest         json.RawMessage    `json:"manifest"`
	Signature        revision.Signature `json:"signature"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = runKeygen(os.Args[2:])
	case "sign":
		err = runSign(os.Args[2:], os.Stdin, os.Stderr)
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootoptim-release-signer:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  bootoptim-release-signer keygen --key-id ID --private-out private-key.json --public-out release-public-keys.json
  bootoptim-release-signer sign --key private-key.json --request profile.signing-request.json --out profile.signed-envelope.json

Key generation and signing run locally. Never copy the private-key file to the Distribution server.`)
}

func runKeygen(args []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	keyID := flags.String("key-id", "", "stable identifier for this signing key")
	privateOut := flags.String("private-out", "", "new local private key file; must not exist")
	publicOut := flags.String("public-out", "", "new JSON public-key map; must not exist")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*keyID) == "" || len(*keyID) > 128 || strings.TrimSpace(*keyID) != *keyID {
		return errors.New("--key-id must contain 1..128 non-whitespace-edge characters")
	}
	if *privateOut == "" || *publicOut == "" || filepath.Clean(*privateOut) == filepath.Clean(*publicOut) {
		return errors.New("--private-out and --public-out must be distinct file paths")
	}
	if _, err := os.Stat(*privateOut); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("refusing to overwrite %s", *privateOut)
		}
		return err
	}
	if _, err := os.Stat(*publicOut); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("refusing to overwrite %s", *publicOut)
		}
		return err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	encodedPrivate := base64.RawURLEncoding.EncodeToString(private)
	encodedPublic := base64.RawURLEncoding.EncodeToString(public)
	privateJSON, err := json.MarshalIndent(privateKeyFile{
		SchemaVersion: 1, KeyID: *keyID, Algorithm: revision.SignatureAlgorithm,
		PrivateKeyBase64URL: encodedPrivate, PublicKeyBase64URL: encodedPublic,
	}, "", "  ")
	if err != nil {
		return err
	}
	publicJSON, err := json.MarshalIndent(map[string]string{*keyID: encodedPublic}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNewFile(*privateOut, append(privateJSON, '\n'), 0600); err != nil {
		return err
	}
	if err := writeNewFile(*publicOut, append(publicJSON, '\n'), 0644); err != nil {
		_ = os.Remove(*privateOut)
		return err
	}
	fmt.Fprintf(os.Stdout, "Created local Ed25519 signer %q.\nPublic key: %s\nPrivate key saved to: %s\nKeep an encrypted offline backup of that private-key file.\n", *keyID, encodedPublic, *privateOut)
	return nil
}

func runSign(args []string, input io.Reader, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyPath := flags.String("key", "", "local private key JSON")
	requestPath := flags.String("request", "", "canonical signing request downloaded from the admin panel")
	outPath := flags.String("out", "", "new signed envelope path; must not exist")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *requestPath == "" || *outPath == "" {
		return errors.New("--key, --request and --out are required")
	}
	if _, err := os.Stat(*outPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("refusing to overwrite %s", *outPath)
		}
		return err
	}
	keyBytes, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	var keyFile privateKeyFile
	if err := decodeStrict(keyBytes, &keyFile); err != nil {
		return fmt.Errorf("read private key: %w", err)
	}
	if keyFile.SchemaVersion != 1 || keyFile.Algorithm != revision.SignatureAlgorithm || keyFile.KeyID == "" {
		return errors.New("unsupported private-key file")
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(keyFile.PrivateKeyBase64URL)
	if err != nil || len(privateBytes) != ed25519.PrivateKeySize {
		return errors.New("private key must be an unpadded base64url Ed25519 private key")
	}
	publicBytes, err := base64.RawURLEncoding.DecodeString(keyFile.PublicKeyBase64URL)
	if err != nil || len(publicBytes) != ed25519.PublicKeySize {
		return errors.New("public key must be a 32-byte unpadded base64url Ed25519 key")
	}
	private := ed25519.PrivateKey(privateBytes)
	if !bytes.Equal(private.Public().(ed25519.PublicKey), publicBytes) {
		return errors.New("private and public keys do not match")
	}

	requestBytes, err := os.ReadFile(*requestPath)
	if err != nil {
		return err
	}
	var request signingRequest
	if err := decodeStrict(requestBytes, &request); err != nil {
		return fmt.Errorf("read signing request: %w", err)
	}
	if request.Canonicalization != revision.CanonicalizationRFC8785 {
		return errors.New("unsupported signing request canonicalization")
	}
	canonicalManifest, err := revision.CanonicalizeValidatedManifest(request.Manifest)
	if err != nil {
		return fmt.Errorf("canonicalize manifest: %w", err)
	}
	digest := sha256.Sum256(canonicalManifest)
	digestHex := hex.EncodeToString(digest[:])
	if digestHex != request.ManifestSHA256 {
		return errors.New("manifest digest does not match signing request")
	}
	var identity struct {
		Profile  struct{ ID, Name string } `json:"profile"`
		Revision struct {
			ID       string
			Sequence int64
		} `json:"revision"`
		Game struct {
			Minecraft string `json:"minecraft"`
			NeoForge  string `json:"neoforge"`
		} `json:"game"`
		Base *struct {
			ProfileID      string `json:"profile_id"`
			RevisionID     string `json:"revision_id"`
			ManifestSHA256 string `json:"manifest_sha256"`
		} `json:"base"`
		Mods           []json.RawMessage `json:"mods"`
		RemoveMods     []json.RawMessage `json:"remove_mods"`
		Configs        []json.RawMessage `json:"configs"`
		RemoveConfigs  []json.RawMessage `json:"remove_configs"`
		ConfigSettings []json.RawMessage `json:"config_settings"`
		Objects        []json.RawMessage `json:"objects"`
	}
	if err := json.Unmarshal(request.Manifest, &identity); err != nil {
		return err
	}
	if identity.Profile.ID == "" || identity.Revision.ID == "" || identity.Revision.Sequence < 1 {
		return errors.New("manifest identity is incomplete")
	}
	parent := "none (root profile)"
	if identity.Base != nil {
		parent = fmt.Sprintf("%s@%s (%s)", identity.Base.ProfileID, identity.Base.RevisionID, identity.Base.ManifestSHA256)
	}
	fmt.Fprintf(diagnostics,
		"About to sign profile %q (%s), revision %s sequence %d, parent %s, Minecraft %s / NeoForge %s; files: %d mods, %d removed mods, %d configs, %d removed configs, %d setting rules, %d other objects; digest %s.\nType the profile ID to confirm: ",
		identity.Profile.Name, identity.Profile.ID, identity.Revision.ID, identity.Revision.Sequence, parent,
		identity.Game.Minecraft, identity.Game.NeoForge,
		len(identity.Mods), len(identity.RemoveMods), len(identity.Configs), len(identity.RemoveConfigs), len(identity.ConfigSettings), len(identity.Objects), digestHex,
	)
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(line) != identity.Profile.ID {
		return errors.New("profile ID confirmation did not match; nothing was signed")
	}
	envelope := signedEnvelope{
		Canonicalization: revision.CanonicalizationRFC8785,
		ManifestSHA256:   digestHex,
		Manifest:         append(json.RawMessage(nil), request.Manifest...),
		Signature: revision.Signature{
			KeyID: keyFile.KeyID, Algorithm: revision.SignatureAlgorithm,
			Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, canonicalManifest)),
		},
	}
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNewFile(*outPath, append(out, '\n'), 0644); err != nil {
		return err
	}
	fmt.Fprintf(diagnostics, "Signed envelope written to %s. Private key was not copied.\n", *outPath)
	return nil
}

func decodeStrict(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}
