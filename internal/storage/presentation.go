package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PresentationAsset is a CAS object referenced by a profile's own cosmetic
// presentation (artwork image or logo). It is served publicly while referenced.
type PresentationAsset struct {
	Kind   string // "artwork" or "logo"
	SHA256 string
	Size   int64
}

// presentationAssetsSchema is executed with the rest of the profile API schema.
const presentationAssetsSchema = `
CREATE TABLE IF NOT EXISTS profile_presentation_assets (
 profile_id TEXT NOT NULL, kind TEXT NOT NULL, sha256 TEXT NOT NULL, size INTEGER NOT NULL,
 PRIMARY KEY(profile_id, kind)
);
CREATE INDEX IF NOT EXISTS profile_presentation_assets_sha ON profile_presentation_assets(sha256);
`

// SetProfilePresentation stores the own presentation payload, the icon digest
// and the artwork/logo references atomically (last write wins).
func (s *SQLiteStore) SetProfilePresentation(ctx context.Context, id string, raw []byte, icon string, assets []PresentationAsset) error {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO profile_presentation(profile_id,payload,icon_sha256) VALUES(?,?,?) ON CONFLICT(profile_id) DO UPDATE SET payload=excluded.payload,icon_sha256=excluded.icon_sha256", id, raw, icon); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM profile_presentation_assets WHERE profile_id = ?", id); err != nil {
		return err
	}
	for _, asset := range assets {
		if !validDigest(asset.SHA256) {
			return ErrInvalidDigest
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO profile_presentation_assets(profile_id,kind,sha256,size) VALUES(?,?,?,?)", id, asset.Kind, asset.SHA256, asset.Size); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestRevisionAny returns the newest revision of a profile even when the
// profile has been withdrawn from the catalog (inheritance follows the chain
// through deleted mothers).
func (s *SQLiteStore) LatestRevisionAny(ctx context.Context, profileID string) (StoredRevision, error) {
	var stored StoredRevision
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, profile_id, sequence, manifest_sha256, manifest, created_at
		 FROM revisions WHERE profile_id = ? ORDER BY sequence DESC, id LIMIT 1`, profileID).Scan(
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
