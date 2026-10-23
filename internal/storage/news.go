package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxNewsItems is the number of live news items (no pagination exists).
	MaxNewsItems = 2000
	// MaxNewsCategories is the number of news categories.
	MaxNewsCategories = 32
	// NewsCursorPrefix versions the opaque sync cursor.
	NewsCursorPrefix = "n1."
)

var (
	ErrNewsNotFound      = errors.New("news item not found")
	ErrNewsLimitReached  = errors.New("news item limit reached")
	ErrNewsCategoryInUse = errors.New("news category has news")
	ErrNewsCategoryUnset = errors.New("news category does not exist")
)

// NewsCategory is server data: the operator creates them in the panel.
type NewsCategory struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

// NewsObject is a CAS object a news item references (cover or body image).
type NewsObject struct {
	SHA256 string
	Size   int64
}

// NewsRecord is one live news item. Payload is the public JSON form (the API
// layer owns its shape); the other fields are kept for indexing and sync.
type NewsRecord struct {
	ID              string
	CategoryID      string
	TargetProfileID string
	PublishedAt     time.Time
	UpdatedAt       time.Time
	Payload         []byte
	Objects         []NewsObject
	// ChangeSeq is set by storage: the sync sequence of the last write.
	ChangeSeq int64
}

// NewsSnapshot is the data of one GET /v1/news response.
type NewsSnapshot struct {
	Cursor     int64
	Full       bool
	Categories []NewsCategory
	Items      []NewsRecord
	Deleted    []string
}

// The change log is the cursor source. Every write (create, edit, delete,
// category replacement) appends one row; the row id is the monotonic cursor.
// Delete rows are the tombstones and are kept forever (a few bytes each), so
// any cursor issued by this database stays valid.
const newsSchema = `
CREATE TABLE IF NOT EXISTS news_categories (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 sort_order INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS news_changes (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL CHECK(kind IN ('upsert', 'delete', 'categories')),
 news_id TEXT,
 at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS news_items (
 id TEXT PRIMARY KEY,
 category_id TEXT NOT NULL,
 target_profile_id TEXT NOT NULL DEFAULT '',
 published_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 change_seq INTEGER NOT NULL,
 payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS news_items_change_seq ON news_items(change_seq);
CREATE TABLE IF NOT EXISTS news_objects (
 news_id TEXT NOT NULL,
 sha256 TEXT NOT NULL CHECK(length(sha256) = 64),
 size INTEGER NOT NULL,
 PRIMARY KEY(news_id, sha256)
);
CREATE INDEX IF NOT EXISTS news_objects_sha256 ON news_objects(sha256);
`

var newsMu sync.Mutex

func (s *SQLiteStore) ensureNewsSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, newsSchema); err != nil {
		return fmt.Errorf("initialize news storage: %w", err)
	}
	return nil
}

// NewNewsID returns prefix plus n random characters of [a-z0-9].
func NewNewsID(prefix string, n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return prefix + string(out), nil
}

// FormatNewsCursor renders the opaque cursor.
func FormatNewsCursor(seq int64) string { return NewsCursorPrefix + strconv.FormatInt(seq, 10) }

// ParseNewsCursor accepts only cursors in the exact form this service issues.
func ParseNewsCursor(value string) (int64, bool) {
	if !strings.HasPrefix(value, NewsCursorPrefix) {
		return 0, false
	}
	digits := value[len(NewsCursorPrefix):]
	if digits == "" || len(digits) > 18 || (len(digits) > 1 && digits[0] == '0') {
		return 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	seq, err := strconv.ParseInt(digits, 10, 64)
	return seq, err == nil
}

func appendNewsChange(ctx context.Context, tx *sql.Tx, kind, newsID string) (int64, error) {
	var id interface{}
	if newsID != "" {
		id = newsID
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO news_changes(kind, news_id, at) VALUES(?, ?, ?)`,
		kind, id, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// NewsCursor is the current cursor value (0 when nothing was ever written).
func (s *SQLiteStore) NewsCursor(ctx context.Context) (int64, error) {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return 0, err
	}
	return newsCursor(ctx, s.db)
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func newsCursor(ctx context.Context, q queryRower) (int64, error) {
	var seq sql.NullInt64
	// sqlite_sequence survives the removal of old log rows.
	err := q.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'news_changes'`).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq.Int64, err
}

func (s *SQLiteStore) NewsCategories(ctx context.Context) ([]NewsCategory, error) {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return nil, err
	}
	return listNewsCategories(ctx, s.db)
}

type queryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

func listNewsCategories(ctx context.Context, q queryer) ([]NewsCategory, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, sort_order FROM news_categories ORDER BY sort_order, name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NewsCategory{}
	for rows.Next() {
		var c NewsCategory
		if err := rows.Scan(&c.ID, &c.Name, &c.Order); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReplaceNewsCategories makes the stored list equal to categories. Categories
// that disappear must have no news (ErrNewsCategoryInUse). Validation of names
// and ids belongs to the caller.
func (s *SQLiteStore) ReplaceNewsCategories(ctx context.Context, categories []NewsCategory) error {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return err
	}
	newsMu.Lock()
	defer newsMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[string]bool{}
	for _, c := range categories {
		keep[c.ID] = true
	}
	existing, err := listNewsCategories(ctx, tx)
	if err != nil {
		return err
	}
	for _, c := range existing {
		if keep[c.ID] {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM news_items WHERE category_id = ?`, c.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s", ErrNewsCategoryInUse, c.ID)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_categories`); err != nil {
		return err
	}
	for _, c := range categories {
		if _, err := tx.ExecContext(ctx, `INSERT INTO news_categories(id, name, sort_order) VALUES(?, ?, ?)`, c.ID, c.Name, c.Order); err != nil {
			return err
		}
	}
	// The categories travel in every response: a change must move the cursor.
	if _, err := appendNewsChange(ctx, tx, "categories", ""); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_changes WHERE kind = 'categories' AND seq < (SELECT MAX(seq) FROM news_changes)`); err != nil {
		return err
	}
	return tx.Commit()
}

func scanNewsRecord(scan func(dest ...interface{}) error) (NewsRecord, error) {
	var r NewsRecord
	var published, updated string
	if err := scan(&r.ID, &r.CategoryID, &r.TargetProfileID, &published, &updated, &r.ChangeSeq, &r.Payload); err != nil {
		return NewsRecord{}, err
	}
	var err error
	if r.PublishedAt, err = time.Parse(time.RFC3339, published); err != nil {
		return NewsRecord{}, err
	}
	if r.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return NewsRecord{}, err
	}
	return r, nil
}

const newsColumns = `id, category_id, target_profile_id, published_at, updated_at, change_seq, payload`

// NewsItem returns one live item.
func (s *SQLiteStore) NewsItem(ctx context.Context, id string) (NewsRecord, error) {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return NewsRecord{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+newsColumns+` FROM news_items WHERE id = ?`, id)
	r, err := scanNewsRecord(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return NewsRecord{}, ErrNewsNotFound
	}
	return r, err
}

// NewsItems returns every live item, newest first.
func (s *SQLiteStore) NewsItems(ctx context.Context) ([]NewsRecord, error) {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return nil, err
	}
	return listNewsItems(ctx, s.db, -1)
}

func listNewsItems(ctx context.Context, q queryer, after int64) ([]NewsRecord, error) {
	query := `SELECT ` + newsColumns + ` FROM news_items`
	var args []interface{}
	if after >= 0 {
		query += ` WHERE change_seq > ?`
		args = append(args, after)
	}
	rows, err := q.QueryContext(ctx, query+` ORDER BY published_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NewsRecord{}
	for rows.Next() {
		r, err := scanNewsRecord(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveNews creates (create=true) or replaces a live item and its object
// references in one transaction.
func (s *SQLiteStore) SaveNews(ctx context.Context, rec NewsRecord, create bool) (NewsRecord, error) {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return NewsRecord{}, err
	}
	newsMu.Lock()
	defer newsMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NewsRecord{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM news_items WHERE id = ?`, rec.ID).Scan(&exists); err != nil {
		return NewsRecord{}, err
	}
	if create {
		var live int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM news_items`).Scan(&live); err != nil {
			return NewsRecord{}, err
		}
		if live >= MaxNewsItems {
			return NewsRecord{}, ErrNewsLimitReached
		}
		if exists != 0 {
			return NewsRecord{}, fmt.Errorf("news id collision: %s", rec.ID)
		}
	} else if exists == 0 {
		return NewsRecord{}, ErrNewsNotFound
	}
	var category int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM news_categories WHERE id = ?`, rec.CategoryID).Scan(&category); err != nil {
		return NewsRecord{}, err
	}
	if category == 0 {
		return NewsRecord{}, ErrNewsCategoryUnset
	}
	seq, err := appendNewsChange(ctx, tx, "upsert", rec.ID)
	if err != nil {
		return NewsRecord{}, err
	}
	rec.ChangeSeq = seq
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO news_items(`+newsColumns+`) VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET category_id = excluded.category_id,
		  target_profile_id = excluded.target_profile_id, updated_at = excluded.updated_at,
		  change_seq = excluded.change_seq, payload = excluded.payload`,
		rec.ID, rec.CategoryID, rec.TargetProfileID, rec.PublishedAt.UTC().Format(time.RFC3339),
		rec.UpdatedAt.UTC().Format(time.RFC3339), seq, rec.Payload); err != nil {
		return NewsRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_objects WHERE news_id = ?`, rec.ID); err != nil {
		return NewsRecord{}, err
	}
	for _, o := range rec.Objects {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO news_objects(news_id, sha256, size) VALUES(?, ?, ?)`,
			rec.ID, o.SHA256, o.Size); err != nil {
			return NewsRecord{}, err
		}
	}
	// Only the last change of an item matters for sync.
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_changes WHERE kind = 'upsert' AND news_id = ? AND seq < ?`, rec.ID, seq); err != nil {
		return NewsRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return NewsRecord{}, err
	}
	return rec, nil
}

// DeleteNews removes a live item and leaves a tombstone.
func (s *SQLiteStore) DeleteNews(ctx context.Context, id string) error {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return err
	}
	newsMu.Lock()
	defer newsMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM news_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNewsNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_objects WHERE news_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_changes WHERE kind = 'upsert' AND news_id = ?`, id); err != nil {
		return err
	}
	if _, err := appendNewsChange(ctx, tx, "delete", id); err != nil {
		return err
	}
	return tx.Commit()
}

