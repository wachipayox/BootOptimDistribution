package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

var (
	ErrChannelConflict = errors.New("channel compare-and-swap conflict")
)

type StoredSignedRevision struct {
	StoredRevision
	Envelope []byte
}

type ChannelRecord struct {
	ProfileID      string
	Channel        string
	RevisionID     string
	Sequence       int64
	ManifestSHA256 string
	UpdatedAt      time.Time
}

// Open returns a regular CAS object by digest. Callers that expose content over
// HTTP must separately prove that the object is published; staged objects are
// intentionally present in CAS before they become visible metadata.
func (c *CAS) Open(ctx context.Context, digest string) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	path, err := c.objectPath(digest, false)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, fmt.Errorf("%w: %s", ErrObjectMissing, digest)
	}
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%w: non-regular CAS entry", ErrCorruptObject)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *SQLiteStore) ensureProfileAPISchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, profileAPISchema)
	if err != nil {
		return fmt.Errorf("initialize profile API storage: %w", err)
	}
	return nil
}

// PublishSignedRevision is the HTTP publication storage boundary. It mirrors
// PublishRevision's defensive object re-checks but records the already-verified
// signed envelope in the same transaction as the immutable revision metadata.
// Cryptographic parsing and policy decisions deliberately stay outside storage.
func (s *SQLiteStore) PublishSignedRevision(ctx context.Context, publication RevisionPublication, envelope []byte) error {
	if len(envelope) == 0 {
		return fmt.Errorf("%w: envelope is empty", ErrInvalidPublication)
	}
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return err
	}
	return s.publishRevision(ctx, publication, publicationHooks{
		existing: func(ctx context.Context, tx *sql.Tx, publication RevisionPublication) error {
			var storedEnvelope []byte
			err := tx.QueryRowContext(ctx,
				`SELECT envelope FROM revision_envelopes WHERE revision_id = ?`,
				publication.RevisionID).Scan(&storedEnvelope)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrImmutableConflict
			}
			if err != nil {
				return err
			}
			if !bytes.Equal(storedEnvelope, envelope) {
				return ErrImmutableConflict
			}
			return nil
		},
		beforeInsert: func(ctx context.Context, tx *sql.Tx, publication RevisionPublication) error {
			var maxSequence sql.NullInt64
			if err := tx.QueryRowContext(ctx,
				`SELECT MAX(sequence) FROM revisions WHERE profile_id = ?`,
				publication.ProfileID).Scan(&maxSequence); err != nil {
				return fmt.Errorf("read profile sequence head: %w", err)
			}
			if maxSequence.Valid && publication.Sequence <= maxSequence.Int64 {
				return fmt.Errorf("%w: revision sequence must advance profile head", ErrImmutableConflict)
			}
			return nil
		},
		afterInsert: func(ctx context.Context, tx *sql.Tx, publication RevisionPublication) error {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO revision_envelopes(revision_id, envelope) VALUES(?, ?)`,
				publication.RevisionID, envelope); err != nil {
				if isConstraintError(err) {
					return fmt.Errorf("%w: %v", ErrImmutableConflict, err)
				}
				return fmt.Errorf("record signed envelope: %w", err)
			}
			return nil
		},
	})
}

func (s *SQLiteStore) SignedRevision(ctx context.Context, revisionID string) (StoredSignedRevision, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return StoredSignedRevision{}, err
	}
	var stored StoredSignedRevision
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT r.id, r.profile_id, r.sequence, r.manifest_sha256, r.manifest, r.created_at, e.envelope
		 FROM revisions r
		 JOIN revision_envelopes e ON e.revision_id = r.id
		 WHERE r.id = ?`, revisionID).Scan(
		&stored.RevisionID, &stored.ProfileID, &stored.Sequence,
		&stored.ManifestSHA256, &stored.Manifest, &created, &stored.Envelope)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredSignedRevision{}, ErrRevisionNotFound
	}
	if err != nil {
		return StoredSignedRevision{}, err
	}
	stored.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return StoredSignedRevision{}, fmt.Errorf("parse revision timestamp: %w", err)
	}
	return stored, nil
}

