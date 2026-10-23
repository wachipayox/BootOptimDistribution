package profileapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wachipayox/BootOptimDistribution/internal/news"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

// Limits of the news contract (docs/PROFILE_PROTOCOL.md, «Noticias»).
const (
	newsTitleMax        = 160
	newsAuthorMax       = 64
	newsCategoryMax     = 32
	newsCoverMaxBytes   = 4 << 20
	newsBodyImageMax    = 10 << 20
	newsCoverMinWidth   = 640
	newsCoverMaxWidth   = 3840
	newsBodyImageMaxDim = 4096
)

// newsStore is implemented by the SQLite store; other stores simply have no
// news and the routes answer 503.
type newsStore interface {
	NewsCursor(context.Context) (int64, error)
	NewsCategories(context.Context) ([]storage.NewsCategory, error)
	ReplaceNewsCategories(context.Context, []storage.NewsCategory) error
	NewsItem(context.Context, string) (storage.NewsRecord, error)
	NewsItems(context.Context) ([]storage.NewsRecord, error)
	SaveNews(context.Context, storage.NewsRecord, bool) (storage.NewsRecord, error)
	DeleteNews(context.Context, string) error
	NewsSync(context.Context, int64) (storage.NewsSnapshot, error)
}

type newsImage struct {
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	URL       string `json:"url,omitempty"`
}

type newsLinkedRevision struct {
	ProfileID  string `json:"profile_id"`
	RevisionID string `json:"revision_id"`
}

// newsItemView is the public form of a news item.
type newsItemView struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	Body            string              `json:"body"`
	Image           *newsImage          `json:"image"`
	Author          string              `json:"author"`
	CategoryID      string              `json:"category_id"`
	TargetProfileID *string             `json:"target_profile_id"`
	Pinned          bool                `json:"pinned"`
	PublishedAt     string              `json:"published_at"`
	UpdatedAt       string              `json:"updated_at"`
	ExpiresAt       *string             `json:"expires_at"`
	LinkedRevision  *newsLinkedRevision `json:"linked_revision"`
}

// newsInput holds the editable fields of POST/PUT /v1/admin/news.
type newsInput struct {
	Title           string              `json:"title"`
	Body            string              `json:"body"`
	Image           *newsImage          `json:"image"`
	Author          string              `json:"author"`
	CategoryID      string              `json:"category_id"`
	TargetProfileID *string             `json:"target_profile_id"`
	Pinned          bool                `json:"pinned"`
	ExpiresAt       *string             `json:"expires_at"`
	LinkedRevision  *newsLinkedRevision `json:"linked_revision"`
}

type newsFieldError struct {
	code    string
	message string
	status  int
}

func (e *newsFieldError) Error() string { return e.message }

func invalidNews(format string, args ...interface{}) *newsFieldError {
	return &newsFieldError{code: "invalid_news", message: fmt.Sprintf(format, args...), status: http.StatusUnprocessableEntity}
}

func invalidNewsImage(format string, args ...interface{}) *newsFieldError {
	return &newsFieldError{code: "invalid_image", message: fmt.Sprintf(format, args...), status: http.StatusUnprocessableEntity}
}

func (a *API) needNewsStore(w http.ResponseWriter) (newsStore, bool) {
	store, ok := a.store.(newsStore)
	if !ok {
		a.writeError(w, http.StatusServiceUnavailable, "unavailable", "news storage unavailable")
	}
	return store, ok
}

func (a *API) writeNewsError(w http.ResponseWriter, err error) {
	var fe *newsFieldError
	switch {
	case errors.As(err, &fe):
		a.writeError(w, fe.status, fe.code, fe.message)
	case errors.Is(err, storage.ErrNewsNotFound):
		a.writeError(w, http.StatusNotFound, "news_not_found", "news item not found")
	case errors.Is(err, storage.ErrNewsLimitReached):
		a.writeError(w, http.StatusConflict, "news_limit_reached", fmt.Sprintf("at most %d news items can exist; delete old ones", storage.MaxNewsItems))
	case errors.Is(err, storage.ErrNewsCategoryInUse):
		a.writeError(w, http.StatusConflict, "category_in_use", "a category with news cannot be deleted; rename it or move its news first")
	case errors.Is(err, storage.ErrNewsCategoryUnset):
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_news", "category_id: category does not exist")
	default:
		a.writeError(w, http.StatusInternalServerError, "storage_error", "news storage failed")
	}
}

