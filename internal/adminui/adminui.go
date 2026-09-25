package adminui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

// BuildInfo is intentionally limited to non-sensitive build identity.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// ProfileView is a presentation model, not a persistence or signed-manifest type.
type ProfileView struct {
	ID          string        `json:"id"`
	DisplayName string        `json:"display_name"`
	Official    bool          `json:"official"`
	Visibility  string        `json:"visibility,omitempty"`
	Channels    []ChannelView `json:"channels"`
}

// ChannelView describes the administrative status of one published channel.
type ChannelView struct {
	Name           string           `json:"name"`
	Head           *RevisionSummary `json:"head,omitempty"`
	RollbackStatus string           `json:"rollback_status"`
}

// RevisionSummary exposes immutable revision identity and pinned inheritance.
type RevisionSummary struct {
	ProfileID      string            `json:"profile_id"`
	ProfileName    string            `json:"profile_name"`
	ID             string            `json:"id"`
	Sequence       uint64            `json:"sequence"`
	ManifestSHA256 string            `json:"manifest_sha256"`
	PublishedAt    time.Time         `json:"published_at"`
	Base           *PinnedBaseView   `json:"base,omitempty"`
	Changes        RevisionDeltaView `json:"changes"`
}

// PinnedBaseView never represents "latest"; every inherited revision is exact.
type PinnedBaseView struct {
	ProfileID      string `json:"profile_id"`
	RevisionID     string `json:"revision_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// RevisionDeltaView summarizes effective administrative changes without bytes.
type RevisionDeltaView struct {
	AddedOrUpdatedMods    int `json:"added_or_updated_mods"`
	RemovedMods           int `json:"removed_mods"`
	AddedOrUpdatedConfigs int `json:"added_or_updated_configs"`
	RemovedConfigs        int `json:"removed_configs"`
	OtherObjects          int `json:"other_objects"`
}

// Overview is the only projection consumed by this development shell today.
type Overview struct {
	Profiles        []ProfileView     `json:"profiles"`
	RecentRevisions []RevisionSummary `json:"recent_revisions"`
	Storage         StorageView       `json:"storage"`
}

type StorageView struct {
	RevisionCount int64 `json:"revision_count"`
	ObjectCount   int64 `json:"object_count"`
	ObjectBytes   int64 `json:"object_bytes"`
}

// ReadModel defines the future domain boundary needed by the UI. Implementations
// must return only data already authorized for an administrator; this UI does
// not provide authentication or authorization itself.
type ReadModel interface {
	Overview(ctx context.Context) (Overview, error)
}

// EmptyReadModel deliberately returns no profile fixtures. Agents implementing
// signed revisions/storage can replace it with an adapter after those domains exist.
type EmptyReadModel struct{}

func (EmptyReadModel) Overview(context.Context) (Overview, error) {
	return Overview{Profiles: []ProfileView{}, RecentRevisions: []RevisionSummary{}}, nil
}

type revisionInventory interface {
	ListRevisions(context.Context, int) ([]storage.StoredRevision, error)
	Stats(context.Context) (storage.StorageStats, error)
}

// SQLiteReadModel projects only persisted, already-published immutable
// manifests. It does not imply that their release signatures were verified at
// read time; publication must remain behind the signed admin API boundary.
type SQLiteReadModel struct{ Store revisionInventory }

func (m SQLiteReadModel) Overview(ctx context.Context) (Overview, error) {
	if m.Store == nil {
		return Overview{}, errors.New("revision inventory is not configured")
	}
	revisions, err := m.Store.ListRevisions(ctx, 100)
	if err != nil {
		return Overview{}, err
	}
	stats, err := m.Store.Stats(ctx)
	if err != nil {
		return Overview{}, err
	}
	profiles := make(map[string]*ProfileView)
	view := Overview{
		Profiles:        []ProfileView{},
		RecentRevisions: make([]RevisionSummary, 0, len(revisions)),
		Storage: StorageView{
			RevisionCount: stats.RevisionCount,
			ObjectCount:   stats.ObjectCount,
			ObjectBytes:   stats.ObjectBytes,
		},
	}
	for _, stored := range revisions {
		var manifest revision.Manifest
		if err := json.Unmarshal(stored.Manifest, &manifest); err != nil {
			return Overview{}, err
		}
		profile := profiles[stored.ProfileID]
		if profile == nil {
			profile = &ProfileView{
				ID: stored.ProfileID, DisplayName: manifest.Profile.Name,
				Official: manifest.Profile.Official,
				Channels: []ChannelView{},
			}
			profiles[stored.ProfileID] = profile
		}
		summary := RevisionSummary{
			ProfileID: stored.ProfileID, ProfileName: manifest.Profile.Name,
			ID: stored.RevisionID, Sequence: uint64(stored.Sequence),
			ManifestSHA256: stored.ManifestSHA256, PublishedAt: stored.CreatedAt,
			Changes: RevisionDeltaView{
				AddedOrUpdatedMods: len(manifest.Mods), RemovedMods: len(manifest.RemoveMods),
				AddedOrUpdatedConfigs: len(manifest.Configs), RemovedConfigs: len(manifest.RemoveConfigs),
				OtherObjects: len(manifest.Objects),
			},
		}
		if manifest.Base != nil {
			summary.Base = &PinnedBaseView{ProfileID: manifest.Base.ProfileID,
				RevisionID: manifest.Base.RevisionID, ManifestSHA256: manifest.Base.ManifestSHA256}
		}
		view.RecentRevisions = append(view.RecentRevisions, summary)
	}
	for _, profile := range profiles {
		view.Profiles = append(view.Profiles, *profile)
	}
	sort.Slice(view.Profiles, func(i, j int) bool { return view.Profiles[i].ID < view.Profiles[j].ID })
	return view, nil
}

type overviewResponse struct {
	Build    BuildInfo     `json:"build"`
	Overview Overview      `json:"overview"`
	Mode     string        `json:"mode"`
	Actions  []interface{} `json:"actions"`
}

//go:embed static/*
var staticFiles embed.FS

// NewHandler returns a development-only, read-only administrative handler.
func NewHandler(model ReadModel, build BuildInfo) http.Handler {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	assets := http.FileServer(http.FS(static))
	indexHTML, err := fs.ReadFile(static, "index.html")
	if err != nil {
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setHeaders(w)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read-only development UI", http.StatusMethodNotAllowed)
			return
		}

		switch {
		case r.URL.Path == "/admin/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(indexHTML)
			}
		case r.URL.Path == "/admin/api/overview":
			overview, err := model.Overview(r.Context())
			if err != nil {
				http.Error(w, "administrative read model unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(overviewResponse{
				Build:    build,
				Overview: overview,
				Mode:     "development-read-only",
				Actions:  []interface{}{},
			})
		case strings.HasPrefix(r.URL.Path, "/admin/assets/"):
			serveStaticFile(w, r, assets, strings.TrimPrefix(r.URL.Path, "/admin/assets"))
		default:
			http.NotFound(w, r)
		}
	})
}

func serveStaticFile(w http.ResponseWriter, r *http.Request, assets http.Handler, path string) {
	cloned := r.Clone(r.Context())
	cloned.URL.Path = path
	assets.ServeHTTP(w, cloned)
}

func setHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}
