package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ConfigPolicyRule is one admin-panel intent about config file policies:
// a folder rule (Path ends with "/") applies to every config under it,
// subfolders included; a file rule (no trailing "/") is an explicit
// exception. The most specific rule wins. These rules are unsigned panel
// metadata: launchers never read them. The signed manifest still carries the
// expanded per-file `configs[].policy`, which is the only client contract.
type ConfigPolicyRule struct {
	Path   string `json:"path"`
	Policy string `json:"policy"`
}

const configPolicyRulesSchema = `
CREATE TABLE IF NOT EXISTS profile_config_policy_rules (
 profile_id TEXT PRIMARY KEY,
 rules TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
`

func (s *SQLiteStore) ensureConfigPolicySchema(ctx context.Context) error {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, configPolicyRulesSchema); err != nil {
		return fmt.Errorf("initialize config policy rule storage: %w", err)
	}
	return nil
}

// ConfigPolicyRules returns the rules saved for a profile (empty when none).
func (s *SQLiteStore) ConfigPolicyRules(ctx context.Context, profileID string) ([]ConfigPolicyRule, time.Time, error) {
	if err := s.ensureConfigPolicySchema(ctx); err != nil {
		return nil, time.Time{}, err
	}
	var raw, updated string
	err := s.db.QueryRowContext(ctx, `SELECT rules, updated_at FROM profile_config_policy_rules WHERE profile_id = ?`, profileID).Scan(&raw, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return []ConfigPolicyRule{}, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	rules := []ConfigPolicyRule{}
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode stored config policy rules: %w", err)
	}
	at, _ := time.Parse(time.RFC3339, updated)
	return rules, at, nil
}

// SetConfigPolicyRules replaces the rules of a published, non-deleted profile.
// Validation of each rule belongs to the HTTP layer.
func (s *SQLiteStore) SetConfigPolicyRules(ctx context.Context, profileID string, rules []ConfigPolicyRule) (time.Time, error) {
	if err := s.ensureConfigPolicySchema(ctx); err != nil {
		return time.Time{}, err
	}
	if rules == nil {
		rules = []ConfigPolicyRule{}
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return time.Time{}, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM revisions WHERE profile_id = ?
		 AND NOT EXISTS (SELECT 1 FROM deleted_profiles d WHERE d.profile_id = revisions.profile_id)`, profileID).Scan(&exists); err != nil {
		return time.Time{}, err
	}
	if exists == 0 {
		return time.Time{}, ErrRevisionNotFound
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO profile_config_policy_rules(profile_id, rules, updated_at) VALUES(?,?,?)
		 ON CONFLICT(profile_id) DO UPDATE SET rules=excluded.rules, updated_at=excluded.updated_at`,
		profileID, string(raw), now.Format(time.RFC3339)); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// DeletedProfileIDs lists the tombstoned profile identifiers, which can never
// be published again (publication answers immutable_conflict).
func (s *SQLiteStore) DeletedProfileIDs(ctx context.Context) ([]string, error) {
	if err := s.ensureProfileAPISchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT profile_id FROM deleted_profiles ORDER BY profile_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
