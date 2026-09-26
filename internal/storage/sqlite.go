package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrInvalidPublication = errors.New("invalid revision publication")
	ErrImmutableConflict  = errors.New("immutable revision conflict")
	ErrRevisionNotFound   = errors.New("revision not found")
)

type ValidatedManifest struct {
	Bytes  []byte
	SHA256 string
}

type RevisionPublication struct {
	RevisionID      string
	ProfileID       string
	Sequence        int64
	Manifest        ValidatedManifest
	ExpectedObjects []Object
}

type StoredRevision struct {
	RevisionID     string
	ProfileID      string
	Sequence       int64
	ManifestSHA256 string
	Manifest       []byte
	CreatedAt      time.Time
}

type StorageStats struct {
	RevisionCount int64
	ObjectCount   int64
	ObjectBytes   int64
}

type RevisionPublisher interface {
	PublishRevision(context.Context, RevisionPublication) error
}

type SQLiteStore struct {
	db        *sql.DB
	objects   ObjectIngester
	publishMu sync.Mutex
}

func OpenSQLite(path string, objects ObjectIngester) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	if objects == nil {
		return nil, errors.New("object verifier is required")
	}
	parent := filepath.Dir(filepath.Clean(path))
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, fmt.Errorf("create sqlite parent: %w", err)
	}
	if err := requireDirectoryNoSymlink(parent); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe sqlite path %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect sqlite path: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// v1 deliberately uses one writer connection in one service process. This
	// keeps PRAGMA state deterministic and publication ordering simple.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &SQLiteStore{db: db, objects: objects}
	if err := store.initialize(); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("restrict sqlite file: %w", err)
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) initialize() error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, statement := range pragmas {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("configure sqlite (%s): %w", statement, err)
		}
	}
	if _, err := s.db.Exec(schemaV1); err != nil {
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	return nil
}

type publicationHooks struct {
	existing     func(context.Context, *sql.Tx, RevisionPublication) error
	beforeInsert func(context.Context, *sql.Tx, RevisionPublication) error
	afterInsert  func(context.Context, *sql.Tx, RevisionPublication) error
}

func (s *SQLiteStore) PublishRevision(ctx context.Context, publication RevisionPublication) error {
	return s.publishRevision(ctx, publication, publicationHooks{})
}

