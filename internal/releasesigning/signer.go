// Package releasesigning owns the server's durable publication identity.
package releasesigning

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
)

type Signer struct {
	private ed25519.PrivateKey
	id      string
	keys    map[string]ed25519.PublicKey
}

// Open creates a signing identity once in the persistent data directory. Existing
// malformed keys fail startup rather than silently changing the publication identity.
func Open(dataDir string) (*Signer, error) {
	dir := filepath.Join(dataDir, "signing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "server-release-key.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		raw, err = json.Marshal(struct {
			Private string `json:"private_key_base64url"`
		}{base64.RawURLEncoding.EncodeToString(private)})
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(err, os.ErrExist) {
			raw, err = os.ReadFile(path)
		} else if err == nil {
			_, err = file.Write(raw)
			if err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	var stored struct {
		Private string `json:"private_key_base64url"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid publication identity document")
	}
	private, err := base64.RawURLEncoding.DecodeString(stored.Private)
	if err != nil || len(private) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid server publication identity")
	}
	rebuilt := ed25519.NewKeyFromSeed(private[:ed25519.SeedSize])
	if !bytes.Equal(private, rebuilt) {
		return nil, errors.New("server publication identity is inconsistent")
	}
	public := rebuilt.Public().(ed25519.PublicKey)
	digest := sha256.Sum256(public)
	signer := &Signer{private: rebuilt, id: "server-" + hex.EncodeToString(digest[:16]), keys: make(map[string]ed25519.PublicKey)}
	historyPath := filepath.Join(dir, "public-keys.json")
	encoded := make(map[string]string)
	if history, err := os.ReadFile(historyPath); err == nil {
		if err := json.Unmarshal(history, &encoded); err != nil || encoded == nil || len(encoded) > 128 {
			return nil, errors.New("invalid publication public-key history")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for id, value := range encoded {
		key, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(key) != ed25519.PublicKeySize || id == "" {
			return nil, errors.New("invalid historical publication public key")
		}
		signer.keys[id] = ed25519.PublicKey(key)
	}
	if previous, exists := signer.keys[signer.id]; exists && !bytes.Equal(previous, public) {
		return nil, errors.New("publication key identity collision")
	}
	signer.keys[signer.id] = public
	encoded[signer.id] = base64.RawURLEncoding.EncodeToString(public)
	history, err := json.Marshal(encoded)
	if err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(dir, ".public-keys-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temporary.Name())
	_, err = temporary.Write(history)
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary.Name(), historyPath)
	}
	if err != nil {
		return nil, err
	}
	return signer, nil
}

// PublicKeys retains verification identities after an explicit key regeneration.
func (s *Signer) PublicKeys() map[string]ed25519.PublicKey {
	result := make(map[string]ed25519.PublicKey, len(s.keys))
	for id, key := range s.keys {
		result[id] = append(ed25519.PublicKey(nil), key...)
	}
	return result
}

func (s *Signer) KeyID() string { return s.id }
func (s *Signer) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), s.private.Public().(ed25519.PublicKey)...)
}

// SignRequest validates the schema and canonical digest, and returns the same
// signed envelope consumed by existing verification, storage and launcher code.
func (s *Signer) SignRequest(raw []byte) ([]byte, error) {
	var request struct {
		Canonicalization string          `json:"canonicalization"`
		Digest           string          `json:"manifest_sha256"`
		Manifest         json.RawMessage `json:"manifest"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, revision.ErrInvalidEnvelope
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, revision.ErrInvalidEnvelope
	}
	if request.Canonicalization != revision.CanonicalizationRFC8785 {
		return nil, revision.ErrInvalidEnvelope
	}
	canonical, err := revision.CanonicalizeValidatedManifest(request.Manifest)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	digestHex := hex.EncodeToString(digest[:])
	if request.Digest != digestHex {
		return nil, revision.ErrDigestMismatch
	}
	return json.Marshal(revision.SignedRevisionEnvelope{
		Canonicalization: revision.CanonicalizationRFC8785, ManifestSHA256: digestHex, Manifest: canonical,
		Signature: revision.Signature{KeyID: s.id, Algorithm: revision.SignatureAlgorithm, Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.private, canonical))},
	})
}
