package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/wachipayox/BootOptimDistribution/internal/semver"
)

var (
	ErrLauncherReleaseExists   = errors.New("launcher release version already used")
	ErrLauncherReleaseNotFound = errors.New("launcher release not found")
)

// LauncherArtifact is one signed binary of a launcher release. Everything in
// it is immutable once stored.
type LauncherArtifact struct {
	Platform  string
	FileName  string
	SHA256    string
	Size      int64
	Statement string // unpadded base64url of the canonical statement
	KeyID     string
	Algorithm string
	Signature string // unpadded base64url
}

// LauncherRelease is one version of the Pandora or Android launcher. Only
// Channel, Notes and WithdrawnAt ever change.
type LauncherRelease struct {
	App         string
	Version     string
	Channel     string
	Notes       string
	PublishedAt time.Time
	WithdrawnAt *time.Time
	Artifacts   []LauncherArtifact
}

const launcherSchema = `
CREATE TABLE IF NOT EXISTS launcher_releases (
 app TEXT NOT NULL,
 version TEXT NOT NULL,
 channel TEXT NOT NULL CHECK(channel IN ('player', 'developer')),
 notes TEXT NOT NULL,
 published_at TEXT NOT NULL,
 withdrawn_at TEXT,
 PRIMARY KEY(app, version)
);
CREATE TABLE IF NOT EXISTS launcher_artifacts (
 app TEXT NOT NULL,
 version TEXT NOT NULL,
 platform TEXT NOT NULL,
 file_name TEXT NOT NULL,
 sha256 TEXT NOT NULL CHECK(length(sha256) = 64),
 size INTEGER NOT NULL CHECK(size >= 0),
 statement TEXT NOT NULL,
 key_id TEXT NOT NULL,
 signature TEXT NOT NULL,
 PRIMARY KEY(app, version, platform),
 FOREIGN KEY(app, version) REFERENCES launcher_releases(app, version)
);
CREATE TRIGGER IF NOT EXISTS launcher_artifacts_no_update
BEFORE UPDATE ON launcher_artifacts BEGIN
 SELECT RAISE(ABORT, 'immutable launcher artifacts');
END;
CREATE TRIGGER IF NOT EXISTS launcher_artifacts_no_delete
BEFORE DELETE ON launcher_artifacts BEGIN
 SELECT RAISE(ABORT, 'immutable launcher artifacts');
END;
CREATE TRIGGER IF NOT EXISTS launcher_releases_no_delete
BEFORE DELETE ON launcher_releases BEGIN
 SELECT RAISE(ABORT, 'launcher versions are never reused');
END;
CREATE TRIGGER IF NOT EXISTS launcher_releases_guard_update
BEFORE UPDATE ON launcher_releases
WHEN OLD.app <> NEW.app OR OLD.version <> NEW.version OR OLD.published_at <> NEW.published_at
  OR (OLD.withdrawn_at IS NOT NULL AND NEW.withdrawn_at IS NOT OLD.withdrawn_at) BEGIN
 SELECT RAISE(ABORT, 'immutable launcher release fields');
END;
`

func (s *SQLiteStore) ensureLauncherSchema() error {
	if _, err := s.db.Exec(launcherSchema); err != nil {
		return fmt.Errorf("initialize launcher release storage: %w", err)
	}
	return nil
}

const launcherTimeFormat = "2006-01-02T15:04:05Z"