// ---- public read ----

func (a *API) handleNews(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		a.methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	store, ok := a.needNewsStore(w)
	if !ok {
		return
	}
	since := int64(-1)
	if raw := r.URL.Query().Get("since"); raw != "" {
		if seq, valid := storage.ParseNewsCursor(raw); valid {
			since = seq
		}
	}
	cursor, err := store.NewsCursor(r.Context())
	if err != nil {
		a.writeNewsError(w, err)
		return
	}
	etag := `"` + storage.FormatNewsCursor(cursor) + `"`
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-BootOptim-Protocol-Version", strconv.Itoa(ProtocolVersion))
		w.WriteHeader(http.StatusNotModified)
		return
	}
	snap, err := store.NewsSync(r.Context(), since)
	if err != nil {
		a.writeNewsError(w, err)
		return
	}
	items := make([]json.RawMessage, 0, len(snap.Items))
	for _, item := range snap.Items {
		items = append(items, json.RawMessage(item.Payload))
	}
	categories := snap.Categories
	if categories == nil {
		categories = []storage.NewsCategory{}
	}
	body := struct {
		SchemaVersion int                    `json:"schema_version"`
		ServerTime    string                 `json:"server_time"`
		Cursor        string                 `json:"cursor"`
		Full          bool                   `json:"full"`
		Categories    []storage.NewsCategory `json:"categories"`
		Items         []json.RawMessage      `json:"items"`
		Deleted       []string               `json:"deleted"`
	}{1, time.Now().UTC().Format(time.RFC3339), storage.FormatNewsCursor(snap.Cursor), snap.Full, categories, items, snap.Deleted}
	// The ETag is the cursor of the data actually returned.
	w.Header().Set("ETag", `"`+body.Cursor+`"`)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-BootOptim-Protocol-Version", strconv.Itoa(ProtocolVersion))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if part == etag {
			return true
		}
	}
	return false
}

// ---- admin routes ----

// serveAdminNews handles the news routes and reports whether it did.
func (a *API) serveAdminNews(w http.ResponseWriter, r *http.Request) bool {
	parts := pathParts(r.URL.Path)
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "admin" {
		return false
	}
	switch {
	case len(parts) == 3 && parts[2] == "news":
		switch r.Method {
		case http.MethodGet:
			a.handleAdminNewsList(w, r)
		case http.MethodPost:
			a.handleAdminNewsSave(w, r, "")
		default:
			a.methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	case len(parts) == 4 && parts[2] == "news":
		switch r.Method {
		case http.MethodPut:
			a.handleAdminNewsSave(w, r, parts[3])
		case http.MethodDelete:
			a.handleAdminNewsDelete(w, r, parts[3])
		default:
			a.methodNotAllowed(w, http.MethodPut, http.MethodDelete)
		}
	case len(parts) == 3 && parts[2] == "news-categories":
		switch r.Method {
		case http.MethodGet:
			a.handleAdminCategories(w, r)
		case http.MethodPut:
			a.handleAdminCategoriesReplace(w, r)
		default:
			a.methodNotAllowed(w, http.MethodGet, http.MethodPut)
		}
	default:
		return false
	}
	return true
}

func (a *API) handleAdminNewsList(w http.ResponseWriter, r *http.Request) {
	store, ok := a.needNewsStore(w)
	if !ok {
		return
	}
	records, err := store.NewsItems(r.Context())
	if err != nil {
		a.writeNewsError(w, err)
		return
	}
	items := make([]json.RawMessage, 0, len(records))
	for _, rec := range records {
		items = append(items, json.RawMessage(rec.Payload))
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion int               `json:"schema_version"`
		Items         []json.RawMessage `json:"items"`
	}{SchemaVersion, items})
}

func (a *API) handleAdminNewsDelete(w http.ResponseWriter, r *http.Request, id string) {
	store, ok := a.needNewsStore(w)
	if !ok {
		return
	}
	if err := store.DeleteNews(r.Context(), id); err != nil {
		a.writeNewsError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, okBody{SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion, Status: "deleted"})
}