// publishRevision is the single durable publication primitive. HTTP-facing
// extensions may add metadata/policy checks through hooks, but object
// verification, immutable revision insertion and the commit boundary stay here.
func (s *SQLiteStore) publishRevision(ctx context.Context, publication RevisionPublication, hooks publicationHooks) error {
	objects, err := normalizePublication(publication)
	if err != nil {
		return err
	}
	manifestHash := sha256.Sum256(publication.Manifest.Bytes)
	if hex.EncodeToString(manifestHash[:]) != publication.Manifest.SHA256 {
		return fmt.Errorf("%w: manifest bytes do not match supplied digest", ErrInvalidPublication)
	}

	// Re-prove every referenced CAS object immediately before the metadata
	// transaction. There is intentionally no DB-only shortcut here.
	for _, object := range objects {
		if err := s.objects.Verify(ctx, object); err != nil {
			if errors.Is(err, ErrObjectMissing) {
				return err
			}
			return fmt.Errorf("%w: %s: %v", ErrCorruptObject, object.SHA256, err)
		}
	}

	s.publishMu.Lock()
	defer s.publishMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin publication transaction: %w", err)
	}
	defer tx.Rollback()

	identical, err := identicalExistingRevision(ctx, tx, publication, objects)
	if err != nil {
		return err
	}
	if identical {
		if hooks.existing != nil {
			return hooks.existing(ctx, tx, publication)
		}
		return nil
	}
	if hooks.beforeInsert != nil {
		if err := hooks.beforeInsert(ctx, tx, publication); err != nil {
			return err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, object := range objects {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO objects(sha256, size, created_at) VALUES(?, ?, ?)
			 ON CONFLICT(sha256) DO NOTHING`,
			object.SHA256, object.Size, now); err != nil {
			return fmt.Errorf("record object %s: %w", object.SHA256, err)
		}
		var storedSize int64
		if err := tx.QueryRowContext(ctx, `SELECT size FROM objects WHERE sha256 = ?`, object.SHA256).Scan(&storedSize); err != nil {
			return fmt.Errorf("read object metadata %s: %w", object.SHA256, err)
		}
		if storedSize != object.Size {
			return fmt.Errorf("%w: object %s size changed", ErrImmutableConflict, object.SHA256)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO revisions(id, profile_id, sequence, manifest_sha256, manifest, created_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		publication.RevisionID, publication.ProfileID, publication.Sequence,
		publication.Manifest.SHA256, publication.Manifest.Bytes, now); err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: %v", ErrImmutableConflict, err)
		}
		return fmt.Errorf("record revision: %w", err)
	}
	for _, object := range objects {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO revision_objects(revision_id, object_sha256) VALUES(?, ?)`,
			publication.RevisionID, object.SHA256); err != nil {
			return fmt.Errorf("record revision object %s: %w", object.SHA256, err)
		}
	}
	if hooks.afterInsert != nil {
		if err := hooks.afterInsert(ctx, tx, publication); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: %v", ErrImmutableConflict, err)
		}
		return fmt.Errorf("commit publication: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Revision(ctx context.Context, revisionID string) (StoredRevision, error) {
	var stored StoredRevision
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, profile_id, sequence, manifest_sha256, manifest, created_at
		 FROM revisions WHERE id = ?`, revisionID).Scan(
		&stored.RevisionID, &stored.ProfileID, &stored.Sequence,
		&stored.ManifestSHA256, &stored.Manifest, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredRevision{}, ErrRevisionNotFound
	}
	if err != nil {
		return StoredRevision{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return StoredRevision{}, fmt.Errorf("parse revision timestamp: %w", err)
	}
	stored.CreatedAt = parsed
	return stored, nil
}

// ListRevisions returns the newest immutable revision records for an
// administrative inventory. It deliberately returns signed manifest bytes
// unchanged; callers decide which validated fields are safe to project.
func (s *SQLiteStore) ListRevisions(ctx context.Context, limit int) ([]StoredRevision, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, profile_id, sequence, manifest_sha256, manifest, created_at
		 FROM revisions ORDER BY julianday(created_at) DESC, profile_id, sequence DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var revisions []StoredRevision
	for rows.Next() {
		var stored StoredRevision
		var created string
		if err := rows.Scan(&stored.RevisionID, &stored.ProfileID, &stored.Sequence,
			&stored.ManifestSHA256, &stored.Manifest, &created); err != nil {
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

func (s *SQLiteStore) Stats(ctx context.Context) (StorageStats, error) {
	var stats StorageStats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions`).Scan(&stats.RevisionCount); err != nil {
		return StorageStats{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM objects`).Scan(&stats.ObjectCount, &stats.ObjectBytes); err != nil {
		return StorageStats{}, err
	}
	return stats, nil
}

func (s *SQLiteStore) IsObjectPublished(ctx context.Context, digest string) (bool, error) {
	if !validDigest(digest) {
		return false, ErrInvalidDigest
	}
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM revision_objects WHERE object_sha256 = ? LIMIT 1`, digest).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func normalizePublication(publication RevisionPublication) ([]Object, error) {
	if publication.RevisionID == "" || publication.ProfileID == "" || publication.Sequence < 0 {
		return nil, ErrInvalidPublication
	}
	if len(publication.Manifest.Bytes) == 0 || !validDigest(publication.Manifest.SHA256) {
		return nil, ErrInvalidPublication
	}
	byHash := make(map[string]Object, len(publication.ExpectedObjects))
	for _, object := range publication.ExpectedObjects {
		if err := validateObject(object); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPublication, err)
		}
		if previous, ok := byHash[object.SHA256]; ok && previous.Size != object.Size {
			return nil, fmt.Errorf("%w: object %s has conflicting sizes", ErrInvalidPublication, object.SHA256)
		}
		byHash[object.SHA256] = object
	}
	objects := make([]Object, 0, len(byHash))
	for _, object := range byHash {
		objects = append(objects, object)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].SHA256 < objects[j].SHA256 })
	return objects, nil
}