// CreateLauncherRelease stores a release and its already-signed artifacts.
// Every referenced object is re-verified in the CAS first. Versions are unique
// per app by SemVer precedence, withdrawn ones included, forever.
func (s *SQLiteStore) CreateLauncherRelease(ctx context.Context, release LauncherRelease) error {
	if len(release.Artifacts) == 0 {
		return fmt.Errorf("%w: a release needs at least one artifact", ErrInvalidPublication)
	}
	newVersion, err := semver.Parse(release.Version)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPublication, err)
	}
	for _, artifact := range release.Artifacts {
		if err := s.objects.Verify(ctx, Object{SHA256: artifact.SHA256, Size: artifact.Size}); err != nil {
			return err
		}
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT version FROM launcher_releases WHERE app = ?`, release.App)
	if err != nil {
		return err
	}
	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			rows.Close()
			return err
		}
		if v, err := semver.Parse(existing); err == nil && semver.Compare(v, newVersion) == 0 {
			rows.Close()
			return ErrLauncherReleaseExists
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO launcher_releases(app, version, channel, notes, published_at) VALUES(?, ?, ?, ?, ?)`,
		release.App, release.Version, release.Channel, release.Notes, release.PublishedAt.UTC().Format(launcherTimeFormat)); err != nil {
		if isConstraintError(err) {
			return ErrLauncherReleaseExists
		}
		return err
	}
	for _, a := range release.Artifacts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO objects(sha256, size, created_at) VALUES(?, ?, ?) ON CONFLICT(sha256) DO NOTHING`,
			a.SHA256, a.Size, now); err != nil {
			return fmt.Errorf("record object %s: %w", a.SHA256, err)
		}
		var stored int64
		if err := tx.QueryRowContext(ctx, `SELECT size FROM objects WHERE sha256 = ?`, a.SHA256).Scan(&stored); err != nil {
			return err
		}
		if stored != a.Size {
			return fmt.Errorf("%w: object %s size changed", ErrImmutableConflict, a.SHA256)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO launcher_artifacts(app, version, platform, file_name, sha256, size, statement, key_id, signature)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			release.App, release.Version, a.Platform, a.FileName, a.SHA256, a.Size, a.Statement, a.KeyID, a.Signature); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LauncherReleases lists every release (withdrawn included) of one app, or of
// all apps when app is empty, newest SemVer first within each app.
func (s *SQLiteStore) LauncherReleases(ctx context.Context, app string) ([]LauncherRelease, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT app, version, channel, notes, published_at, withdrawn_at FROM launcher_releases
		 WHERE ? = '' OR app = ? ORDER BY app, version`, app, app)
	if err != nil {
		return nil, err
	}
	var releases []LauncherRelease
	for rows.Next() {
		var r LauncherRelease
		var published string
		var withdrawn sql.NullString
		if err := rows.Scan(&r.App, &r.Version, &r.Channel, &r.Notes, &published, &withdrawn); err != nil {
			rows.Close()
			return nil, err
		}
		if r.PublishedAt, err = time.Parse(launcherTimeFormat, published); err != nil {
			rows.Close()
			return nil, err
		}
		if withdrawn.Valid {
			t, err := time.Parse(launcherTimeFormat, withdrawn.String)
			if err != nil {
				rows.Close()
				return nil, err
			}
			r.WithdrawnAt = &t
		}
		releases = append(releases, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	index := make(map[[2]string]int, len(releases))
	for i := range releases {
		index[[2]string{releases[i].App, releases[i].Version}] = i
	}
	arows, err := s.db.QueryContext(ctx,
		`SELECT app, version, platform, file_name, sha256, size, statement, key_id, signature FROM launcher_artifacts
		 WHERE ? = '' OR app = ? ORDER BY platform`, app, app)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var appName, version string
		a := LauncherArtifact{Algorithm: "Ed25519"}
		if err := arows.Scan(&appName, &version, &a.Platform, &a.FileName, &a.SHA256, &a.Size, &a.Statement, &a.KeyID, &a.Signature); err != nil {
			return nil, err
		}
		if i, ok := index[[2]string{appName, version}]; ok {
			releases[i].Artifacts = append(releases[i].Artifacts, a)
		}
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].App != releases[j].App {
			return releases[i].App < releases[j].App
		}
		vi, ei := semver.Parse(releases[i].Version)
		vj, ej := semver.Parse(releases[j].Version)
		if ei != nil || ej != nil {
			return releases[i].Version > releases[j].Version
		}
		return semver.Compare(vi, vj) > 0
	})
	return releases, nil
}

// LauncherRelease returns one release by its exact stored version string.
func (s *SQLiteStore) LauncherRelease(ctx context.Context, app, version string) (LauncherRelease, error) {
	all, err := s.LauncherReleases(ctx, app)
	if err != nil {
		return LauncherRelease{}, err
	}
	for _, r := range all {
		if r.Version == version {
			return r, nil
		}
	}
	return LauncherRelease{}, ErrLauncherReleaseNotFound
}

// UpdateLauncherRelease changes the cosmetic fields; nil means unchanged.
func (s *SQLiteStore) UpdateLauncherRelease(ctx context.Context, app, version string, channel, notes *string) (LauncherRelease, error) {
	s.publishMu.Lock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE launcher_releases SET channel = COALESCE(?, channel), notes = COALESCE(?, notes)
		 WHERE app = ? AND version = ?`, channel, notes, app, version)
	s.publishMu.Unlock()
	if err != nil {
		return LauncherRelease{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return LauncherRelease{}, ErrLauncherReleaseNotFound
	}
	return s.LauncherRelease(ctx, app, version)
}

// WithdrawLauncherRelease stops offering a release. It is idempotent and keeps
// the original withdrawal time.
func (s *SQLiteStore) WithdrawLauncherRelease(ctx context.Context, app, version string) (LauncherRelease, error) {
	s.publishMu.Lock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE launcher_releases SET withdrawn_at = COALESCE(withdrawn_at, ?) WHERE app = ? AND version = ?`,
		time.Now().UTC().Format(launcherTimeFormat), app, version)
	s.publishMu.Unlock()
	if err != nil {
		return LauncherRelease{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return LauncherRelease{}, ErrLauncherReleaseNotFound
	}
	return s.LauncherRelease(ctx, app, version)
}
