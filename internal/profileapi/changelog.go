package profileapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

const (
	defaultChangelogLimit = 50
	maxChangelogLimit     = 200
	maxOriginDepth        = 64
)

// clRev is one verified revision held for the duration of a request.
type clRev struct {
	id, profile string
	seq         int64
	created     string
	m           revision.Manifest
	prev        *clRev // previous revision of the same profile
	ownKey      string
	ownDone     bool
}

type clMod struct{ id, file, sha string }

type clOrigin struct {
	profile, revision string
	seq               int64
}

// changelogBuilder verifies each stored revision at most once per request and
// memoizes everything derived from the immutable manifests.
type changelogBuilder struct {
	a         *API
	ctx       context.Context
	store     changelogStore
	byID      map[string]*clRev
	byProfile map[string][]*clRev // ascending sequence
	loaded    map[string]bool
	origins   map[string]clOrigin
	effective map[string]map[string]clMod
}

func newChangelogBuilder(ctx context.Context, a *API, store changelogStore) *changelogBuilder {
	return &changelogBuilder{a: a, ctx: ctx, store: store, byID: map[string]*clRev{}, byProfile: map[string][]*clRev{},
		loaded: map[string]bool{}, origins: map[string]clOrigin{}, effective: map[string]map[string]clMod{}}
}

var errCorruptRevision = errors.New("stored revision failed integrity verification")

func (b *changelogBuilder) loadProfile(profileID string) ([]*clRev, error) {
	if b.loaded[profileID] {
		return b.byProfile[profileID], nil
	}
	stored, err := b.store.ProfileRevisionsAscending(b.ctx, profileID)
	if err != nil {
		return nil, err
	}
	list := make([]*clRev, 0, len(stored))
	for _, item := range stored {
		verified, _, err := b.a.verifyStoredRevision(b.ctx, item)
		if err != nil {
			return nil, errCorruptRevision
		}
		manifest := verified.Manifest()
		rev := &clRev{id: item.RevisionID, profile: item.ProfileID, seq: item.Sequence,
			created: manifest.Revision.CreatedAt, m: manifest}
		if len(list) > 0 {
			rev.prev = list[len(list)-1]
		}
		list = append(list, rev)
		b.byID[rev.id] = rev
	}
	b.loaded[profileID] = true
	b.byProfile[profileID] = list
	return list, nil
}

// resolve returns the revision a pin points at, or nil if it cannot be found.
func (b *changelogBuilder) resolve(pin *revision.ParentRef) (*clRev, error) {
	if pin == nil {
		return nil, nil
	}
	if _, err := b.loadProfile(pin.ProfileID); err != nil {
		return nil, err
	}
	return b.byID[pin.RevisionID], nil
}

// ownContent is the part of a manifest the author wrote: everything except the
// revision stamp, the base pin, the game pin and the profile identity.
func (r *clRev) ownContent() string {
	if r.ownDone {
		return r.ownKey
	}
	m := r.m
	own := struct {
		Permissions      revision.Permissions     `json:"permissions"`
		Mods             []revision.ModEntry      `json:"mods"`
		RemoveMods       []revision.RemoveMod     `json:"remove_mods"`
		Configs          []revision.ConfigEntry   `json:"configs"`
		RemoveConfigs    []revision.RemoveConfig  `json:"remove_configs"`
		ConfigSettings   []revision.ConfigSetting `json:"config_settings"`
		JVMOptions       []revision.JVMOption     `json:"jvm_options"`
		RemoveJVMOptions []string                 `json:"remove_jvm_options"`
		Objects          []revision.ObjectEntry   `json:"objects"`
	}{m.Permissions, m.Mods, m.RemoveMods, m.Configs, m.RemoveConfigs, m.ConfigSettings, m.JVMOptions, m.RemoveJVMOptions, m.Objects}
	if own.Mods == nil {
		own.Mods = []revision.ModEntry{}
	}
	if own.RemoveMods == nil {
		own.RemoveMods = []revision.RemoveMod{}
	}
	if own.Configs == nil {
		own.Configs = []revision.ConfigEntry{}
	}
	if own.RemoveConfigs == nil {
		own.RemoveConfigs = []revision.RemoveConfig{}
	}
	if own.ConfigSettings == nil {
		own.ConfigSettings = []revision.ConfigSetting{}
	}
	if own.JVMOptions == nil {
		own.JVMOptions = []revision.JVMOption{}
	}
	if own.RemoveJVMOptions == nil {
		own.RemoveJVMOptions = []string{}
	}
	if own.Objects == nil {
		own.Objects = []revision.ObjectEntry{}
	}
	raw, _ := json.Marshal(own)
	r.ownKey, r.ownDone = string(raw), true
	return r.ownKey
}