// handleAdminNewsSave creates (id == "") or replaces a news item.
func (a *API) handleAdminNewsSave(w http.ResponseWriter, r *http.Request, id string) {
	store, ok := a.needNewsStore(w)
	if !ok {
		return
	}
	var in newsInput
	if err := a.decodeJSON(w, r, &in); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			a.writeBodyReadError(w, err)
			return
		}
		a.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	create := id == ""
	now := time.Now().UTC().Truncate(time.Second)
	published := now
	if create {
		var err error
		if id, err = storage.NewNewsID("news_", 12); err != nil {
			a.writeNewsError(w, err)
			return
		}
	} else {
		existing, err := store.NewsItem(r.Context(), id)
		if err != nil {
			a.writeNewsError(w, err)
			return
		}
		published = existing.PublishedAt
	}
	view, objects, err := a.buildNews(r.Context(), store, in, id, published, now)
	if err != nil {
		a.writeNewsError(w, err)
		return
	}
	payload, _ := json.Marshal(view)
	rec := storage.NewsRecord{
		ID: id, CategoryID: view.CategoryID, PublishedAt: published, UpdatedAt: now,
		Payload: payload, Objects: objects,
	}
	if view.TargetProfileID != nil {
		rec.TargetProfileID = *view.TargetProfileID
	}
	if _, err := store.SaveNews(r.Context(), rec, create); err != nil {
		a.writeNewsError(w, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	a.writeJSON(w, status, json.RawMessage(payload))
}

// oneLine trims s and checks it is a single line of 1..max bytes.
func oneLine(field, s string, max int) (string, *newsFieldError) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalidNews("%s: required", field)
	}
	if len(s) > max {
		return "", invalidNews("%s: longer than %d bytes", field, max)
	}
	if !utf8.ValidString(s) {
		return "", invalidNews("%s: not valid UTF-8", field)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 {
			return "", invalidNews("%s: must be a single line without control characters", field)
		}
	}
	return s, nil
}

func (a *API) buildNews(ctx context.Context, store newsStore, in newsInput, id string, published, now time.Time) (newsItemView, []storage.NewsObject, error) {
	var view newsItemView
	var err *newsFieldError
	view.ID = id
	if view.Title, err = oneLine("title", in.Title, newsTitleMax); err != nil {
		return view, nil, err
	}
	if view.Author, err = oneLine("author", in.Author, newsAuthorMax); err != nil {
		return view, nil, err
	}
	view.Pinned = in.Pinned
	view.PublishedAt = published.Format(time.RFC3339)
	view.UpdatedAt = now.Format(time.RFC3339)

	categories, cerr := store.NewsCategories(ctx)
	if cerr != nil {
		return view, nil, cerr
	}
	found := false
	for _, c := range categories {
		if c.ID == in.CategoryID {
			found = true
		}
	}
	if !found {
		return view, nil, invalidNews("category_id: category does not exist")
	}
	view.CategoryID = in.CategoryID

	if in.TargetProfileID != nil {
		target := *in.TargetProfileID
		if target == "" || isTestProfileID(target) {
			return view, nil, invalidNews("target_profile_id: must be null or a global profile")
		}
		if _, perr := a.store.ProfileHead(ctx, target); perr != nil {
			if errors.Is(perr, storage.ErrRevisionNotFound) {
				return view, nil, invalidNews("target_profile_id: profile does not exist")
			}
			return view, nil, perr
		}
		view.TargetProfileID = &target
	}

	if in.ExpiresAt != nil {
		expires, perr := time.Parse(time.RFC3339, *in.ExpiresAt)
		if perr != nil {
			return view, nil, invalidNews("expires_at: must be null or an RFC 3339 date")
		}
		expires = expires.UTC().Truncate(time.Second)
		if !expires.After(published) {
			return view, nil, invalidNews("expires_at: must be after published_at")
		}
		text := expires.Format(time.RFC3339)
		view.ExpiresAt = &text
	}

	if in.LinkedRevision != nil {
		lr := *in.LinkedRevision
		if isTestProfileID(lr.ProfileID) {
			return view, nil, invalidNews("linked_revision: test profiles cannot be linked")
		}
		stored, rerr := a.store.SignedRevision(ctx, lr.RevisionID)
		if rerr != nil {
			if errors.Is(rerr, storage.ErrRevisionNotFound) {
				return view, nil, invalidNews("linked_revision: revision does not exist")
			}
			return view, nil, rerr
		}
		if stored.ProfileID != lr.ProfileID {
			return view, nil, invalidNews("linked_revision: revision does not belong to profile_id")
		}
		view.LinkedRevision = &lr
	}

	var objects []storage.NewsObject
	if in.Image != nil {
		img := *in.Image
		cover, ierr := a.validateNewsCover(ctx, img)
		if ierr != nil {
			return view, nil, ierr
		}
		if cover != nil {
			objects = append(objects, *cover)
		}
		view.Image = &img
	}

	if len(in.Body) > news.MaxBodyBytes {
		return view, nil, invalidNews("body: longer than %d bytes", news.MaxBodyBytes)
	}
	var bodyErr error
	info, berr := news.Validate(in.Body, func(sha string) bool {
		if bodyErr != nil {
			return false
		}
		ok, err := a.validNewsBodyImage(ctx, sha)
		if err != nil {
			bodyErr = err
		}
		return ok
	})
	if bodyErr != nil {
		return view, nil, bodyErr
	}
	if berr != nil {
		return view, nil, &newsFieldError{
			code: "invalid_news_body", status: http.StatusUnprocessableEntity,
			message: fmt.Sprintf("line %d: %s", berr.Line, berr.Message),
		}
	}
	view.Body = in.Body
	for _, sha := range info.CASImages {
		size, serr := a.objectSize(ctx, sha)
		if serr != nil {
			return view, nil, serr
		}
		objects = append(objects, storage.NewsObject{SHA256: sha, Size: size})
	}
	return view, dedupeObjects(objects), nil
}