// NewsSync builds a consistent snapshot. since < 0 asks for a full one; a
// cursor newer than the database (restored backup, other server) is full too.
func (s *SQLiteStore) NewsSync(ctx context.Context, since int64) (NewsSnapshot, error) {
	if err := s.ensureNewsSchema(ctx); err != nil {
		return NewsSnapshot{}, err
	}
	newsMu.Lock()
	defer newsMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NewsSnapshot{}, err
	}
	defer tx.Rollback()
	var snap NewsSnapshot
	if snap.Cursor, err = newsCursor(ctx, tx); err != nil {
		return NewsSnapshot{}, err
	}
	snap.Full = since < 0 || since > snap.Cursor
	if snap.Categories, err = listNewsCategories(ctx, tx); err != nil {
		return NewsSnapshot{}, err
	}
	snap.Deleted = []string{}
	if snap.Full {
		snap.Items, err = listNewsItems(ctx, tx, -1)
		return snap, err
	}
	if snap.Items, err = listNewsItems(ctx, tx, since); err != nil {
		return NewsSnapshot{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT news_id FROM news_changes WHERE kind = 'delete' AND seq > ? ORDER BY seq`, since)
	if err != nil {
		return NewsSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return NewsSnapshot{}, err
		}
		snap.Deleted = append(snap.Deleted, id)
	}
	return snap, rows.Err()
}