func identicalExistingRevision(ctx context.Context, tx *sql.Tx, publication RevisionPublication, objects []Object) (bool, error) {
	var profileID, manifestDigest string
	var sequence int64
	var manifest []byte
	err := tx.QueryRowContext(ctx,
		`SELECT profile_id, sequence, manifest_sha256, manifest FROM revisions WHERE id = ?`,
		publication.RevisionID).Scan(&profileID, &sequence, &manifestDigest, &manifest)
	if err == nil {
		if profileID != publication.ProfileID || sequence != publication.Sequence ||
			manifestDigest != publication.Manifest.SHA256 || !bytes.Equal(manifest, publication.Manifest.Bytes) {
			return false, ErrImmutableConflict
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT ro.object_sha256, o.size
			 FROM revision_objects ro JOIN objects o ON o.sha256 = ro.object_sha256
			 WHERE ro.revision_id = ? ORDER BY ro.object_sha256`, publication.RevisionID)
		if err != nil {
			return false, err
		}
		defer rows.Close()
		var stored []Object
		for rows.Next() {
			var object Object
			if err := rows.Scan(&object.SHA256, &object.Size); err != nil {
				return false, err
			}
			stored = append(stored, object)
		}
		if err := rows.Err(); err != nil {
			return false, err
		}
		if len(stored) != len(objects) {
			return false, ErrImmutableConflict
		}
		for i := range stored {
			if stored[i] != objects[i] {
				return false, ErrImmutableConflict
			}
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	var existingID string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM revisions WHERE profile_id = ? AND sequence = ?`,
		publication.ProfileID, publication.Sequence).Scan(&existingID); err == nil {
		return false, ErrImmutableConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM revisions WHERE manifest_sha256 = ?`,
		publication.Manifest.SHA256).Scan(&existingID); err == nil {
		return false, ErrImmutableConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return false, nil
}

func isConstraintError(err error) bool {
	return strings.Contains(err.Error(), "constraint failed") || strings.Contains(err.Error(), "UNIQUE constraint")
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS objects (
    sha256 TEXT PRIMARY KEY CHECK(length(sha256) = 64),
    size INTEGER NOT NULL CHECK(size >= 0),
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS revisions (
    id TEXT PRIMARY KEY,
    profile_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK(sequence >= 0),
    manifest_sha256 TEXT UNIQUE NOT NULL CHECK(length(manifest_sha256) = 64),
    manifest BLOB NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(profile_id, sequence)
);

CREATE TABLE IF NOT EXISTS revision_objects (
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    object_sha256 TEXT NOT NULL REFERENCES objects(sha256),
    PRIMARY KEY(revision_id, object_sha256)
);

CREATE TRIGGER IF NOT EXISTS revisions_no_update
BEFORE UPDATE ON revisions BEGIN
    SELECT RAISE(ABORT, 'immutable revisions');
END;
CREATE TRIGGER IF NOT EXISTS revisions_no_delete
BEFORE DELETE ON revisions BEGIN
    SELECT RAISE(ABORT, 'immutable revisions');
END;
CREATE TRIGGER IF NOT EXISTS revision_objects_no_update
BEFORE UPDATE ON revision_objects BEGIN
    SELECT RAISE(ABORT, 'immutable revision objects');
END;
CREATE TRIGGER IF NOT EXISTS revision_objects_no_delete
BEFORE DELETE ON revision_objects BEGIN
    SELECT RAISE(ABORT, 'immutable revision objects');
END;
CREATE TRIGGER IF NOT EXISTS published_objects_no_update
BEFORE UPDATE ON objects BEGIN
    SELECT RAISE(ABORT, 'immutable published objects');
END;
CREATE TRIGGER IF NOT EXISTS published_objects_no_delete
BEFORE DELETE ON objects BEGIN
    SELECT RAISE(ABORT, 'published objects retained in v1');
END;

PRAGMA user_version = 1;
`