func dedupeObjects(in []storage.NewsObject) []storage.NewsObject {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, o := range in {
		if !seen[o.SHA256] {
			seen[o.SHA256] = true
			out = append(out, o)
		}
	}
	return out
}

func (a *API) objectSize(ctx context.Context, sha string) (int64, error) {
	f, size, err := a.objects.Open(ctx, sha)
	if err != nil {
		return 0, err
	}
	f.Close()
	return size, nil
}

// validateNewsCover checks the cover. External images return a nil object.
func (a *API) validateNewsCover(ctx context.Context, img newsImage) (*storage.NewsObject, error) {
	if img.URL != "" {
		if img.SHA256 != "" || img.Size != 0 || img.MediaType != "" {
			return nil, invalidNewsImage("image: use either url or sha256/size/media_type")
		}
		if err := news.CheckHTTPSURL(img.URL); err != nil {
			return nil, invalidNewsImage("image.url: %v", err)
		}
		return nil, nil
	}
	if !validDigest(img.SHA256) {
		return nil, invalidNewsImage("image: needs a url or a lowercase sha256")
	}
	if img.MediaType != "image/jpeg" && img.MediaType != "image/png" {
		return nil, invalidNewsImage("image.media_type: must be image/jpeg or image/png")
	}
	if img.Size < 1 || img.Size > newsCoverMaxBytes {
		return nil, invalidNewsImage("image.size: must be 1 to %d bytes", newsCoverMaxBytes)
	}
	f, size, err := a.objects.Open(ctx, img.SHA256)
	if err != nil {
		if errors.Is(err, storage.ErrObjectMissing) {
			return nil, invalidNewsImage("image: object was not uploaded")
		}
		return nil, err
	}
	defer f.Close()
	if size != img.Size {
		return nil, invalidNewsImage("image.size: does not match the uploaded object")
	}
	if err := a.objects.Verify(ctx, storage.Object{SHA256: img.SHA256, Size: img.Size}); err != nil {
		return nil, invalidNewsImage("image: integrity check failed")
	}
	width, height, kind, derr := decodeImageHeader(f)
	if derr != nil || kind != img.MediaType {
		return nil, invalidNewsImage("image: bytes are not a valid %s", img.MediaType)
	}
	if width < newsCoverMinWidth || width > newsCoverMaxWidth {
		return nil, invalidNewsImage("image: width must be %d to %d pixels", newsCoverMinWidth, newsCoverMaxWidth)
	}
	// 16:9 within 1 %: |9w - 16h| <= 0.01 * 16h
	diff := 9*width - 16*height
	if diff < 0 {
		diff = -diff
	}
	if diff*100 > 16*height {
		return nil, invalidNewsImage("image: aspect ratio must be 16:9 (1%% tolerance), got %dx%d", width, height)
	}
	return &storage.NewsObject{SHA256: img.SHA256, Size: img.Size}, nil
}