// originOf follows auto-follow revisions (same own content as the previous
// revision, different base) to the parent revision that introduced the change.
func (b *changelogBuilder) originOf(r *clRev, depth int) (clOrigin, error) {
	if o, ok := b.origins[r.id]; ok {
		return o, nil
	}
	origin := clOrigin{r.profile, r.id, r.seq}
	if r.prev != nil && r.m.Base != nil && r.prev.m.Base != nil && depth < maxOriginDepth &&
		r.m.Base.RevisionID != r.prev.m.Base.RevisionID && r.ownContent() == r.prev.ownContent() {
		parent, err := b.resolve(r.m.Base)
		if err != nil {
			return origin, err
		}
		if parent != nil {
			if origin, err = b.originOf(parent, depth+1); err != nil {
				return origin, err
			}
		}
	}
	b.origins[r.id] = origin
	return origin, nil
}

// effectiveMods resolves the inherited chain: per layer removals by id first,
// then additions by path (the same order the panel and launchers apply).
func (b *changelogBuilder) effectiveMods(r *clRev, depth int) (map[string]clMod, error) {
	if cached, ok := b.effective[r.id]; ok {
		return cached, nil
	}
	files := map[string]clMod{}
	if r.m.Base != nil && depth <= revision.MaxInheritanceBases {
		parent, err := b.resolve(r.m.Base)
		if err != nil {
			return nil, err
		}
		if parent != nil {
			inherited, err := b.effectiveMods(parent, depth+1)
			if err != nil {
				return nil, err
			}
			for p, mod := range inherited {
				files[p] = mod
			}
		}
	}
	for _, removal := range r.m.RemoveMods {
		for p, mod := range files {
			if mod.id == removal.ID {
				delete(files, p)
				break
			}
		}
	}
	for _, entry := range r.m.Mods {
		files[entry.Path] = clMod{id: entry.ID, file: path.Base(entry.Path), sha: entry.Object.SHA256}
	}
	b.effective[r.id] = files
	return files, nil
}

type modRef struct {
	ID   string `json:"id"`
	File string `json:"file"`
}
type modUpdate struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}
type modsDiff struct {
	Added   []modRef    `json:"added"`
	Removed []modRef    `json:"removed"`
	Updated []modUpdate `json:"updated"`
}

// modsDiff compares the effective mods of r with the previous revision of its
// profile; the first revision of a profile is compared with its branch point.
func (b *changelogBuilder) modsDiff(r *clRev) (modsDiff, error) {
	out := modsDiff{Added: []modRef{}, Removed: []modRef{}, Updated: []modUpdate{}}
	after, err := b.effectiveMods(r, 0)
	if err != nil {
		return out, err
	}
	before := map[string]clMod{}
	switch {
	case r.prev != nil:
		before, err = b.effectiveMods(r.prev, 0)
	case r.m.Base != nil:
		var base *clRev
		if base, err = b.resolve(r.m.Base); err == nil && base != nil {
			before, err = b.effectiveMods(base, 0)
		}
	}
	if err != nil {
		return out, err
	}
	byID := func(files map[string]clMod) map[string]clMod {
		m := make(map[string]clMod, len(files))
		for _, mod := range files {
			m[mod.id] = mod
		}
		return m
	}
	old, now := byID(before), byID(after)
	for id, mod := range now {
		previous, ok := old[id]
		switch {
		case !ok:
			out.Added = append(out.Added, modRef{id, mod.file})
		case previous.sha != mod.sha || previous.file != mod.file:
			out.Updated = append(out.Updated, modUpdate{id, previous.file, mod.file})
		}
	}
	for id, mod := range old {
		if _, ok := now[id]; !ok {
			out.Removed = append(out.Removed, modRef{id, mod.file})
		}
	}
	sort.Slice(out.Added, func(i, j int) bool { return out.Added[i].ID < out.Added[j].ID })
	sort.Slice(out.Removed, func(i, j int) bool { return out.Removed[i].ID < out.Removed[j].ID })
	sort.Slice(out.Updated, func(i, j int) bool { return out.Updated[i].ID < out.Updated[j].ID })
	return out, nil
}

type clEntry struct {
	origin clOrigin
	via    *clRev // revision of the requested profile that brought the change; nil for earlier ancestors
}

