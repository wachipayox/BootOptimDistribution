package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RevisionChangelog is the mutable, unsigned operator text of one revision.
// The immutable signed revision never changes; only this cosmetic row does.
type RevisionChangelog struct {
	Title       string
	Description string
	Changes     []string
	UpdatedAt   time.Time
}

const revisionChangelogSchema = `
CREATE TABLE IF NOT EXISTS revision_changelog (
 revision_id TEXT PRIMARY KEY REFERENCES revisions(id),
 title TEXT NOT NULL,
 description TEXT NOT NULL,
 changes TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
`

func (s *SQLiteStore) ensureChangelogSchema(ctx context.Context) error {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, revisionChangelogSchema); err != nil {
		return fmt.Errorf("initialize revision changelog storage: %w", err)
	}
	return nil
}

// SetRevisionChangelog replaces the text of an existing revision.
func (s *SQLiteStore) SetRevisionChangelog(ctx context.Context, revisionID string, text RevisionChangelog) (RevisionChangelog, error) {
	if err := s.ensureChangelogSchema(ctx); err != nil {
		return RevisionChangelog{}, err
	}
	changes := text.Changes
	if changes == nil {
		changes = []string{}
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return RevisionChangelog{}, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE id = ?`, revisionID).Scan(&exists); err != nil {
		return RevisionChangelog{}, err
	}
	if exists == 0 {
		return RevisionChangelog{}, ErrRevisionNotFound
	}
	now := time.Now().UTC().Truncate(time.Second)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO revision_changelog(revision_id, title, description, changes, updated_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(revision_id) DO UPDATE SET title=excluded.title, description=excluded.description,
		   changes=excluded.changes, updated_at=excluded.updated_at`,
		revisionID, text.Title, text.Description, string(raw), now.Format(time.RFC3339))
	if err != nil {
		return RevisionChangelog{}, err
	}
	text.Changes, text.UpdatedAt = changes, now
	return text, nil
}

// RevisionChangelogs returns the stored texts among ids; ids without a row are absent.
func (s *SQLiteStore) RevisionChangelogs(ctx context.Context, ids []string) (map[string]RevisionChangelog, error) {
	out := make(map[string]RevisionChangelog)
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.ensureChangelogSchema(ctx); err != nil {
		return nil, err
	}
	for start := 0; start < len(ids); start += 200 {
		end := start + 200
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		args := make([]interface{}, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT revision_id, title, description, changes, updated_at FROM revision_changelog WHERE revision_id IN (`+
				strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, rawChanges, updated string
			var text RevisionChangelog
			if err := rows.Scan(&id, &text.Title, &text.Description, &rawChanges, &updated); err != nil {
				rows.Close()
				return nil, err
			}
			if err := json.Unmarshal([]byte(rawChanges), &text.Changes); err != nil {
				rows.Close()
				return nil, fmt.Errorf("decode stored changelog: %w", err)
			}
			if text.Changes == nil {
				text.Changes = []string{}
			}
			text.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
			out[id] = text
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ProfileRevisionsAscending returns every revision of a profile (deleted or
// not, no row limit) by ascending sequence.
func (s *SQLiteStore) ProfileRevisionsAscending(ctx context.Context, profileID string) ([]StoredSignedRevision, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.profile_id, r.sequence, r.manifest_sha256, r.manifest, r.created_at, e.envelope
		 FROM revisions r JOIN revision_envelopes e ON e.revision_id = r.id
		 WHERE r.profile_id = ? ORDER BY r.sequence ASC, r.id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoredSignedRevision, 0)
	for rows.Next() {
		var stored StoredSignedRevision
		var created string
		if err := rows.Scan(&stored.RevisionID, &stored.ProfileID, &stored.Sequence,
			&stored.ManifestSHA256, &stored.Manifest, &created, &stored.Envelope); err != nil {
			return nil, err
		}
		if stored.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("parse revision timestamp: %w", err)
		}
		out = append(out, stored)
	}
	return out, rows.Err()
}
