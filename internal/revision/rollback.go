package revision

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidRollbackEvent = errors.New("invalid rollback event")
	ErrRollbackRequired     = errors.New("downgrade requires a valid signed rollback event")
	ErrAmbiguousHead        = errors.New("ambiguous channel head transition")
)

func ParseAndVerifyRollbackEvent(raw []byte, publicKey ed25519.PublicKey) (*VerifiedRollbackEvent, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidPublicKey
	}
	if _, err := CanonicalizeJSON(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRollbackEvent, err)
	}
	var event RollbackEvent
	if err := decodeStrict(raw, &event); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRollbackEvent, err)
	}
	if event.EventID == "" || !isSHA256(event.StatementSHA256) {
		return nil, fmt.Errorf("%w: event id or statement digest is invalid", ErrInvalidRollbackEvent)
	}
	if err := validateSignature(event.Signature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRollbackEvent, err)
	}
	statementFields, err := objectShape(event.Statement,
		[]string{"profile_id", "channel", "from_revision_id", "from_sequence", "to_revision_id", "reason", "issued_at"}, nil)
	if err != nil || len(statementFields) == 0 {
		return nil, fmt.Errorf("%w: malformed statement: %v", ErrInvalidRollbackEvent, err)
	}
	var statement RollbackStatement
	if err := decodeStrict(event.Statement, &statement); err != nil {
		return nil, fmt.Errorf("%w: malformed statement: %v", ErrInvalidRollbackEvent, err)
	}
	if !validProfileID(statement.ProfileID) || !validChannel(statement.Channel) || !validRevisionID(statement.FromRevisionID) || statement.FromSequence < 1 || !validRevisionID(statement.ToRevisionID) {
		return nil, fmt.Errorf("%w: statement identifiers are invalid", ErrInvalidRollbackEvent)
	}
	if len(statement.Reason) == 0 || len(statement.Reason) > 2048 {
		return nil, fmt.Errorf("%w: rollback reason must be 1..2048 bytes", ErrInvalidRollbackEvent)
	}
	if _, err := time.Parse(time.RFC3339Nano, statement.IssuedAt); err != nil {
		return nil, fmt.Errorf("%w: issued_at must be RFC3339 date-time", ErrInvalidRollbackEvent)
	}
	canonical, err := CanonicalizeJSON(event.Statement)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRollbackEvent, err)
	}
	digest := sha256.Sum256(canonical)
	digestHex := hex.EncodeToString(digest[:])
	if digestHex != event.StatementSHA256 {
		return nil, fmt.Errorf("%w: statement digest mismatch", ErrInvalidRollbackEvent)
	}
	sig, err := base64.RawURLEncoding.DecodeString(event.Signature.Value)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(publicKey, canonical, sig) {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRollbackEvent, ErrInvalidSignature)
	}
	return &VerifiedRollbackEvent{
		eventID: event.EventID, digest: digestHex, statement: statement,
		signature: event.Signature, canonical: append([]byte(nil), canonical...),
	}, nil
}

// DecideChannelTransition enforces monotonic acceptance. A same-sequence state
// is accepted only as an exact idempotent replay. A lower sequence requires a
// verified rollback whose signed from/to statement exactly binds this transition.
func DecideChannelTransition(known *ChannelState, candidate ChannelState, rollback *VerifiedRollbackEvent) error {
	if !validProfileID(candidate.ProfileID) || !validChannel(candidate.Channel) || !validRevisionID(candidate.RevisionID) || candidate.Sequence < 1 || !isSHA256(candidate.ManifestSHA256) {
		return ErrAmbiguousHead
	}
	if known == nil {
		if rollback != nil {
			return ErrAmbiguousHead
		}
		return nil
	}
	if known.ProfileID != candidate.ProfileID || known.Channel != candidate.Channel || !validRevisionID(known.RevisionID) || known.Sequence < 1 || !isSHA256(known.ManifestSHA256) {
		return ErrAmbiguousHead
	}
	if candidate.Sequence > known.Sequence {
		if rollback != nil {
			return ErrAmbiguousHead
		}
		return nil
	}
	if candidate.Sequence == known.Sequence {
		if rollback != nil || candidate.RevisionID != known.RevisionID || candidate.ManifestSHA256 != known.ManifestSHA256 {
			return ErrAmbiguousHead
		}
		return nil
	}
	if rollback == nil {
		return ErrRollbackRequired
	}
	statement := rollback.statement
	if statement.ProfileID != known.ProfileID || statement.Channel != known.Channel || statement.FromRevisionID != known.RevisionID || statement.FromSequence != known.Sequence || statement.ToRevisionID != candidate.RevisionID {
		return ErrAmbiguousHead
	}
	return nil
}