// validNewsBodyImage reports whether a cas: image of a body is a stored,
// intact JPEG or PNG. The error is for storage failures only.
func (a *API) validNewsBodyImage(ctx context.Context, sha string) (bool, error) {
	f, size, err := a.objects.Open(ctx, sha)
	if err != nil {
		if errors.Is(err, storage.ErrObjectMissing) || errors.Is(err, storage.ErrInvalidDigest) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	if size < 1 || size > newsBodyImageMax {
		return false, nil
	}
	if err := a.objects.Verify(ctx, storage.Object{SHA256: sha, Size: size}); err != nil {
		return false, nil
	}
	width, height, _, derr := decodeImageHeader(f)
	if derr != nil || width < 1 || height < 1 || width > newsBodyImageMaxDim || height > newsBodyImageMaxDim {
		return false, nil
	}
	return true, nil
}

// decodeImageHeader decodes only the header and returns the real media type.
func decodeImageHeader(r io.Reader) (width, height int, mediaType string, err error) {
	head, err := io.ReadAll(io.LimitReader(r, newsBodyImageMax+1))
	if err != nil {
		return 0, 0, "", err
	}
	if cfg, perr := png.DecodeConfig(bytes.NewReader(head)); perr == nil {
		return cfg.Width, cfg.Height, "image/png", nil
	}
	cfg, jerr := jpeg.DecodeConfig(bytes.NewReader(head))
	if jerr != nil {
		return 0, 0, "", jerr
	}
	return cfg.Width, cfg.Height, "image/jpeg", nil
}

// ---- categories ----

type newsCategoryInput struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

func (a *API) handleAdminCategories(w http.ResponseWriter, r *http.Request) {
	store, ok := a.needNewsStore(w)
	if !ok {
		return
	}
	a.writeCategories(w, r, store)
}

func (a *API) writeCategories(w http.ResponseWriter, r *http.Request, store newsStore) {
	categories, err := store.NewsCategories(r.Context())
	if err != nil {
		a.writeNewsError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion int                    `json:"schema_version"`
		Categories    []storage.NewsCategory `json:"categories"`
	}{SchemaVersion, categories})
}

func (a *API) handleAdminCategoriesReplace(w http.ResponseWriter, r *http.Request) {
	store, ok := a.needNewsStore(w)
	if !ok {
		return
	}
	raw, err := a.readJSONBody(w, r)
	if err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	var list []newsCategoryInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
		var wrapped struct {
			Categories []newsCategoryInput `json:"categories"`
		}
		err = dec.Decode(&wrapped)
		list = wrapped.Categories
	} else {
		err = dec.Decode(&list)
	}
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(list) > storage.MaxNewsCategories {
		a.writeNewsError(w, invalidNews("categories: at most %d", storage.MaxNewsCategories))
		return
	}
	existing, err := store.NewsCategories(r.Context())
	if err != nil {
		a.writeNewsError(w, err)
		return
	}
	known := map[string]bool{}
	for _, c := range existing {
		known[c.ID] = true
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	out := make([]storage.NewsCategory, 0, len(list))
	for _, in := range list {
		name, ferr := oneLine("categories.name", in.Name, newsCategoryMax)
		if ferr != nil {
			a.writeNewsError(w, ferr)
			return
		}
		key := strings.ToLower(name)
		if names[key] {
			a.writeNewsError(w, invalidNews("categories.name: %q is repeated", name))
			return
		}
		names[key] = true
		id := in.ID
		if id != "" {
			if !known[id] {
				a.writeNewsError(w, invalidNews("categories.id: %q does not exist", id))
				return
			}
			if ids[id] {
				a.writeNewsError(w, invalidNews("categories.id: %q is repeated", id))
				return
			}
		}
		if id == "" {
			for {
				if id, err = storage.NewNewsID("cat_", 8); err != nil {
					a.writeNewsError(w, err)
					return
				}
				if !known[id] && !ids[id] {
					break
				}
			}
		}
		ids[id] = true
		out = append(out, storage.NewsCategory{ID: id, Name: name, Order: in.Order})
	}
	if err := store.ReplaceNewsCategories(r.Context(), out); err != nil {
		a.writeNewsError(w, err)
		return
	}
	a.writeCategories(w, r, store)
}