func (s *SQLiteStore) ListSignedRevisions(ctx context.Context, limit int) ([]StoredSignedRevision, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.profile_id, r.sequence, r.manifest_sha256, r.manifest, r.created_at, e.envelope
		 FROM revisions r
		 JOIN revision_envelopes e ON e.revision_id = r.id
		 ORDER BY r.profile_id, r.sequence DESC, r.id
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var revisions []StoredSignedRevision
	for rows.Next() {
		var stored StoredSignedRevision
		var created string
		if err := rows.Scan(&stored.RevisionID, &stored.ProfileID, &stored.Sequence,
			&stored.ManifestSHA256, &stored.Manifest, &created, &stored.Envelope); err != nil {
			return nil, err
		}
		stored.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse revision timestamp: %w", err)
		}
		revisions = append(revisions, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revisions, nil
}

func (s *SQLiteStore) ListSignedRevisionsForProfile(ctx context.Context, profileID string, limit int) ([]StoredSignedRevision, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return nil, err
	}
	if profileID == "" {
		return []StoredSignedRevision{}, nil
	}
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.profile_id, r.sequence, r.manifest_sha256, r.manifest, r.created_at, e.envelope
		 FROM revisions r
		 JOIN revision_envelopes e ON e.revision_id = r.id
		 WHERE r.profile_id = ?
		 ORDER BY r.sequence DESC, r.id
		 LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	revisions := make([]StoredSignedRevision, 0)
	for rows.Next() {
		var stored StoredSignedRevision
		var created string
		if err := rows.Scan(&stored.RevisionID, &stored.ProfileID, &stored.Sequence,
			&stored.ManifestSHA256, &stored.Manifest, &created, &stored.Envelope); err != nil {
			return nil, err
		}
		stored.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse revision timestamp: %w", err)
		}
		revisions = append(revisions, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revisions, nil
}

func (s *SQLiteStore) ProfileHead(ctx context.Context, profileID string) (StoredRevision, error) {
	var stored StoredRevision
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, profile_id, sequence, manifest_sha256, manifest, created_at
		 FROM revisions WHERE profile_id = ?
		 ORDER BY sequence DESC, id LIMIT 1`, profileID).Scan(
		&stored.RevisionID, &stored.ProfileID, &stored.Sequence,
		&stored.ManifestSHA256, &stored.Manifest, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredRevision{}, ErrRevisionNotFound
	}
	if err != nil {
		return StoredRevision{}, err
	}
	stored.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return StoredRevision{}, fmt.Errorf("parse revision timestamp: %w", err)
	}
	return stored, nil
}

func (s *SQLiteStore) PublishedObject(ctx context.Context, digest string) (Object, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return Object{}, err
	}
	if !validDigest(digest) {
		return Object{}, ErrInvalidDigest
	}
	var object Object
	err := s.db.QueryRowContext(ctx,
		`SELECT o.sha256, o.size
		 FROM objects o
		 WHERE o.sha256 = ?
		   AND EXISTS (
		       SELECT 1
		       FROM revision_objects ro
		       JOIN revision_envelopes e ON e.revision_id = ro.revision_id
		       WHERE ro.object_sha256 = o.sha256
		   )
		 LIMIT 1`, digest).Scan(&object.SHA256, &object.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return Object{}, ErrObjectMissing
	}
	if err != nil {
		return Object{}, err
	}
	return object, nil
}

func (s *SQLiteStore) Channel(ctx context.Context, profileID, channel string) (ChannelRecord, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return ChannelRecord{}, err
	}
	var record ChannelRecord
	var updated string
	err := s.db.QueryRowContext(ctx,
		`SELECT profile_id, channel, revision_id, sequence, manifest_sha256, updated_at
		 FROM channels WHERE profile_id = ? AND channel = ?`,
		profileID, channel).Scan(&record.ProfileID, &record.Channel, &record.RevisionID,
		&record.Sequence, &record.ManifestSHA256, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelRecord{}, ErrRevisionNotFound
	}
	if err != nil {
		return ChannelRecord{}, err
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return ChannelRecord{}, fmt.Errorf("parse channel timestamp: %w", err)
	}
	return record, nil
}

func (s *SQLiteStore) ListChannels(ctx context.Context) ([]ChannelRecord, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT profile_id, channel, revision_id, sequence, manifest_sha256, updated_at
		 FROM channels ORDER BY profile_id, channel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []ChannelRecord
	for rows.Next() {
		var record ChannelRecord
		var updated string
		if err := rows.Scan(&record.ProfileID, &record.Channel, &record.RevisionID,
			&record.Sequence, &record.ManifestSHA256, &updated); err != nil {
			return nil, err
		}
		record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("parse channel timestamp: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// CompareAndSetChannel updates only mutable channel state and verifies that the
// candidate points at an already-published immutable revision. Domain policy
// (promotion vs signed rollback) is decided by internal/revision before this.
func (s *SQLiteStore) CompareAndSetChannel(ctx context.Context, expected *ChannelRecord, candidate ChannelRecord) error {
	if candidate.ProfileID == "" || candidate.Channel == "" || candidate.RevisionID == "" ||
		candidate.Sequence < 1 || !validDigest(candidate.ManifestSHA256) {
		return ErrChannelConflict
	}

	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current ChannelRecord
	err = tx.QueryRowContext(ctx,
		`SELECT profile_id, channel, revision_id, sequence, manifest_sha256
		 FROM channels WHERE profile_id = ? AND channel = ?`,
		candidate.ProfileID, candidate.Channel).Scan(
		&current.ProfileID, &current.Channel, &current.RevisionID, &current.Sequence, &current.ManifestSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		if expected != nil {
			return ErrChannelConflict
		}
	} else if err != nil {
		return err
	} else {
		if expected == nil || current.ProfileID != expected.ProfileID || current.Channel != expected.Channel ||
			current.RevisionID != expected.RevisionID || current.Sequence != expected.Sequence ||
			current.ManifestSHA256 != expected.ManifestSHA256 {
			return ErrChannelConflict
		}
	}

	var profileID, manifestSHA string
	var sequence int64
	err = tx.QueryRowContext(ctx,
		`SELECT profile_id, sequence, manifest_sha256 FROM revisions WHERE id = ?`,
		candidate.RevisionID).Scan(&profileID, &sequence, &manifestSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRevisionNotFound
	}
	if err != nil {
		return err
	}
	if profileID != candidate.ProfileID || sequence != candidate.Sequence || manifestSHA != candidate.ManifestSHA256 {
		return ErrChannelConflict
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO channels(profile_id, channel, revision_id, sequence, manifest_sha256, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(profile_id, channel) DO UPDATE SET
		   revision_id = excluded.revision_id,
		   sequence = excluded.sequence,
		   manifest_sha256 = excluded.manifest_sha256,
		   updated_at = excluded.updated_at`,
		candidate.ProfileID, candidate.Channel, candidate.RevisionID,
		candidate.Sequence, candidate.ManifestSHA256, now); err != nil {
		return err
	}
	return tx.Commit()
}

const profileAPISchema = `
CREATE TABLE IF NOT EXISTS revision_envelopes (
    revision_id TEXT PRIMARY KEY REFERENCES revisions(id),
    envelope BLOB NOT NULL CHECK(length(envelope) > 0)
);

CREATE TRIGGER IF NOT EXISTS revision_envelopes_no_update
BEFORE UPDATE ON revision_envelopes BEGIN
    SELECT RAISE(ABORT, 'immutable revision envelopes');
END;
CREATE TRIGGER IF NOT EXISTS revision_envelopes_no_delete
BEFORE DELETE ON revision_envelopes BEGIN
    SELECT RAISE(ABORT, 'immutable revision envelopes');
END;

CREATE TABLE IF NOT EXISTS channels (
    profile_id TEXT NOT NULL,
    channel TEXT NOT NULL,
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    sequence INTEGER NOT NULL CHECK(sequence >= 1),
    manifest_sha256 TEXT NOT NULL CHECK(length(manifest_sha256) = 64),
    updated_at TEXT NOT NULL,
    PRIMARY KEY(profile_id, channel)
);
`