// collect lists one entry per origin, newest first: the profile's own
// revisions, then the revisions its ancestors had when it was created.
func (b *changelogBuilder) collect(profileID string) ([]clEntry, error) {
	own, err := b.loadProfile(profileID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var entries []clEntry
	for _, r := range own { // oldest first: the earliest revision is the one that brought a change
		o, err := b.originOf(r, 0)
		if err != nil {
			return nil, err
		}
		if !seen[o.revision] {
			seen[o.revision] = true
			entries = append(entries, clEntry{origin: o, via: r})
		}
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if len(own) == 0 {
		return entries, nil
	}
	visited := map[string]bool{profileID: true}
	cur := own[0]
	for depth := 1; cur.m.Base != nil && depth <= revision.MaxInheritanceBases; depth++ {
		pin := cur.m.Base
		if visited[pin.ProfileID] {
			break
		}
		visited[pin.ProfileID] = true
		branch, err := b.resolve(pin)
		if err != nil {
			return nil, err
		}
		list := b.byProfile[pin.ProfileID]
		if branch == nil || len(list) == 0 {
			break
		}
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].seq > branch.seq {
				continue
			}
			o, err := b.originOf(list[i], 0)
			if err != nil {
				return nil, err
			}
			if !seen[o.revision] {
				seen[o.revision] = true
				entries = append(entries, clEntry{origin: o})
			}
		}
		cur = list[0]
	}
	return entries, nil
}

type changelogEntryView struct {
	Origin    originView    `json:"origin"`
	Revision  *revisionRef2 `json:"revision"`
	CreatedAt string        `json:"created_at"`
	Title     string        `json:"title"`
	Desc      string        `json:"description"`
	Changes   []string      `json:"changes"`
	Mods      modsDiff      `json:"mods"`
}
type originView struct {
	ProfileID  string `json:"profile_id"`
	RevisionID string `json:"revision_id"`
	Sequence   int64  `json:"sequence"`
}
type revisionRef2 struct {
	RevisionID string `json:"revision_id"`
	Sequence   int64  `json:"sequence"`
}
type lineageView struct {
	ProfileID string `json:"profile_id"`
	Name      string `json:"name"`
	Depth     int    `json:"depth"`
}

func (b *changelogBuilder) lineage(profileID string, entries []clEntry) []lineageView {
	var out []lineageView
	index := map[string]bool{}
	add := func(id string, depth int) {
		if index[id] {
			return
		}
		index[id] = true
		name := id
		if list := b.byProfile[id]; len(list) > 0 {
			name = list[len(list)-1].m.Profile.Name
		}
		if p, ok := b.a.store.(presentationStore); ok {
			if raw, err := p.ProfilePresentation(b.ctx, id); err == nil && len(raw) > 0 {
				var pres profilePresentation
				if json.Unmarshal(raw, &pres) == nil && pres.Name != "" {
					name = pres.Name
				}
			}
		}
		out = append(out, lineageView{id, name, depth})
	}
	add(profileID, 0)
	walk := func(start *clRev) {
		cur := start
		for depth := 1; cur != nil && cur.m.Base != nil && depth <= revision.MaxInheritanceBases; depth++ {
			parent, err := b.resolve(cur.m.Base)
			if err != nil || parent == nil {
				return
			}
			add(parent.profile, depth)
			cur = parent
		}
	}
	if own := b.byProfile[profileID]; len(own) > 0 {
		walk(own[len(own)-1])
		walk(own[0])
	}
	for _, e := range entries {
		add(e.origin.profile, len(out))
	}
	return out
}

func encodeChangelogCursor(originRevision string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("c1:" + originRevision))
}

func decodeChangelogCursor(cursor string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), "c1:") || len(raw) == 3 {
		return "", false
	}
	return string(raw[3:]), true
}

