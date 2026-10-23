package profileapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wachipayox/BootOptimDistribution/internal/launcherartifact"
	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/semver"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

const (
	launcherNotesMaxBytes    = 16384
	launcherFileNameMaxBytes = 128
	launcherMaxArtifacts     = 16
	launcherTimeFormat       = "2006-01-02T15:04:05Z"
)

type launcherStore interface {
	CreateLauncherRelease(context.Context, storage.LauncherRelease) error
	LauncherReleases(context.Context, string) ([]storage.LauncherRelease, error)
	LauncherRelease(context.Context, string, string) (storage.LauncherRelease, error)
	UpdateLauncherRelease(context.Context, string, string, *string, *string) (storage.LauncherRelease, error)
	WithdrawLauncherRelease(context.Context, string, string) (storage.LauncherRelease, error)
}

// bytesSigner is implemented by the server identity (releasesigning.Signer).
type bytesSigner interface {
	SignBytes([]byte) revision.Signature
}

func launcherAppValid(app string) bool { return app == "pandora" || app == "android" }

func launcherChannelValid(channel string) bool { return channel == "player" || channel == "developer" }

func launcherPlatformValid(platform string) bool {
	if len(platform) < 1 || len(platform) > 32 {
		return false
	}
	for i := 0; i < len(platform); i++ {
		c := platform[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func launcherFileNameValid(name string) bool {
	if len(name) < 1 || len(name) > launcherFileNameMaxBytes || !utf8.ValidString(name) || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

// launcherNotesValid accepts plain UTF-8 text; only tab and line breaks are
// allowed among the control characters.
func launcherNotesValid(notes string) bool {
	if len(notes) > launcherNotesMaxBytes || !utf8.ValidString(notes) {
		return false
	}
	for _, r := range notes {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

type launcherArtifactView struct {
	Platform  string             `json:"platform"`
	FileName  string             `json:"file_name"`
	URL       string             `json:"url"`
	SHA256    string             `json:"sha256"`
	Size      int64              `json:"size"`
	Statement string             `json:"statement"`
	Signature revision.Signature `json:"signature"`
}

func newLauncherArtifactView(a storage.LauncherArtifact) launcherArtifactView {
	return launcherArtifactView{
		Platform: a.Platform, FileName: a.FileName, URL: "/v1/objects/sha256/" + a.SHA256,
		SHA256: a.SHA256, Size: a.Size, Statement: a.Statement,
		Signature: revision.Signature{KeyID: a.KeyID, Algorithm: a.Algorithm, Value: a.Signature},
	}
}

type launcherReleaseView struct {
	Version     string                `json:"version"`
	Channel     string                `json:"channel"`
	PublishedAt string                `json:"published_at"`
	Notes       string                `json:"notes"`
	Artifact    *launcherArtifactView `json:"artifact"`
}

type launcherAdminView struct {
	App         string                 `json:"app"`
	Version     string                 `json:"version"`
	Channel     string                 `json:"channel"`
	Notes       string                 `json:"notes"`
	PublishedAt string                 `json:"published_at"`
	WithdrawnAt *string                `json:"withdrawn_at"`
	Artifacts   []launcherArtifactView `json:"artifacts"`
}

func newLauncherAdminView(r storage.LauncherRelease) launcherAdminView {
	view := launcherAdminView{
		App: r.App, Version: r.Version, Channel: r.Channel, Notes: r.Notes,
		PublishedAt: r.PublishedAt.UTC().Format(launcherTimeFormat), Artifacts: []launcherArtifactView{},
	}
	if r.WithdrawnAt != nil {
		w := r.WithdrawnAt.UTC().Format(launcherTimeFormat)
		view.WithdrawnAt = &w
	}
	for _, a := range r.Artifacts {
		view.Artifacts = append(view.Artifacts, newLauncherArtifactView(a))
	}
	return view
}

// serveLauncherRead handles GET /v1/launcher/{app}/releases.
func (a *API) serveLauncherRead(w http.ResponseWriter, r *http.Request) bool {
	parts := pathParts(r.URL.Path)
	if len(parts) < 2 || parts[0] != "v1" || parts[1] != "launcher" {
		return false
	}
	if len(parts) != 4 || parts[3] != "releases" {
		a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		a.methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return true
	}
	store, ok := a.store.(launcherStore)
	if !ok {
		a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return true
	}
	app := parts[2]
	if !launcherAppValid(app) {
		a.writeError(w, http.StatusNotFound, "app_not_found", "unknown launcher app")
		return true
	}
	query := r.URL.Query()
	platform := query.Get("platform")
	if platform == "" {
		a.writeError(w, http.StatusBadRequest, "platform_required", "the platform query parameter is required")
		return true
	}
	if !launcherPlatformValid(platform) {
		a.writeError(w, http.StatusBadRequest, "invalid_platform", "platform must match [a-z0-9_-]{1,32}")
		return true
	}
	channel := query.Get("channel")
	if channel == "" {
		channel = "player"
	}
	if !launcherChannelValid(channel) {
		a.writeError(w, http.StatusBadRequest, "invalid_channel", "channel must be player or developer")
		return true
	}
	var after *semver.Version
	if raw := query.Get("after"); raw != "" {
		v, err := semver.Parse(raw)
		if err != nil {
			a.writeError(w, http.StatusUnprocessableEntity, "invalid_after", "after must be a SemVer 2.0 version")
			return true
		}
		after = &v
	}
	all, err := store.LauncherReleases(r.Context(), app)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "launcher releases unavailable")
		return true
	}
	type candidate struct {
		version semver.Version
		view    launcherReleaseView
	}
	var found []candidate
	for _, release := range all {
		if release.WithdrawnAt != nil || (release.Channel != "player" && channel != "developer") {
			continue
		}
		version, err := semver.Parse(release.Version)
		if err != nil || (after != nil && semver.Compare(version, *after) <= 0) {
			continue
		}
		for _, artifact := range release.Artifacts {
			if artifact.Platform != platform {
				continue
			}
			view := newLauncherArtifactView(artifact)
			found = append(found, candidate{version, launcherReleaseView{
				Version: release.Version, Channel: release.Channel, Notes: release.Notes,
				PublishedAt: release.PublishedAt.UTC().Format(launcherTimeFormat), Artifact: &view,
			}})
			break
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return semver.Compare(found[i].version, found[j].version) > 0 })
	releases := make([]launcherReleaseView, 0, len(found))
	for _, item := range found {
		releases = append(releases, item.view)
	}
	var latest *launcherReleaseView
	if len(releases) > 0 {
		latest = &releases[0]
	}
	a.writeETagJSON(w, r, struct {
		SchemaVersion int                   `json:"schema_version"`
		App           string                `json:"app"`
		Latest        *launcherReleaseView  `json:"latest"`
		Releases      []launcherReleaseView `json:"releases"`
	}{SchemaVersion, app, latest, releases})
	return true
}

// serveLauncherAdmin handles /v1/admin/launcher-releases[/{app}/{version}].
func (a *API) serveLauncherAdmin(w http.ResponseWriter, r *http.Request) bool {
	parts := pathParts(r.URL.Path)
	if len(parts) < 3 || parts[2] != "launcher-releases" {
		return false
	}
	store, ok := a.store.(launcherStore)
	if !ok {
		a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return true
	}
	switch len(parts) {
	case 3:
		switch r.Method {
		case http.MethodGet:
			a.handleLauncherAdminList(w, r, store)
		case http.MethodPost:
			a.handleLauncherCreate(w, r, store)
		default:
			a.methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	case 5:
		app, version := parts[3], parts[4]
		switch r.Method {
		case http.MethodPut:
			a.handleLauncherUpdate(w, r, store, app, version)
		case http.MethodDelete:
			release, err := store.WithdrawLauncherRelease(r.Context(), app, version)
			a.writeLauncherResult(w, http.StatusOK, release, err)
		default:
			a.methodNotAllowed(w, http.MethodPut, http.MethodDelete)
		}
	default:
		a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
	}
	return true
}

func (a *API) handleLauncherAdminList(w http.ResponseWriter, r *http.Request, store launcherStore) {
	app := r.URL.Query().Get("app")
	if app != "" && !launcherAppValid(app) {
		a.writeError(w, http.StatusNotFound, "app_not_found", "unknown launcher app")
		return
	}
	all, err := store.LauncherReleases(r.Context(), app)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "launcher releases unavailable")
		return
	}
	views := make([]launcherAdminView, 0, len(all))
	for _, release := range all {
		views = append(views, newLauncherAdminView(release))
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion int                 `json:"schema_version"`
		Releases      []launcherAdminView `json:"releases"`
	}{SchemaVersion, views})
}

type launcherCreateRequest struct {
	App       string `json:"app"`
	Version   string `json:"version"`
	Channel   string `json:"channel"`
	Notes     string `json:"notes"`
	Artifacts []struct {
		Platform string `json:"platform"`
		FileName string `json:"file_name"`
		Object   struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"object"`
	} `json:"artifacts"`
}

func (a *API) handleLauncherCreate(w http.ResponseWriter, r *http.Request, store launcherStore) {
	signer, ok := a.signer.(bytesSigner)
	if !ok {
		a.writeError(w, http.StatusServiceUnavailable, "signing_unavailable", "the server publication identity is unavailable")
		return
	}
	var req launcherCreateRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	invalid := func(message string) { a.writeError(w, http.StatusUnprocessableEntity, "invalid_release", message) }
	if !launcherAppValid(req.App) {
		invalid("app must be pandora or android")
		return
	}
	if _, err := semver.Parse(req.Version); err != nil {
		invalid("version must be SemVer 2.0 (MAJOR.MINOR.PATCH[-pre][+build])")
		return
	}
	if req.Channel == "" {
		req.Channel = "player"
	}
	if !launcherChannelValid(req.Channel) {
		invalid("channel must be player or developer")
		return
	}
	if !launcherNotesValid(req.Notes) {
		invalid("notes must be plain UTF-8 text of at most 16384 bytes")
		return
	}
	if len(req.Artifacts) == 0 || len(req.Artifacts) > launcherMaxArtifacts {
		invalid("a release needs between 1 and 16 artifacts")
		return
	}
	issued := time.Now().UTC().Truncate(time.Second)
	seen := make(map[string]bool)
	release := storage.LauncherRelease{
		App: req.App, Version: req.Version, Channel: req.Channel, Notes: req.Notes, PublishedAt: issued,
	}
	for _, item := range req.Artifacts {
		switch {
		case !launcherPlatformValid(item.Platform):
			invalid("platform must match [a-z0-9_-]{1,32}")
			return
		case seen[item.Platform]:
			invalid("only one artifact per platform is allowed")
			return
		case !launcherFileNameValid(item.FileName):
			invalid("file_name must be 1-128 bytes without slashes or control characters")
			return
		case !validDigest(item.Object.SHA256) || item.Object.Size < 0:
			invalid("object needs a lowercase sha256 and a non-negative size")
			return
		}
		seen[item.Platform] = true
		if err := a.objects.Verify(r.Context(), storage.Object{SHA256: item.Object.SHA256, Size: item.Object.Size}); err != nil {
			a.writeLauncherObjectError(w, err)
			return
		}
		statement := launcherartifact.Statement{
			Type: launcherartifact.Type, App: req.App, Version: req.Version, Platform: item.Platform,
			SHA256: item.Object.SHA256, Size: item.Object.Size, IssuedAt: issued.Format(launcherTimeFormat),
		}
		canonical, encoded, err := statement.Encode()
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "signing_error", "statement could not be canonicalized")
			return
		}
		signature := signer.SignBytes(canonical)
		release.Artifacts = append(release.Artifacts, storage.LauncherArtifact{
			Platform: item.Platform, FileName: item.FileName, SHA256: item.Object.SHA256, Size: item.Object.Size,
			Statement: encoded, KeyID: signature.KeyID, Algorithm: signature.Algorithm, Signature: signature.Value,
		})
	}
	err := store.CreateLauncherRelease(r.Context(), release)
	if err == nil {
		stored, err := store.LauncherRelease(r.Context(), req.App, req.Version)
		a.writeLauncherResult(w, http.StatusCreated, stored, err)
		return
	}
	if errors.Is(err, storage.ErrLauncherReleaseExists) {
		a.writeError(w, http.StatusConflict, "release_exists", "this version was already published for this app and can never be reused")
		return
	}
	if errors.Is(err, storage.ErrObjectMissing) || errors.Is(err, storage.ErrSizeMismatch) ||
		errors.Is(err, storage.ErrDigestMismatch) || errors.Is(err, storage.ErrCorruptObject) {
		a.writeLauncherObjectError(w, err)
		return
	}
	a.writeError(w, http.StatusInternalServerError, "storage_error", "launcher release could not be stored")
}

func (a *API) writeLauncherObjectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrObjectMissing):
		a.writeError(w, http.StatusUnprocessableEntity, "object_missing", "the binary must be uploaded before publishing the release")
	case errors.Is(err, storage.ErrSizeMismatch):
		a.writeError(w, http.StatusUnprocessableEntity, "object_size_mismatch", "the uploaded binary does not have the declared size")
	case errors.Is(err, storage.ErrDigestMismatch), errors.Is(err, storage.ErrCorruptObject):
		a.writeError(w, http.StatusUnprocessableEntity, "object_hash_mismatch", "the stored binary does not match the declared sha256")
	case errors.Is(err, storage.ErrInvalidDigest):
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_release", "object digest is invalid")
	default:
		a.writeError(w, http.StatusInternalServerError, "storage_error", "binary could not be verified")
	}
}

func (a *API) handleLauncherUpdate(w http.ResponseWriter, r *http.Request, store launcherStore, app, version string) {
	var req struct {
		Channel *string `json:"channel"`
		Notes   *string `json:"notes"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	if req.Channel == nil && req.Notes == nil {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_release", "channel or notes is required")
		return
	}
	if req.Channel != nil && !launcherChannelValid(*req.Channel) {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_release", "channel must be player or developer")
		return
	}
	if req.Notes != nil && !launcherNotesValid(*req.Notes) {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_release", "notes must be plain UTF-8 text of at most 16384 bytes")
		return
	}
	release, err := store.UpdateLauncherRelease(r.Context(), app, version, req.Channel, req.Notes)
	a.writeLauncherResult(w, http.StatusOK, release, err)
}

func (a *API) writeLauncherResult(w http.ResponseWriter, status int, release storage.LauncherRelease, err error) {
	if errors.Is(err, storage.ErrLauncherReleaseNotFound) {
		a.writeError(w, http.StatusNotFound, "release_not_found", "launcher release not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "launcher release unavailable")
		return
	}
	a.writeJSON(w, status, struct {
		SchemaVersion int               `json:"schema_version"`
		Release       launcherAdminView `json:"release"`
	}{SchemaVersion, newLauncherAdminView(release)})
}

// writeETagJSON serves a JSON document with a content ETag and 304 support.
func (a *API) writeETagJSON(w http.ResponseWriter, r *http.Request, body interface{}) {
	raw, err := json.Marshal(body)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "encode_error", "response could not be encoded")
		return
	}
	raw = append(raw, 0x0a)
	digest := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-BootOptim-Protocol-Version", "1")
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(raw)
	}
}
