package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TestProfilePrefix marks the profile identifiers that the test API may write.
// The prefix alone is not enough: a test_profiles row is the authority.
const TestProfilePrefix = "profile_test-"

var (
	ErrTestProfileExists   = errors.New("test profile identifier already used")
	ErrTestProfileNotFound = errors.New("test profile not found")
)

// TestProfileRecord is the server-side authority for one test profile.
type TestProfileRecord struct {
	ProfileID    string
	Owner        string
	CreatedAt    time.Time
	FollowParent bool
	Deleted      bool
}

// TestUsage is the quota state of the test API.
type TestUsage struct {
	LiveProfiles int64
	// CountedBytes are bytes first uploaded through the test API that a live
	// test profile still references, plus recent unreferenced staging.
	CountedBytes int64
}

// TestStagingGrace keeps a freshly uploaded test object counted until it is
// published or this grace period expires.
const TestStagingGrace = 24 * time.Hour

const testProfilesSchema = `
CREATE TABLE IF NOT EXISTS test_profiles (
 profile_id TEXT PRIMARY KEY CHECK(substr(profile_id, 1, 13) = 'profile_test-'),
 owner TEXT NOT NULL,
 created_at TEXT NOT NULL,
 follow_parent INTEGER NOT NULL CHECK(follow_parent IN (0, 1))
);
CREATE TABLE IF NOT EXISTS test_api_token (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 token_sha256 TEXT NOT NULL CHECK(length(token_sha256) = 64),
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS test_objects (
 sha256 TEXT PRIMARY KEY CHECK(length(sha256) = 64),
 size INTEGER NOT NULL CHECK(size >= 0),
 owner TEXT NOT NULL,
 uploaded_at TEXT NOT NULL
);
`

func (s *SQLiteStore) ensureTestSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, testProfilesSchema); err != nil {
		return fmt.Errorf("initialize test profile storage: %w", err)
	}
	return nil
}

// RegisterTestProfile reserves a never-used test identifier before its first
// revision is published. The identifier must not have revisions or a tombstone.
func (s *SQLiteStore) RegisterTestProfile(ctx context.Context, id, owner string, followParent bool) error {
	if !strings.HasPrefix(id, TestProfilePrefix) {
		return fmt.Errorf("%w: %s", ErrInvalidPublication, "test profile identifier lacks the test prefix")
	}
	if err := s.ensureTestSchema(ctx); err != nil {
		return err
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var used int
	if err := tx.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM revisions WHERE profile_id = ?)
		      + (SELECT COUNT(*) FROM deleted_profiles WHERE profile_id = ?)
		      + (SELECT COUNT(*) FROM test_profiles WHERE profile_id = ?)`, id, id, id).Scan(&used); err != nil {
		return err
	}
	if used != 0 {
		return ErrTestProfileExists
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO test_profiles(profile_id, owner, created_at, follow_parent) VALUES(?, ?, ?, ?)`,
		id, owner, time.Now().UTC().Format(time.RFC3339Nano), boolInt(followParent)); err != nil {
		return err
	}
	return tx.Commit()
}

// ReleaseUnpublishedTestProfile undoes RegisterTestProfile when the first
// publication failed. It never touches a profile that already has revisions.
func (s *SQLiteStore) ReleaseUnpublishedTestProfile(ctx context.Context, id string) error {
	if err := s.ensureTestSchema(ctx); err != nil {
		return err
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM test_profiles WHERE profile_id = ?
		 AND NOT EXISTS (SELECT 1 FROM revisions WHERE profile_id = ?)`, id, id)
	return err
}

func (s *SQLiteStore) TestProfile(ctx context.Context, id string) (TestProfileRecord, error) {
	if err := s.ensureTestSchema(ctx); err != nil {
		return TestProfileRecord{}, err
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT t.profile_id, t.owner, t.created_at, t.follow_parent,
		        EXISTS (SELECT 1 FROM deleted_profiles d WHERE d.profile_id = t.profile_id)
		 FROM test_profiles t WHERE t.profile_id = ?`, id)
	record, err := scanTestProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TestProfileRecord{}, ErrTestProfileNotFound
	}
	return record, err
}

