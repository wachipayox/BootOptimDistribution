package profileapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

type newsEnv struct {
	t        *testing.T
	h        http.Handler
	cas      *storage.CAS
	store    *storage.SQLiteStore
	revision string
}

func newNewsEnv(t *testing.T) *newsEnv {
	t.Helper()
	root := t.TempDir()
	cas, err := storage.OpenCAS(root, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenSQLite(root+"/metadata.sqlite3", cas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	pub, priv := testKeyPair()
	h, err := New(Dependencies{Objects: cas, Store: store, Keys: testKeys{"test-key": pub}},
		Options{ReadMiddleware: identity, AdminMiddleware: identity, MaxObjectBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	e := &newsEnv{t: t, h: h, cas: cas, store: store, revision: "rev_0000000000000001"}
	data := []byte("synthetic mod")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if err := cas.Put(context.Background(), storage.Object{SHA256: digest, Size: int64(len(data))}, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if r := e.do(http.MethodPost, "/v1/admin/revisions", string(signEnvelope(t, testManifest(e.revision, 1, digest, int64(len(data))), priv))); r.Code != 201 {
		t.Fatalf("publish fixture: %d %s", r.Code, r.Body.String())
	}
	return e
}

func (e *newsEnv) do(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *newsEnv) must(rec *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	e.t.Helper()
	if rec.Code != status {
		e.t.Fatalf("status %d want %d: %s", rec.Code, status, rec.Body.String())
	}
	return rec
}

func (e *newsEnv) code(rec *httptest.ResponseRecorder, status int, code string) string {
	e.t.Helper()
	if rec.Code != status || errorCode(rec) != code {
		e.t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body.String(), status, code)
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Error.Message
}

func (e *newsEnv) categories(names ...string) []storage.NewsCategory {
	e.t.Helper()
	var list []map[string]interface{}
	for i, n := range names {
		list = append(list, map[string]interface{}{"name": n, "order": (i + 1) * 10})
	}
	raw, _ := json.Marshal(list)
	rec := e.must(e.do(http.MethodPut, "/v1/admin/news-categories", string(raw)), 200)
	var out struct {
		Categories []storage.NewsCategory `json:"categories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out.Categories
}

func newsBody(fields map[string]interface{}, category string) string {
	m := map[string]interface{}{"title": "Titulo", "body": "Texto", "author": "Equipo", "category_id": category}
	for k, v := range fields {
		m[k] = v
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

type newsItemOut struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	PublishedAt     string  `json:"published_at"`
	UpdatedAt       string  `json:"updated_at"`
	ExpiresAt       *string `json:"expires_at"`
	TargetProfileID *string `json:"target_profile_id"`
	Pinned          bool    `json:"pinned"`
}

type newsFeed struct {
	SchemaVersion int                    `json:"schema_version"`
	ServerTime    string                 `json:"server_time"`
	Cursor        string                 `json:"cursor"`
	Full          bool                   `json:"full"`
	Categories    []storage.NewsCategory `json:"categories"`
	Items         []newsItemOut          `json:"items"`
	Deleted       []string               `json:"deleted"`
}

func (e *newsEnv) feed(query string, headers ...string) (newsFeed, *httptest.ResponseRecorder) {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/v1/news"+query, "", headers...)
	var f newsFeed
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
			e.t.Fatal(err)
		}
	}
	return f, rec
}

func (e *newsEnv) create(category string, fields map[string]interface{}) newsItemOut {
	e.t.Helper()
	rec := e.must(e.do(http.MethodPost, "/v1/admin/news", newsBody(fields, category)), 201)
	var out newsItemOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *newsEnv) stage(data []byte) (string, int64) {
	e.t.Helper()
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	e.must(e.do(http.MethodPost, "/v1/admin/objects/sha256/"+digest, string(data)), 200)
	return digest, int64(len(data))
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x += 7 {
		img.Set(x, x%h, color.RGBA{200, 10, 10, 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func coverJSON(digest string, size int64, media string) map[string]interface{} {
	return map[string]interface{}{"image": map[string]interface{}{"sha256": digest, "size": size, "media_type": media}}
}

func TestNewsLifecycleAndIncrementalSync(t *testing.T) {
	e := newNewsEnv(t)
	empty, rec := e.feed("")
	if rec.Code != 200 || !empty.Full || empty.SchemaVersion != 1 || empty.Cursor != "n1.0" || empty.Items == nil && !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("empty feed: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"deleted":[]`) || !strings.Contains(rec.Body.String(), `"categories":[]`) {
		t.Fatalf("empty arrays must not be null: %s", rec.Body.String())
	}
	if rec.Header().Get("ETag") != `"n1.0"` {
		t.Fatalf("etag %q", rec.Header().Get("ETag"))
	}

	cats := e.categories("Actualizacion", "Mantenimiento")
	if len(cats) != 2 || !strings.HasPrefix(cats[0].ID, "cat_") || cats[0].Name != "Actualizacion" {
		t.Fatalf("categories %+v", cats)
	}
	first := e.create(cats[0].ID, map[string]interface{}{"title": "Uno", "pinned": true})
	if !strings.HasPrefix(first.ID, "news_") || first.PublishedAt == "" || first.PublishedAt != first.UpdatedAt {
		t.Fatalf("created %+v", first)
	}

	f1, rec1 := e.feed("")
	if !f1.Full || len(f1.Items) != 1 || f1.Items[0].ID != first.ID || len(f1.Categories) != 2 || len(f1.Deleted) != 0 {
		t.Fatalf("full feed %+v", f1)
	}
	if rec1.Header().Get("ETag") != `"`+f1.Cursor+`"` {
		t.Fatalf("etag %q cursor %q", rec1.Header().Get("ETag"), f1.Cursor)
	}
	// 304 with the same cursor (also with a weak validator and a list).
	for _, inm := range []string{`"` + f1.Cursor + `"`, `W/"` + f1.Cursor + `"`, `"x", "` + f1.Cursor + `"`} {
		if _, r := e.feed("", "If-None-Match", inm); r.Code != http.StatusNotModified || r.Body.Len() != 0 {
			t.Fatalf("If-None-Match %s: %d %s", inm, r.Code, r.Body.String())
		}
	}
	if _, r := e.feed("", "If-None-Match", `"n1.1"`); r.Code != 200 {
		t.Fatalf("stale etag must not 304: %d", r.Code)
	}

	second := e.create(cats[1].ID, map[string]interface{}{"title": "Dos"})
	inc, _ := e.feed("?since=" + f1.Cursor)
	if inc.Full || len(inc.Items) != 1 || inc.Items[0].ID != second.ID || len(inc.Deleted) != 0 || len(inc.Categories) != 2 {
		t.Fatalf("incremental after create %+v", inc)
	}
	same, _ := e.feed("?since=" + inc.Cursor)
	if same.Full || len(same.Items) != 0 || len(same.Deleted) != 0 || same.Cursor != inc.Cursor {
		t.Fatalf("nothing new %+v", same)
	}

	// Edit keeps id and published_at, moves updated_at and the cursor.
	body := newsBody(map[string]interface{}{"title": "Uno editado", "pinned": false}, cats[0].ID)
	e.must(e.do(http.MethodPut, "/v1/admin/news/"+first.ID, body), 200)
	edited, _ := e.feed("?since=" + inc.Cursor)
	if edited.Full || len(edited.Items) != 1 || edited.Items[0].ID != first.ID || edited.Items[0].Title != "Uno editado" ||
		edited.Items[0].PublishedAt != first.PublishedAt || edited.Items[0].Pinned || edited.Cursor == inc.Cursor {
		t.Fatalf("incremental after edit %+v", edited)
	}

	// Delete leaves a tombstone for old cursors.
	e.must(e.do(http.MethodDelete, "/v1/admin/news/"+second.ID, ""), 200)
	del, _ := e.feed("?since=" + edited.Cursor)
	if del.Full || len(del.Items) != 0 || len(del.Deleted) != 1 || del.Deleted[0] != second.ID {
		t.Fatalf("incremental after delete %+v", del)
	}
	old, _ := e.feed("?since=" + f1.Cursor)
	if old.Full || len(old.Items) != 1 || old.Items[0].ID != first.ID || len(old.Deleted) != 1 || old.Deleted[0] != second.ID {
		t.Fatalf("old cursor %+v", old)
	}
	e.code(e.do(http.MethodDelete, "/v1/admin/news/"+second.ID, ""), 404, "news_not_found")
	e.code(e.do(http.MethodPut, "/v1/admin/news/"+second.ID, newsBody(nil, cats[0].ID)), 404, "news_not_found")

	// Unknown, malformed or future cursors give a full snapshot.
	for _, since := range []string{"?since=n1.999999", "?since=garbage", "?since=n2.1", "?since=n1.-1", "?since=n1.01", "?since=n1."} {
		full, _ := e.feed(since)
		if !full.Full || len(full.Items) != 1 || len(full.Deleted) != 0 {
			t.Fatalf("%s: %+v", since, full)
		}
	}

	// Category changes move the cursor so clients refresh them.
	before, _ := e.feed("")
	withNew, _ := json.Marshal([]map[string]interface{}{
		{"id": cats[0].ID, "name": cats[0].Name, "order": 10},
		{"id": cats[1].ID, "name": cats[1].Name, "order": 20},
		{"name": "Otra", "order": 30},
	})
	e.must(e.do(http.MethodPut, "/v1/admin/news-categories", string(withNew)), 200)
	after, _ := e.feed("?since=" + before.Cursor)
	if after.Cursor == before.Cursor || len(after.Categories) != 3 || after.Full {
		t.Fatalf("categories change %+v", after)
	}
	if _, r := e.feed("", "If-None-Match", `"`+before.Cursor+`"`); r.Code != 200 {
		t.Fatal("category change must invalidate the ETag")
	}

	// HEAD answers headers only.
	head := e.do(http.MethodHead, "/v1/news", "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") == "" {
		t.Fatalf("HEAD %d", head.Code)
	}
	e.must(e.do(http.MethodPost, "/v1/news", "{}"), 405)

	// Admin list includes everything alive.
	list := e.must(e.do(http.MethodGet, "/v1/admin/news", ""), 200)
	if !strings.Contains(list.Body.String(), first.ID) || strings.Contains(list.Body.String(), second.ID) {
		t.Fatal(list.Body.String())
	}
}

func TestNewsExpiredStayInFeedAndExpiryRules(t *testing.T) {
	e := newNewsEnv(t)
	cat := e.categories("General")[0].ID
	e.code(e.do(http.MethodPost, "/v1/admin/news", newsBody(map[string]interface{}{"expires_at": "2000-01-01T00:00:00Z"}, cat)), 422, "invalid_news")
	e.code(e.do(http.MethodPost, "/v1/admin/news", newsBody(map[string]interface{}{"expires_at": "ayer"}, cat)), 422, "invalid_news")
	item := e.create(cat, map[string]interface{}{"expires_at": "2099-01-01T00:00:00+02:00"})
	if item.ExpiresAt == nil || *item.ExpiresAt != "2098-12-31T22:00:00Z" {
		t.Fatalf("expires_at %v", item.ExpiresAt)
	}
	// Editing to a date before published_at is rejected; null makes it permanent.
	e.code(e.do(http.MethodPut, "/v1/admin/news/"+item.ID, newsBody(map[string]interface{}{"expires_at": item.PublishedAt}, cat)), 422, "invalid_news")
	e.must(e.do(http.MethodPut, "/v1/admin/news/"+item.ID, newsBody(map[string]interface{}{"expires_at": nil}, cat)), 200)
}

func TestNewsFieldValidation(t *testing.T) {
	e := newNewsEnv(t)
	cat := e.categories("General")[0].ID
	long := func(n int) string { return strings.Repeat("a", n) }
	bad := []struct {
		name   string
		fields map[string]interface{}
		code   string
	}{
		{"empty title", map[string]interface{}{"title": " "}, "invalid_news"},
		{"long title", map[string]interface{}{"title": long(161)}, "invalid_news"},
		{"multiline title", map[string]interface{}{"title": "a\nb"}, "invalid_news"},
		{"empty author", map[string]interface{}{"author": ""}, "invalid_news"},
		{"long author", map[string]interface{}{"author": long(65)}, "invalid_news"},
		{"unknown category", map[string]interface{}{"category_id": "cat_nope"}, "invalid_news"},
		{"unknown target", map[string]interface{}{"target_profile_id": "profile_nope"}, "invalid_news"},
		{"test target", map[string]interface{}{"target_profile_id": "profile_test-x"}, "invalid_news"},
		{"empty target", map[string]interface{}{"target_profile_id": ""}, "invalid_news"},
		{"unknown revision", map[string]interface{}{"linked_revision": map[string]string{"profile_id": "profile_root", "revision_id": "rev_9999999999999999"}}, "invalid_news"},
		{"revision of another profile", map[string]interface{}{"linked_revision": map[string]string{"profile_id": "profile_other", "revision_id": e.revision}}, "invalid_news"},
		{"body too long", map[string]interface{}{"body": long(65537)}, "invalid_news"},
		{"html in body", map[string]interface{}{"body": "<script>x</script>"}, "invalid_news_body"},
		{"javascript link", map[string]interface{}{"body": "[x](javascript:alert(1))"}, "invalid_news_body"},
		{"external image http", map[string]interface{}{"image": map[string]string{"url": "http://example.com/a.jpg"}}, "invalid_image"},
		{"external image long", map[string]interface{}{"image": map[string]string{"url": "https://example.com/" + long(2040)}}, "invalid_image"},
		{"image url and sha", map[string]interface{}{"image": map[string]interface{}{"url": "https://example.com/a.jpg", "sha256": strings.Repeat("a", 64)}}, "invalid_image"},
		{"empty image object", map[string]interface{}{"image": map[string]interface{}{}}, "invalid_image"},
	}
	for _, c := range bad {
		fields := c.fields
		category := cat
		if v, ok := fields["category_id"].(string); ok {
			category = v
		}
		e.code(e.do(http.MethodPost, "/v1/admin/news", newsBody(fields, category)), 422, c.code)
	}
	// Unknown JSON fields and bad JSON are 400.
	e.must(e.do(http.MethodPost, "/v1/admin/news", `{"title":"x","published_at":"2026-01-01T00:00:00Z"}`), 400)
	e.must(e.do(http.MethodPost, "/v1/admin/news", `nope`), 400)

	// Valid optional fields.
	item := e.create(cat, map[string]interface{}{
		"target_profile_id": "profile_root",
		"linked_revision":   map[string]string{"profile_id": "profile_root", "revision_id": e.revision},
		"image":             map[string]string{"url": "https://example.com/portada.jpg"},
	})
	if item.TargetProfileID == nil || *item.TargetProfileID != "profile_root" {
		t.Fatalf("target %+v", item)
	}
	// A body error carries the line.
	msg := e.code(e.do(http.MethodPost, "/v1/admin/news", newsBody(map[string]interface{}{"body": "ok\n\n<div>x</div>"}, cat)), 422, "invalid_news_body")
	if !strings.Contains(msg, "line 3") {
		t.Fatalf("message %q lacks the line", msg)
	}
}

func TestNewsCategories(t *testing.T) {
	e := newNewsEnv(t)
	cats := e.categories("Beta", "Alfa")
	if cats[0].Name != "Beta" || cats[1].Name != "Alfa" {
		t.Fatalf("ordering by order then name: %+v", cats)
	}
	// Rename keeps the id; order changes the listing.
	renamed, _ := json.Marshal([]map[string]interface{}{
		{"id": cats[0].ID, "name": "Beta 2", "order": 50},
		{"id": cats[1].ID, "name": "Alfa", "order": 5},
	})
	rec := e.must(e.do(http.MethodPut, "/v1/admin/news-categories", string(renamed)), 200)
	var got struct {
		Categories []storage.NewsCategory `json:"categories"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Categories[0].ID != cats[1].ID || got.Categories[1].ID != cats[0].ID || got.Categories[1].Name != "Beta 2" {
		t.Fatalf("renamed %+v", got.Categories)
	}
	// Names are unique ignoring case; bounded; one line.
	e.code(e.do(http.MethodPut, "/v1/admin/news-categories", `[{"name":"a","order":1},{"name":"A","order":2}]`), 422, "invalid_news")
	e.code(e.do(http.MethodPut, "/v1/admin/news-categories", `[{"name":"","order":1}]`), 422, "invalid_news")
	e.code(e.do(http.MethodPut, "/v1/admin/news-categories", `[{"name":"`+strings.Repeat("a", 33)+`","order":1}]`), 422, "invalid_news")
	e.code(e.do(http.MethodPut, "/v1/admin/news-categories", `[{"id":"cat_unknown","name":"x","order":1}]`), 422, "invalid_news")
	var many []map[string]interface{}
	for i := 0; i < 33; i++ {
		many = append(many, map[string]interface{}{"name": "c" + string(rune('a'+i)), "order": i})
	}
	raw, _ := json.Marshal(many)
	e.code(e.do(http.MethodPut, "/v1/admin/news-categories", string(raw)), 422, "invalid_news")
	e.must(e.do(http.MethodPut, "/v1/admin/news-categories", `[]`), 200) // nothing uses them yet
	cats = e.categories("Uno", "Dos")

	// A category with news cannot be removed.
	item := e.create(cats[0].ID, nil)
	keepOnlyDos, _ := json.Marshal([]map[string]interface{}{{"id": cats[1].ID, "name": "Dos", "order": 20}})
	e.code(e.do(http.MethodPut, "/v1/admin/news-categories", string(keepOnlyDos)), 409, "category_in_use")
	if after := e.must(e.do(http.MethodGet, "/v1/admin/news-categories", ""), 200); !strings.Contains(after.Body.String(), cats[0].ID) {
		t.Fatal("failed replacement must change nothing")
	}
	e.must(e.do(http.MethodDelete, "/v1/admin/news/"+item.ID, ""), 200)
	e.must(e.do(http.MethodPut, "/v1/admin/news-categories", string(keepOnlyDos)), 200)
}

func TestNewsCoverImageValidation(t *testing.T) {
	e := newNewsEnv(t)
	cat := e.categories("General")[0].ID
	post := func(fields map[string]interface{}) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/v1/admin/news", newsBody(fields, cat))
	}

	good := jpegBytes(t, 1600, 900)
	digest, size := e.stage(good)
	// Staged and unreferenced: not public.
	e.must(e.do(http.MethodGet, "/v1/objects/sha256/"+digest, ""), 404)
	item := e.must(post(coverJSON(digest, size, "image/jpeg")), 201)
	var created newsItemOut
	json.Unmarshal(item.Body.Bytes(), &created)
	e.must(e.do(http.MethodGet, "/v1/objects/sha256/"+digest, ""), 200)

	// The same bytes are not public after the only news referencing them goes.
	e.must(e.do(http.MethodDelete, "/v1/admin/news/"+created.ID, ""), 200)
	e.must(e.do(http.MethodGet, "/v1/objects/sha256/"+digest, ""), 404)

	// Tolerance of 1 %: 1600x906 passes, 1600x920 does not.
	d2, s2 := e.stage(jpegBytes(t, 1600, 906))
	e.must(post(coverJSON(d2, s2, "image/jpeg")), 201)
	d3, s3 := e.stage(jpegBytes(t, 1600, 920))
	e.code(post(coverJSON(d3, s3, "image/jpeg")), 422, "invalid_image")
	// PNG is accepted, with the right media type only.
	d4, s4 := e.stage(pngBytes(t, 1280, 720))
	e.must(post(coverJSON(d4, s4, "image/png")), 201)
	e.code(post(coverJSON(d4, s4, "image/jpeg")), 422, "invalid_image")
	// Width bounds.
	d5, s5 := e.stage(jpegBytes(t, 320, 180))
	e.code(post(coverJSON(d5, s5, "image/jpeg")), 422, "invalid_image")
	d6, s6 := e.stage(jpegBytes(t, 3856, 2169))
	e.code(post(coverJSON(d6, s6, "image/jpeg")), 422, "invalid_image")
	// Declared size must match; bytes must be an image; object must exist; media type allowed.
	e.code(post(coverJSON(digest, size+1, "image/jpeg")), 422, "invalid_image")
	d7, s7 := e.stage([]byte("not an image at all"))
	e.code(post(coverJSON(d7, s7, "image/jpeg")), 422, "invalid_image")
	e.code(post(coverJSON(strings.Repeat("9", 64), 100, "image/jpeg")), 422, "invalid_image")
	e.code(post(coverJSON(digest, size, "image/gif")), 422, "invalid_image")
	e.code(post(coverJSON("XYZ", size, "image/jpeg")), 422, "invalid_image")
	e.code(post(coverJSON(digest, 0, "image/jpeg")), 422, "invalid_image")
	// A cover over 4 MiB is refused by size alone.
	e.code(post(coverJSON(digest, 4<<20+1, "image/jpeg")), 422, "invalid_image")
}

func TestNewsBodyCASImages(t *testing.T) {
	e := newNewsEnv(t)
	cat := e.categories("General")[0].ID
	digest, _ := e.stage(pngBytes(t, 300, 200))
	missing := strings.Repeat("8", 64)

	e.code(e.do(http.MethodPost, "/v1/admin/news", newsBody(map[string]interface{}{"body": "![x](cas:" + missing + ")"}, cat)), 422, "invalid_news_body")
	notImage, _ := e.stage([]byte("plain text, not an image"))
	e.code(e.do(http.MethodPost, "/v1/admin/news", newsBody(map[string]interface{}{"body": "![x](cas:" + notImage + ")"}, cat)), 422, "invalid_news_body")

	e.must(e.do(http.MethodGet, "/v1/objects/sha256/"+digest, ""), 404)
	rec := e.must(e.do(http.MethodPost, "/v1/admin/news", newsBody(map[string]interface{}{"body": "Hola\n\n![x](cas:" + digest + ")"}, cat)), 201)
	var item newsItemOut
	json.Unmarshal(rec.Body.Bytes(), &item)
	e.must(e.do(http.MethodGet, "/v1/objects/sha256/"+digest, ""), 200)

	// Editing the image out of the body stops serving it.
	e.must(e.do(http.MethodPut, "/v1/admin/news/"+item.ID, newsBody(map[string]interface{}{"body": "Sin imagen"}, cat)), 200)
	e.must(e.do(http.MethodGet, "/v1/objects/sha256/"+digest, ""), 404)
}

func TestNewsRoutesRequireAdminAccess(t *testing.T) {
	root := t.TempDir()
	cas, _ := storage.OpenCAS(root, 1<<20)
	store, err := storage.OpenSQLite(root+"/metadata.sqlite3", cas)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pub, _ := testKeyPair()
	deny := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Admin") != "yes" {
				http.Error(w, "no session", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	h, err := New(Dependencies{Objects: cas, Store: store, Keys: testKeys{"test-key": pub}},
		Options{ReadMiddleware: identity, AdminMiddleware: deny})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/admin/news"}, {"POST", "/v1/admin/news"}, {"PUT", "/v1/admin/news/news_x"},
		{"DELETE", "/v1/admin/news/news_x"}, {"GET", "/v1/admin/news-categories"}, {"PUT", "/v1/admin/news-categories"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader("[]")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without session: %d", c.method, c.path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/news", nil))
	if rec.Code != 200 {
		t.Fatalf("public news needs no session: %d", rec.Code)
	}
}
