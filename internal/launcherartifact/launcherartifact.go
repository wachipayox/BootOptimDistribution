// Package launcherartifact defines the signed statement that binds one
// launcher binary (app, version, platform, sha256, size) to the server's
// publication key, and the reference verification used by tests and clients.
package launcherartifact

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
)

// Type is the statement discriminator; it keeps a launcher statement from
// being confused with a revision manifest or a rollback statement.
const Type = "wbombo.launcher-artifact.v1"

var ErrInvalidStatement = errors.New("invalid launcher artifact statement")
var ErrBadSignature = errors.New("launcher artifact signature does not verify")

// Statement is the signed payload. Its canonical form is RFC 8785 JSON.
type Statement struct {
	Type     string `json:"type"`
	App      string `json:"app"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	IssuedAt string `json:"issued_at"`
}

// Canonical returns the RFC 8785 canonical JSON bytes of the statement.
func (s Statement) Canonical() ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return revision.CanonicalizeJSON(raw)
}

// Encode returns the canonical bytes and their unpadded base64url form, which
// is what the API publishes as "statement".
func (s Statement) Encode() (canonical []byte, encoded string, err error) {
	canonical, err = s.Canonical()
	if err != nil {
		return nil, "", err
	}
	return canonical, base64.RawURLEncoding.EncodeToString(canonical), nil
}

// Expect lists what a client asked for. Empty fields are not checked.
type Expect struct {
	App, Version, Platform, SHA256 string
	Size                           int64 // 0 = not checked
}

// Verify checks, in this order: the signature over the exact statement bytes,
// that those bytes are the canonical form of a statement with the right type
// and no unknown fields, and that every non-empty Expect field matches.
func Verify(encodedStatement string, sig revision.Signature, key ed25519.PublicKey, want Expect) (Statement, error) {
	if sig.Algorithm != revision.SignatureAlgorithm || len(key) != ed25519.PublicKeySize {
		return Statement{}, ErrBadSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(encodedStatement)
	if err != nil {
		return Statement{}, fmt.Errorf("%w: statement is not unpadded base64url", ErrInvalidStatement)
	}
	signature, err := base64.RawURLEncoding.DecodeString(sig.Value)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, raw, signature) {
		return Statement{}, ErrBadSignature
	}
	var st Statement
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil || dec.Decode(new(any)) != io.EOF {
		return Statement{}, fmt.Errorf("%w: not a statement object", ErrInvalidStatement)
	}
	canonical, err := st.Canonical()
	if err != nil || !bytes.Equal(canonical, raw) {
		return Statement{}, fmt.Errorf("%w: statement is not canonical", ErrInvalidStatement)
	}
	if st.Type != Type {
		return Statement{}, fmt.Errorf("%w: type %q", ErrInvalidStatement, st.Type)
	}
	switch {
	case want.App != "" && st.App != want.App:
		return Statement{}, fmt.Errorf("%w: app %q", ErrInvalidStatement, st.App)
	case want.Version != "" && st.Version != want.Version:
		return Statement{}, fmt.Errorf("%w: version %q", ErrInvalidStatement, st.Version)
	case want.Platform != "" && st.Platform != want.Platform:
		return Statement{}, fmt.Errorf("%w: platform %q", ErrInvalidStatement, st.Platform)
	case want.SHA256 != "" && st.SHA256 != want.SHA256:
		return Statement{}, fmt.Errorf("%w: sha256 mismatch", ErrInvalidStatement)
	case want.Size != 0 && st.Size != want.Size:
		return Statement{}, fmt.Errorf("%w: size mismatch", ErrInvalidStatement)
	}
	return st, nil
}
