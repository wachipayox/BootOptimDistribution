package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewsLimitCursorAndTombstones(t *testing.T) {
	ctx := context.Background()
	_, store := newStores(t)
	if err := store.ReplaceNewsCategories(ctx, []NewsCategory{{ID: "cat_a", Name: "A", Order: 1}}); err != nil {
		t.Fatal(err)
	}
	// Fill to the limit with direct inserts in one transaction (fsync-friendly).
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxNewsItems; i++ {
		id, _ := NewNewsID("news_", 12)
		if _, err := tx.Exec(`INSERT INTO news_items(id, category_id, published_at, updated_at, change_seq, payload) VALUES(?, 'cat_a', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0, '{}')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rec := NewsRecord{ID: "news_overflow00", CategoryID: "cat_a", PublishedAt: time.Now(), UpdatedAt: time.Now(), Payload: []byte("{}")}
	if _, err := store.SaveNews(ctx, rec, true); !errors.Is(err, ErrNewsLimitReached) {
		t.Fatalf("create over the limit: %v", err)
	}
	items, err := store.NewsItems(ctx)
	if err != nil || len(items) != MaxNewsItems {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	// Deleting one frees a slot.
	if err := store.DeleteNews(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNews(ctx, rec, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNews(ctx, NewsRecord{ID: "news_x", CategoryID: "cat_missing", PublishedAt: time.Now(), UpdatedAt: time.Now(), Payload: []byte("{}")}, false); !errors.Is(err, ErrNewsNotFound) {
		t.Fatalf("edit of a missing item: %v", err)
	}
}

func TestNewsCursorOnlyGrowsAndSurvivesPruning(t *testing.T) {
	ctx := context.Background()
	_, store := newStores(t)
	if c, err := store.NewsCursor(ctx); err != nil || c != 0 {
		t.Fatalf("empty cursor %d %v", c, err)
	}
	if err := store.ReplaceNewsCategories(ctx, []NewsCategory{{ID: "cat_a", Name: "A"}}); err != nil {
		t.Fatal(err)
	}
	var last int64
	step := func(what string) {
		t.Helper()
		c, err := store.NewsCursor(ctx)
		if err != nil || c <= last {
			t.Fatalf("%s: cursor %d after %d (%v)", what, c, last, err)
		}
		last = c
	}
	step("categories")
	rec := NewsRecord{ID: "news_one", CategoryID: "cat_a", PublishedAt: time.Now(), UpdatedAt: time.Now(), Payload: []byte("{}")}
	for i := 0; i < 3; i++ {
		if _, err := store.SaveNews(ctx, rec, i == 0); err != nil {
			t.Fatal(err)
		}
		step("save")
	}
	if err := store.DeleteNews(ctx, "news_one"); err != nil {
		t.Fatal(err)
	}
	step("delete")
	snap, err := store.NewsSync(ctx, 1)
	if err != nil || snap.Full || len(snap.Deleted) != 1 || len(snap.Items) != 0 {
		t.Fatalf("snapshot %+v %v", snap, err)
	}
	snap, err = store.NewsSync(ctx, last+10)
	if err != nil || !snap.Full {
		t.Fatalf("future cursor must be full: %+v %v", snap, err)
	}
}

func TestParseNewsCursor(t *testing.T) {
	for in, want := range map[string]int64{"n1.0": 0, "n1.7": 7, "n1.4821": 4821} {
		if got, ok := ParseNewsCursor(in); !ok || got != want || FormatNewsCursor(got) != in {
			t.Errorf("%s: %d %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "n1.", "n1.-3", "n1.01", "n2.1", "1", "n1.1x", "n1. 1", "n1.99999999999999999999"} {
		if _, ok := ParseNewsCursor(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}