// ListTestProfiles returns every registered test profile, deleted ones last.
func (s *SQLiteStore) ListTestProfiles(ctx context.Context) ([]TestProfileRecord, error) {
	if err := s.ensureTestSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.profile_id, t.owner, t.created_at, t.follow_parent,
		        EXISTS (SELECT 1 FROM deleted_profiles d WHERE d.profile_id = t.profile_id) AS deleted
		 FROM test_profiles t ORDER BY deleted, t.profile_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]TestProfileRecord, 0)
	for rows.Next() {
		record, err := scanTestProfile(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLiteStore) SetTestProfileFollow(ctx context.Context, id string, follow bool) error {
	if err := s.ensureTestSchema(ctx); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE test_profiles SET follow_parent = ? WHERE profile_id = ?`, boolInt(follow), id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrTestProfileNotFound
	}
	return nil
}

// TestProfileFollows reports whether automatic parent following applies to a
// profile. Profiles without a test record (normal profiles) always follow.
func (s *SQLiteStore) TestProfileFollows(ctx context.Context, id string) (bool, error) {
	record, err := s.TestProfile(ctx, id)
	if errors.Is(err, ErrTestProfileNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return record.FollowParent, nil
}

// RecordTestObject remembers an object whose bytes the test API added to CAS.
func (s *SQLiteStore) RecordTestObject(ctx context.Context, object Object, owner string) error {
	if !validDigest(object.SHA256) || object.Size < 0 {
		return ErrInvalidDigest
	}
	if err := s.ensureTestSchema(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO test_objects(sha256, size, owner, uploaded_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(sha256) DO NOTHING`,
		object.SHA256, object.Size, owner, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// TestUsage counts live test profiles and the test-uploaded bytes that still
// matter: referenced by a live test profile, or staged within the grace period.
func (s *SQLiteStore) TestUsage(ctx context.Context) (TestUsage, error) {
	if err := s.ensureTestSchema(ctx); err != nil {
		return TestUsage{}, err
	}
	var usage TestUsage
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM test_profiles t
		 WHERE NOT EXISTS (SELECT 1 FROM deleted_profiles d WHERE d.profile_id = t.profile_id)`).Scan(&usage.LiveProfiles); err != nil {
		return TestUsage{}, err
	}
	cutoff := time.Now().UTC().Add(-TestStagingGrace).Format(time.RFC3339Nano)
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(o.size), 0) FROM test_objects o
		 WHERE EXISTS (
		     SELECT 1 FROM revision_objects ro
		     JOIN revisions r ON r.id = ro.revision_id
		     JOIN test_profiles t ON t.profile_id = r.profile_id
		     WHERE ro.object_sha256 = o.sha256
		       AND NOT EXISTS (SELECT 1 FROM deleted_profiles d WHERE d.profile_id = r.profile_id))
		 OR (NOT EXISTS (SELECT 1 FROM revision_objects ro WHERE ro.object_sha256 = o.sha256)
		     AND o.uploaded_at >= ?)`, cutoff).Scan(&usage.CountedBytes); err != nil {
		return TestUsage{}, err
	}
	return usage, nil
}

// TestProfileOwnBytes sums the test-uploaded objects referenced by any
// revision of one profile (what deleting it can release from the quota).
func (s *SQLiteStore) TestProfileOwnBytes(ctx context.Context, id string) (int64, error) {
	if err := s.ensureTestSchema(ctx); err != nil {
		return 0, err
	}
	var total int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(o.size), 0) FROM test_objects o
		 WHERE EXISTS (SELECT 1 FROM revision_objects ro JOIN revisions r ON r.id = ro.revision_id
		               WHERE ro.object_sha256 = o.sha256 AND r.profile_id = ?)`, id).Scan(&total)
	return total, err
}

// TestTokenHash returns the SHA-256 of the shared test API token, if any.
func (s *SQLiteStore) TestTokenHash(ctx context.Context) (string, time.Time, bool, error) {
	if err := s.ensureTestSchema(ctx); err != nil {
		return "", time.Time{}, false, err
	}
	var hash, created string
	err := s.db.QueryRowContext(ctx, `SELECT token_sha256, created_at FROM test_api_token WHERE id = 1`).Scan(&hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return "", time.Time{}, false, err
	}
	return hash, createdAt, true, nil
}

// SetTestTokenHash replaces the shared token verifier; the old token stops
// working as soon as this commits.
func (s *SQLiteStore) SetTestTokenHash(ctx context.Context, hash string, createdAt time.Time) error {
	if !validDigest(hash) {
		return ErrInvalidDigest
	}
	if err := s.ensureTestSchema(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO test_api_token(id, token_sha256, created_at) VALUES(1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET token_sha256 = excluded.token_sha256, created_at = excluded.created_at`,
		hash, createdAt.UTC().Format(time.RFC3339Nano))
	return err
}

type rowScanner interface{ Scan(...any) error }

func scanTestProfile(row rowScanner) (TestProfileRecord, error) {
	var record TestProfileRecord
	var created string
	var follow, deleted int
	if err := row.Scan(&record.ProfileID, &record.Owner, &created, &follow, &deleted); err != nil {
		return TestProfileRecord{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return TestProfileRecord{}, fmt.Errorf("parse test profile timestamp: %w", err)
	}
	record.CreatedAt, record.FollowParent, record.Deleted = createdAt, follow == 1, deleted == 1
	return record, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ProfileSummary is the latest live revision of one profile and its count.
type ProfileSummary struct {
	Latest        StoredSignedRevision
	RevisionCount int64
}

// ProfileSummaries lists the latest revision of every live profile without the
// per-revision row limit, so many test revisions can never truncate the
// catalog or hide a normal child from automatic parent following.
func (s *SQLiteStore) ProfileSummaries(ctx context.Context) ([]ProfileSummary, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.profile_id, r.sequence, r.manifest_sha256, r.manifest, r.created_at, e.envelope,
		        (SELECT COUNT(*) FROM revisions c WHERE c.profile_id = r.profile_id)
		 FROM revisions r
		 JOIN revision_envelopes e ON e.revision_id = r.id
		 WHERE r.sequence = (SELECT MAX(m.sequence) FROM revisions m WHERE m.profile_id = r.profile_id)
		   AND NOT EXISTS (SELECT 1 FROM deleted_profiles d WHERE d.profile_id = r.profile_id)
		 ORDER BY r.profile_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]ProfileSummary, 0)
	for rows.Next() {
		var summary ProfileSummary
		stored := &summary.Latest
		var created string
		if err := rows.Scan(&stored.RevisionID, &stored.ProfileID, &stored.Sequence, &stored.ManifestSHA256,
			&stored.Manifest, &created, &stored.Envelope, &summary.RevisionCount); err != nil {
			return nil, err
		}
		stored.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse revision timestamp: %w", err)
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}