// handleChangelog serves GET /v1/profiles/{profile_id}/changelog.
func (a *API) handleChangelog(w http.ResponseWriter, r *http.Request, profileID string) {
	store, ok := a.store.(changelogStore)
	if !ok {
		a.writeError(w, http.StatusNotImplemented, "changelog_unavailable", "this store does not keep changelogs")
		return
	}
	query := r.URL.Query()
	limit := defaultChangelogLimit
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxChangelogLimit {
			a.writeError(w, http.StatusUnprocessableEntity, "invalid_limit", "limit must be between 1 and 200")
			return
		}
		limit = n
	}
	if _, err := a.store.ProfileHead(r.Context(), profileID); err != nil {
		if errors.Is(err, storage.ErrRevisionNotFound) {
			a.writeError(w, http.StatusNotFound, "profile_not_found", "profile not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "storage_error", "profile unavailable")
		return
	}
	b := newChangelogBuilder(r.Context(), a, store)
	fail := func(err error) {
		if errors.Is(err, errCorruptRevision) {
			a.writeError(w, http.StatusInternalServerError, "revision_corrupt", "stored revision failed integrity verification")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "storage_error", "changelog unavailable")
	}
	all, err := b.collect(profileID)
	if err != nil {
		fail(err)
		return
	}
	own := b.byProfile[profileID]
	notInProfile := func(what string) {
		a.writeError(w, http.StatusUnprocessableEntity, "revision_not_in_profile", what+" is not a revision of this profile")
	}

	selected := all
	if single := query.Get("revision"); single != "" {
		selected = nil
		for _, e := range all {
			if e.origin.revision == single || (e.via != nil && e.via.id == single) {
				selected = []clEntry{e}
				break
			}
		}
		if selected == nil {
			if rev := b.byID[single]; rev != nil && rev.profile == profileID {
				o, err := b.originOf(rev, 0)
				if err != nil {
					fail(err)
					return
				}
				for _, e := range all {
					if e.origin.revision == o.revision {
						selected = []clEntry{e}
					}
				}
			}
		}
		if selected == nil {
			notInProfile("revision")
			return
		}
	} else if query.Get("after") != "" || query.Get("until") != "" {
		var after int64
		until := own[len(own)-1].seq
		if id := query.Get("after"); id != "" {
			rev := b.byID[id]
			if rev == nil || rev.profile != profileID {
				notInProfile("after")
				return
			}
			after = rev.seq
		}
		if id := query.Get("until"); id != "" {
			rev := b.byID[id]
			if rev == nil || rev.profile != profileID {
				notInProfile("until")
				return
			}
			until = rev.seq
		}
		selected = nil
		for _, e := range all {
			if e.via != nil && e.via.seq > after && e.via.seq <= until {
				selected = append(selected, e)
			}
		}
	}

	start := 0
	if cursor := query.Get("cursor"); cursor != "" {
		id, valid := decodeChangelogCursor(cursor)
		start = -1
		if valid {
			for i, e := range selected {
				if e.origin.revision == id {
					start = i + 1
					break
				}
			}
		}
		if start < 0 {
			a.writeError(w, http.StatusUnprocessableEntity, "invalid_cursor", "cursor is not valid for this listing")
			return
		}
	}
	end := start + limit
	var nextCursor interface{}
	if end < len(selected) {
		nextCursor = encodeChangelogCursor(selected[end-1].origin.revision)
	} else {
		end = len(selected)
	}
	page := selected[start:end]

	ids := make([]string, 0, len(page))
	for _, e := range page {
		ids = append(ids, e.origin.revision)
	}
	texts, err := store.RevisionChangelogs(r.Context(), ids)
	if err != nil {
		fail(err)
		return
	}
	views := make([]changelogEntryView, 0, len(page))
	for _, e := range page {
		originRev := b.byID[e.origin.revision]
		if originRev == nil {
			fail(errCorruptRevision)
			return
		}
		var text changelogText
		if row, ok := texts[originRev.id]; ok {
			text = changelogText{row.Title, row.Description, row.Changes}
		} else {
			text = deriveChangelogText(originRev.m.Revision.ReleaseNotes)
		}
		diffTarget := originRev
		if e.via != nil {
			diffTarget = e.via
		}
		diff, err := b.modsDiff(diffTarget)
		if err != nil {
			fail(err)
			return
		}
		view := changelogEntryView{
			Origin:    originView{e.origin.profile, e.origin.revision, e.origin.seq},
			CreatedAt: originRev.created, Title: text.Title, Desc: text.Description, Changes: text.Changes, Mods: diff,
		}
		if e.via != nil {
			view.Revision = &revisionRef2{e.via.id, e.via.seq}
		}
		views = append(views, view)
	}
	body, err := json.Marshal(struct {
		SchemaVersion int                  `json:"schema_version"`
		ProfileID     string               `json:"profile_id"`
		Lineage       []lineageView        `json:"lineage"`
		Entries       []changelogEntryView `json:"entries"`
		NextCursor    interface{}          `json:"next_cursor"`
	}{SchemaVersion, profileID, b.lineage(profileID, all), views, nextCursor})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "internal_error", "changelog could not be encoded")
		return
	}
	etag := `"cl-` + sha256Hex(body)[:32] + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-BootOptim-Protocol-Version", strconv.Itoa(ProtocolVersion))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}
