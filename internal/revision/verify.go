package revision

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
)

var (
	ErrInvalidJSON      = errors.New("invalid JSON")
	ErrInvalidManifest  = errors.New("invalid revision manifest")
	ErrDigestMismatch   = errors.New("manifest digest mismatch")
	ErrInvalidSignature = errors.New("invalid Ed25519 signature")
	ErrInvalidPublicKey = errors.New("invalid Ed25519 public key")
	ErrInvalidEnvelope  = errors.New("invalid signed revision envelope")
)

func CanonicalizeJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, fmt.Errorf("%w: input is empty or not valid UTF-8", ErrInvalidJSON)
	}
	if err := validateJSONStringUnicode(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: RFC 8785 canonicalization failed: %v", ErrInvalidJSON, err)
	}
	return canonical, nil
}

func ParseAndVerifyRevisionEnvelope(raw []byte, publicKey ed25519.PublicKey) (*VerifiedRevision, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidPublicKey
	}
	if _, err := CanonicalizeJSON(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}

	var envelope SignedRevisionEnvelope
	if err := decodeStrict(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	if envelope.Canonicalization != CanonicalizationRFC8785 {
		return nil, fmt.Errorf("%w: unsupported canonicalization %q", ErrInvalidEnvelope, envelope.Canonicalization)
	}
	if err := validateSignature(envelope.Signature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	if !isSHA256(envelope.ManifestSHA256) {
		return nil, fmt.Errorf("%w: malformed manifest_sha256", ErrInvalidEnvelope)
	}

	manifest, canonical, err := parseAndValidateManifest(envelope.Manifest)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	digestHex := hex.EncodeToString(digest[:])
	if digestHex != envelope.ManifestSHA256 {
		return nil, ErrDigestMismatch
	}
	sig, err := base64.RawURLEncoding.DecodeString(envelope.Signature.Value)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature is not unpadded base64url Ed25519 bytes", ErrInvalidSignature)
	}
	if !ed25519.Verify(publicKey, canonical, sig) {
		return nil, ErrInvalidSignature
	}

	return &VerifiedRevision{
		manifest:  manifest,
		digest:    digestHex,
		canonical: append([]byte(nil), canonical...),
		signature: envelope.Signature,
	}, nil
}

func parseAndValidateManifest(raw []byte) (Manifest, []byte, error) {
	canonical, err := CanonicalizeJSON(raw)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := validateManifestShape(raw); err != nil {
		return Manifest{}, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	var manifest Manifest
	if err := decodeStrict(raw, &manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := validateManifestValues(manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	return manifest, canonical, nil
}

func decodeStrict(raw []byte, dst interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
