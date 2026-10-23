package profileapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
	"github.com/wachipayox/BootOptimDistribution/internal/testauth"
)

// Test profiles are agents' sandboxes: children (or deeper descendants) of any
// profile whose id starts with storage.TestProfilePrefix and that have a row in
// test_profiles. The /v1/test/ API writes only those; everything it publishes
// is a normal immutable revision signed by the server and validated by
// publishCore. Nothing it does can modify a normal profile.

const (
	defaultTestMaxProfiles    = 200
	defaultTestMaxBytes       = 100 << 30
	defaultTestDiskMargin     = 10 << 30
	testQuotaWarnPercent      = 80
	maxTestOperations         = 256
	testDefaultConfigPolicy   = "default_once"
	testStableChannel         = "stable"
	testProfileSlugMaxLength  = 59 // profile id tail is at most 64 bytes, "test-" uses 5
	testProfileNameMaxLength  = 96
	testReleaseNotesMaxLength = 8192
)

// TestTokens is the admin-side view of the shared test token.
type TestTokens interface {
	Rotate(context.Context) (string, time.Time, error)
	Status() testauth.Status
}

// TestQuotas bound the test API globally.
type TestQuotas struct {
	MaxProfiles     int
	MaxObjectBytes  int64
	MaxBytes        int64
	DiskMarginBytes int64
}

func (q TestQuotas) withDefaults(casMax int64) TestQuotas {
	if casMax <= 0 {
		casMax = defaultMaxObjectBytes
	}
	if q.MaxProfiles <= 0 {
		q.MaxProfiles = defaultTestMaxProfiles
	}
	if q.MaxObjectBytes <= 0 || q.MaxObjectBytes > casMax {
		q.MaxObjectBytes = casMax
	}
	if q.MaxBytes <= 0 {
		q.MaxBytes = defaultTestMaxBytes
	}
	if q.DiskMarginBytes <= 0 {
		q.DiskMarginBytes = defaultTestDiskMargin
	}
	return q
}

type testProfileStore interface {
	RegisterTestProfile(context.Context, string, string, bool) error
	ReleaseUnpublishedTestProfile(context.Context, string) error
	TestProfile(context.Context, string) (storage.TestProfileRecord, error)
	ListTestProfiles(context.Context) ([]storage.TestProfileRecord, error)
	SetTestProfileFollow(context.Context, string, bool) error
	TestProfileFollows(context.Context, string) (bool, error)
	RecordTestObject(context.Context, storage.Object, string) error
	TestUsage(context.Context) (storage.TestUsage, error)
	TestProfileOwnBytes(context.Context, string) (int64, error)
}

type profileSummaryStore interface {
	ProfileSummaries(context.Context) ([]storage.ProfileSummary, error)
}

type objectPresence interface {
	Has(context.Context, string) (int64, bool, error)
}

type rollbackSigner interface {
	SignRollback(string, revision.RollbackStatement) ([]byte, error)
}

func isTestProfileID(id string) bool { return strings.HasPrefix(id, storage.TestProfilePrefix) }

// latestRevisions returns the latest revision of every live profile. Stores
// that can summarize do so without a row limit; others keep the old listing.
func (a *API) latestRevisions(ctx context.Context) ([]storage.StoredSignedRevision, error) {
	if store, ok := a.store.(profileSummaryStore); ok {
		summaries, err := store.ProfileSummaries(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]storage.StoredSignedRevision, 0, len(summaries))
		for _, summary := range summaries {
			out = append(out, summary.Latest)
		}
		return out, nil
	}
	return a.store.ListSignedRevisions(ctx, 1000)
}

// followsParent is false only for a test profile pinned with follow_parent=false.
func (a *API) followsParent(ctx context.Context, profileID string) bool {
	if !isTestProfileID(profileID) {
		return true
	}
	store, ok := a.store.(testProfileStore)
	if !ok {
		return true
	}
	follows, err := store.TestProfileFollows(ctx, profileID)
	if err != nil {
		log.Printf("auto-follow: WARN follow state of %s unavailable: %v", profileID, err)
		return false
	}
	return follows
}

func (a *API) handleProfileSummaries(w http.ResponseWriter, r *http.Request, admin, includeTest bool) {
	summaries, err := a.store.(profileSummaryStore).ProfileSummaries(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "profile inventory unavailable")
		return
	}
	channels, err := a.store.ListChannels(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "channel inventory unavailable")
		return
	}
	channelByProfile := make(map[string][]channelView)
	for _, ch := range channels {
		channelByProfile[ch.ProfileID] = append(channelByProfile[ch.ProfileID], channelView{
			Name:        ch.Channel,
			revisionRef: revisionRef{RevisionID: ch.RevisionID, Sequence: ch.Sequence, ManifestSHA256: ch.ManifestSHA256},
		})
	}
	views := make([]profileView, 0)
	truncated := false
	for _, summary := range summaries {
		item := summary.Latest
		if isTestProfileID(item.ProfileID) && !includeTest {
			continue
		}
		if len(views) == a.listLimit {
			truncated = true
			break
		}
		verified, _, err := a.verifyStoredRevision(r.Context(), item)
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "revision_corrupt", "stored revision failed integrity verification")
			return
		}
		manifest := verified.Manifest()
		profileChannels := channelByProfile[item.ProfileID]
		if profileChannels == nil {
			profileChannels = []channelView{}
		}
		view := profileView{
			ProfileID: item.ProfileID, Name: manifest.Profile.Name, Description: manifest.Profile.Description,
			Minecraft: manifest.Game.Minecraft, NeoForge: manifest.Game.NeoForge, Loader: manifest.Game.ResolvedLoader(), Icon: manifest.Profile.Icon,
			LatestRevision: revisionRef{RevisionID: item.RevisionID, Sequence: item.Sequence, ManifestSHA256: item.ManifestSHA256},
			Channels:       profileChannels,
		}
		if admin {
			view.PublishedCount = int(summary.RevisionCount)
		}
		views = append(views, view)
	}
	if err := a.applyPresentations(r.Context(), views, admin); err != nil {
		a.writeError(w, 500, "storage_error", "profile presentation unavailable")
		return
	}
	a.writeCatalog(w, r, admin, struct {
		SchemaVersion   int           `json:"schema_version"`
		ProtocolVersion int           `json:"protocol_version"`
		Profiles        []profileView `json:"profiles"`
		Truncated       bool          `json:"truncated"`
	}{SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion, Profiles: views, Truncated: truncated})
}

// ---------------------------------------------------------------------------
// Admin side: token, quotas, list. The admin API cannot write test profiles
// (only delete them) and cannot derive a normal profile from a test profile.

func (a *API) serveAdminTest(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/v1/admin/test-api":
		if r.Method != http.MethodGet {
			a.methodNotAllowed(w, http.MethodGet)
			return true
		}
		usage, ok := a.testUsageView(w, r.Context())
		if !ok {
			return true
		}
		profiles, ok := a.testProfileViews(w, r.Context(), true)
		if !ok {
			return true
		}
		status := testauth.Status{}
		if a.testTokens != nil {
			status = a.testTokens.Status()
		}
		a.writeJSON(w, http.StatusOK, struct {
			SchemaVersion int               `json:"schema_version"`
			Token         testauth.Status   `json:"token"`
			Usage         testUsageView     `json:"usage"`
			Profiles      []testProfileView `json:"profiles"`
		}{SchemaVersion, status, usage, profiles})
		return true
	case "/v1/admin/test-api/token":
		if r.Method != http.MethodPost {
			a.methodNotAllowed(w, http.MethodPost)
			return true
		}
		if a.testTokens == nil {
			a.writeError(w, http.StatusServiceUnavailable, "test_api_unavailable", "test API is not enabled")
			return true
		}
		token, created, err := a.testTokens.Rotate(r.Context())
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "storage_error", "test API token could not be generated")
			return true
		}
		log.Printf("test API: shared token generated or rotated by the administrator; previous token revoked")
		a.writeJSON(w, http.StatusOK, struct {
			SchemaVersion int       `json:"schema_version"`
			Token         string    `json:"token"`
			CreatedAt     time.Time `json:"created_at"`
		}{SchemaVersion, token, created})
		return true
	}
	return false
}

func (a *API) rejectAdminTestProfile(w http.ResponseWriter, profileID string) bool {
	if !isTestProfileID(profileID) {
		return false
	}
	a.writeError(w, http.StatusForbidden, "test_profile_admin_write", "test profiles are written only through /v1/test/; the admin can delete them")
	return true
}

// rejectAdminTestPublication blocks admin publications of test ids and of
// normal profiles inheriting from a test profile.
func (a *API) rejectAdminTestPublication(w http.ResponseWriter, raw []byte) bool {
	var peek struct {
		Manifest struct {
			Profile struct {
				ID string `json:"id"`
			} `json:"profile"`
			Base *struct {
				ProfileID string `json:"profile_id"`
			} `json:"base"`
		} `json:"manifest"`
	}
	if json.Unmarshal(raw, &peek) != nil {
		return false // the normal validation reports the malformed body
	}
	if a.rejectAdminTestProfile(w, peek.Manifest.Profile.ID) {
		return true
	}
	if peek.Manifest.Base != nil && isTestProfileID(peek.Manifest.Base.ProfileID) {
		a.writeError(w, http.StatusForbidden, "test_parent_not_allowed", "a normal profile cannot inherit from a test profile")
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Test API routing.

func (a *API) serveTest(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path)
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "test" {
		a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	store, ok := a.store.(testProfileStore)
	if !ok || a.signer == nil {
		a.writeError(w, http.StatusServiceUnavailable, "test_api_unavailable", "test profile storage or automatic signing is unavailable")
		return
	}
	switch {
	case len(parts) == 3 && parts[2] == "usage":
		if r.Method != http.MethodGet {
			a.methodNotAllowed(w, http.MethodGet)
			return
		}
		if usage, ok := a.testUsageView(w, r.Context()); ok {
			a.writeJSON(w, http.StatusOK, usage)
		}
	case len(parts) == 3 && parts[2] == "profiles":
		switch r.Method {
		case http.MethodGet:
			profiles, ok := a.testProfileViews(w, r.Context(), r.URL.Query().Get("include_deleted") == "1")
			if ok {
				a.writeJSON(w, http.StatusOK, struct {
					SchemaVersion int               `json:"schema_version"`
					Profiles      []testProfileView `json:"profiles"`
				}{SchemaVersion, profiles})
			}
		case http.MethodPost:
			a.handleTestCreate(w, r, store)
		default:
			a.methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	case len(parts) == 5 && parts[2] == "objects" && parts[3] == "sha256":
		switch r.Method {
		case http.MethodHead:
			a.handleTestObjectHead(w, r, parts[4])
		case http.MethodPost:
			a.handleTestObjectUpload(w, r, store, parts[4])
		default:
			a.methodNotAllowed(w, http.MethodHead, http.MethodPost)
		}
	case len(parts) == 4 && parts[2] == "profiles":
		if r.Method != http.MethodDelete {
			a.methodNotAllowed(w, http.MethodDelete)
			return
		}
		if _, ok := a.requireTestProfile(w, r.Context(), store, parts[3]); ok {
			a.handleDeleteProfile(w, r, parts[3])
		}
	case len(parts) == 5 && parts[2] == "profiles" && parts[4] == "revisions":
		// Reading is allowed for every profile, test or not.
		if r.Method != http.MethodGet {
			a.methodNotAllowed(w, http.MethodGet)
			return
		}
		a.handleProfileRevisions(w, r, parts[3])
	case len(parts) == 5 && parts[2] == "profiles":
		id := parts[3]
		switch parts[4] {
		case "changes":
			if r.Method != http.MethodPost {
				a.methodNotAllowed(w, http.MethodPost)
				return
			}
			a.handleTestChanges(w, r, store, id)
		case "repin":
			if r.Method != http.MethodPost {
				a.methodNotAllowed(w, http.MethodPost)
				return
			}
			a.handleTestRepin(w, r, store, id)
		case "follow":
			if r.Method != http.MethodPut {
				a.methodNotAllowed(w, http.MethodPut)
				return
			}
			a.handleTestFollow(w, r, store, id)
		case "presentation":
			if r.Method != http.MethodPut {
				a.methodNotAllowed(w, http.MethodPut)
				return
			}
			if _, ok := a.requireTestProfile(w, r.Context(), store, id); ok {
				a.handlePresentation(w, r, id)
			}
		default:
			a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
		}
	case len(parts) == 7 && parts[2] == "profiles" && parts[4] == "channels":
		if r.Method != http.MethodPost {
			a.methodNotAllowed(w, http.MethodPost)
			return
		}
		if _, ok := a.requireTestProfile(w, r.Context(), store, parts[3]); !ok {
			return
		}
		switch parts[6] {
		case "promote":
			a.handlePromote(w, r, parts[3], parts[5])
		case "rollback":
			a.handleTestRollback(w, r, parts[3], parts[5])
		default:
			a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
		}
	default:
		a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
	}
}

// requireTestProfile is the write gate: the id carries the test prefix AND the
// server registered it as a test profile. Anything else is 403.
func (a *API) requireTestProfile(w http.ResponseWriter, ctx context.Context, store testProfileStore, id string) (storage.TestProfileRecord, bool) {
	if !isTestProfileID(id) {
		a.writeError(w, http.StatusForbidden, "not_a_test_profile", "only test profiles (profile_test-…) registered by the test API can be written here")
		return storage.TestProfileRecord{}, false
	}
	record, err := store.TestProfile(ctx, id)
	if errors.Is(err, storage.ErrTestProfileNotFound) {
		a.writeError(w, http.StatusForbidden, "not_a_test_profile", "only test profiles (profile_test-…) registered by the test API can be written here")
		return storage.TestProfileRecord{}, false
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "test profile state unavailable")
		return storage.TestProfileRecord{}, false
	}
	if record.Deleted {
		a.writeError(w, http.StatusNotFound, "profile_not_found", "test profile was deleted; its id stays burned")
		return storage.TestProfileRecord{}, false
	}
	return record, true
}

// ---------------------------------------------------------------------------
// Quotas.

type quotaView struct {
	Used    int64   `json:"used"`
	Max     int64   `json:"max"`
	Percent float64 `json:"percent"`
	Warning bool    `json:"warning"`
}

type diskView struct {
	FreeBytes   int64   `json:"free_bytes"`
	TotalBytes  int64   `json:"total_bytes"`
	MarginBytes int64   `json:"margin_bytes"`
	UsedPercent float64 `json:"used_percent"`
	Warning     bool    `json:"warning"`
	Available   bool    `json:"available"`
}

type testUsageView struct {
	SchemaVersion  int       `json:"schema_version"`
	Profiles       quotaView `json:"profiles"`
	Bytes          quotaView `json:"bytes"`
	ObjectMaxBytes int64     `json:"object_max_bytes"`
	// EffectiveMaxBytes is the byte quota capped by free disk minus margin.
	EffectiveMaxBytes int64    `json:"effective_max_bytes"`
	Disk              diskView `json:"disk"`
	WarnPercent       int      `json:"warn_percent"`
	Warnings          []string `json:"warnings"`
}

func newQuota(used, max int64) quotaView {
	q := quotaView{Used: used, Max: max}
	if max > 0 {
		q.Percent = float64(used) * 100 / float64(max)
	}
	q.Warning = q.Percent >= testQuotaWarnPercent
	return q
}

// computeTestUsage is the single quota calculation shared by the panel, the
// CLI and the upload/create checks.
func computeTestUsage(q TestQuotas, usage storage.TestUsage, free, total uint64, diskErr error) testUsageView {
	view := testUsageView{
		SchemaVersion: SchemaVersion, ObjectMaxBytes: q.MaxObjectBytes, WarnPercent: testQuotaWarnPercent,
		Profiles: newQuota(usage.LiveProfiles, int64(q.MaxProfiles)),
		Bytes:    newQuota(usage.CountedBytes, q.MaxBytes),
		Warnings: []string{},
	}
	view.EffectiveMaxBytes = q.MaxBytes
	view.Disk.MarginBytes = q.DiskMarginBytes
	if diskErr == nil && total > 0 {
		view.Disk.Available = true
		view.Disk.FreeBytes, view.Disk.TotalBytes = clampInt64(free), clampInt64(total)
		view.Disk.UsedPercent = float64(total-free) * 100 / float64(total)
		view.Disk.Warning = view.Disk.UsedPercent >= testQuotaWarnPercent
		room := view.Disk.FreeBytes - q.DiskMarginBytes
		if room < 0 {
			room = 0
		}
		if usage.CountedBytes+room < view.EffectiveMaxBytes {
			view.EffectiveMaxBytes = usage.CountedBytes + room
		}
	}
	if view.Profiles.Warning {
		view.Warnings = append(view.Warnings, fmt.Sprintf("perfiles de prueba al %.0f %% de la cuota", view.Profiles.Percent))
	}
	if view.Bytes.Warning {
		view.Warnings = append(view.Warnings, fmt.Sprintf("bytes de prueba al %.0f %% de la cuota", view.Bytes.Percent))
	}
	if view.Disk.Warning {
		view.Warnings = append(view.Warnings, fmt.Sprintf("disco al %.0f %% de uso", view.Disk.UsedPercent))
	}
	if !view.Disk.Available {
		view.Warnings = append(view.Warnings, "espacio libre del disco no disponible")
	}
	return view
}

func clampInt64(v uint64) int64 {
	if v > 1<<62 {
		return 1 << 62
	}
	return int64(v)
}

func (a *API) testUsageView(w http.ResponseWriter, ctx context.Context) (testUsageView, bool) {
	store, ok := a.store.(testProfileStore)
	if !ok {
		a.writeError(w, http.StatusServiceUnavailable, "test_api_unavailable", "test profile storage is unavailable")
		return testUsageView{}, false
	}
	usage, err := store.TestUsage(ctx)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "test quota usage unavailable")
		return testUsageView{}, false
	}
	var free, total uint64
	diskErr := errors.New("disk usage not configured")
	if a.diskUsage != nil {
		free, total, diskErr = a.diskUsage()
	}
	return computeTestUsage(a.testQuotas, usage, free, total, diskErr), true
}

// ---------------------------------------------------------------------------
// Listing.

type testProfileView struct {
	ProfileID      string              `json:"profile_id"`
	Name           string              `json:"name,omitempty"`
	Owner          string              `json:"owner"`
	CreatedAt      time.Time           `json:"created_at"`
	FollowParent   bool                `json:"follow_parent"`
	Deleted        bool                `json:"deleted,omitempty"`
	Parent         *revision.ParentRef `json:"parent,omitempty"`
	LatestRevision *revisionRef        `json:"latest_revision,omitempty"`
	OwnBytes       int64               `json:"own_bytes"`
}

func (a *API) testProfileViews(w http.ResponseWriter, ctx context.Context, includeDeleted bool) ([]testProfileView, bool) {
	store, ok := a.store.(testProfileStore)
	if !ok {
		a.writeError(w, http.StatusServiceUnavailable, "test_api_unavailable", "test profile storage is unavailable")
		return nil, false
	}
	records, err := store.ListTestProfiles(ctx)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "test profile inventory unavailable")
		return nil, false
	}
	views := make([]testProfileView, 0, len(records))
	for _, record := range records {
		if record.Deleted && !includeDeleted {
			continue
		}
		view := testProfileView{ProfileID: record.ProfileID, Owner: record.Owner, CreatedAt: record.CreatedAt,
			FollowParent: record.FollowParent, Deleted: record.Deleted}
		if latest, err := a.latestTestRevision(ctx, record.ProfileID); err == nil {
			manifest := latest.Manifest()
			view.Name, view.Parent = manifest.Profile.Name, manifest.Base
			view.LatestRevision = &revisionRef{RevisionID: latest.RevisionID(), Sequence: latest.Sequence(), ManifestSHA256: latest.ManifestSHA256()}
		}
		if bytes, err := store.TestProfileOwnBytes(ctx, record.ProfileID); err == nil {
			view.OwnBytes = bytes
		}
		views = append(views, view)
	}
	return views, true
}

// latestTestRevision returns the latest revision even of a deleted profile.
func (a *API) latestTestRevision(ctx context.Context, profileID string) (*revision.VerifiedRevision, error) {
	stored, err := a.store.ListSignedRevisionsForProfile(ctx, profileID, 1)
	if err != nil {
		return nil, err
	}
	if len(stored) == 0 {
		return nil, storage.ErrRevisionNotFound
	}
	verified, _, err := a.verifyStoredRevision(ctx, stored[0])
	return verified, err
}

// ---------------------------------------------------------------------------
// Objects.

func (a *API) handleTestObjectHead(w http.ResponseWriter, r *http.Request, digest string) {
	presence, ok := a.objects.(objectPresence)
	if !validDigest(digest) || !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	size, exists, err := presence.Has(r.Context(), digest)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("X-Object-Size", fmt.Sprint(size))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (a *API) handleTestObjectUpload(w http.ResponseWriter, r *http.Request, store testProfileStore, digest string) {
	if !validDigest(digest) {
		a.writeError(w, http.StatusBadRequest, "invalid_digest", "sha256 must be lowercase 64-hex")
		return
	}
	if r.ContentLength < 0 {
		a.writeError(w, http.StatusLengthRequired, "content_length_required", "object upload requires Content-Length")
		return
	}
	if r.ContentLength > a.testQuotas.MaxObjectBytes {
		a.writeError(w, http.StatusRequestEntityTooLarge, "quota_object_too_large",
			fmt.Sprintf("object of %d bytes exceeds the test API per-object limit of %d bytes", r.ContentLength, a.testQuotas.MaxObjectBytes))
		return
	}
	alreadyPresent := false
	if presence, ok := a.objects.(objectPresence); ok {
		if _, exists, err := presence.Has(r.Context(), digest); err == nil {
			alreadyPresent = exists
		}
	}
	if !alreadyPresent {
		usage, ok := a.testUsageView(w, r.Context())
		if !ok {
			return
		}
		if usage.Bytes.Used+r.ContentLength > usage.Bytes.Max {
			a.writeError(w, http.StatusConflict, "quota_bytes_exceeded",
				fmt.Sprintf("test uploads would use %d of %d bytes; delete test profiles or ask the admin to raise --test-max-bytes", usage.Bytes.Used+r.ContentLength, usage.Bytes.Max))
			return
		}
		if !usage.Disk.Available || r.ContentLength > usage.Disk.FreeBytes-usage.Disk.MarginBytes {
			a.writeError(w, http.StatusInsufficientStorage, "disk_space_low",
				fmt.Sprintf("the server keeps a free-disk margin of %d bytes; this upload would cross it", usage.Disk.MarginBytes))
			return
		}
	}
	if !a.putObject(w, r, digest, a.testQuotas.MaxObjectBytes) {
		return
	}
	if !alreadyPresent {
		if err := store.RecordTestObject(r.Context(), storage.Object{SHA256: digest, Size: r.ContentLength}, testauth.Agent(r.Context())); err != nil {
			log.Printf("test API: WARN object %s staged but not recorded for quota: %v", digest, err)
		}
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion   int    `json:"schema_version"`
		ProtocolVersion int    `json:"protocol_version"`
		Status          string `json:"status"`
		New             bool   `json:"new"`
	}{SchemaVersion, ProtocolVersion, "staged", !alreadyPresent})
}

// ---------------------------------------------------------------------------
// Create, change, repin, follow.

type testCreateRequest struct {
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	ParentProfileID  string `json:"parent_profile_id"`
	ParentRevisionID string `json:"parent_revision_id"`
	FollowParent     *bool  `json:"follow_parent"`
}

type testPublished struct {
	SchemaVersion   int            `json:"schema_version"`
	ProtocolVersion int            `json:"protocol_version"`
	ProfileID       string         `json:"profile_id"`
	Revision        revisionRef    `json:"revision"`
	Parent          *revisionRef   `json:"parent,omitempty"`
	FollowParent    bool           `json:"follow_parent"`
	Stable          string         `json:"stable,omitempty"`
	StableMessage   string         `json:"stable_message,omitempty"`
	AutoFollow      []followResult `json:"auto_follow,omitempty"`
}

func validTestSlug(slug string) bool {
	if len(slug) < 1 || len(slug) > testProfileSlugMaxLength || !isLowerAlphaNumByte(slug[0]) {
		return false
	}
	for i := 1; i < len(slug); i++ {
		if c := slug[i]; !isLowerAlphaNumByte(c) && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func isLowerAlphaNumByte(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }

func (a *API) handleTestCreate(w http.ResponseWriter, r *http.Request, store testProfileStore) {
	var req testCreateRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	slug := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(req.Slug), storage.TestProfilePrefix), "test-")
	if !validTestSlug(slug) {
		a.writeError(w, http.StatusBadRequest, "invalid_slug", fmt.Sprintf("slug must be 1..%d characters of [a-z0-9_-] starting with a letter or digit", testProfileSlugMaxLength))
		return
	}
	id := storage.TestProfilePrefix + slug
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Prueba " + slug
	}
	if len(name) > testProfileNameMaxLength {
		a.writeError(w, http.StatusBadRequest, "invalid_name", fmt.Sprintf("name must be at most %d bytes", testProfileNameMaxLength))
		return
	}
	follow := true
	if req.FollowParent != nil {
		follow = *req.FollowParent
	}
	if isTestProfileID(req.ParentProfileID) {
		if _, ok := a.requireTestProfile(w, r.Context(), store, req.ParentProfileID); !ok {
			return
		}
	}
	parent, status, code, message := a.resolveParent(r.Context(), req.ParentProfileID, req.ParentRevisionID)
	if status != 0 {
		a.writeError(w, status, code, message)
		return
	}
	usage, ok := a.testUsageView(w, r.Context())
	if !ok {
		return
	}
	if usage.Profiles.Used >= usage.Profiles.Max {
		a.writeError(w, http.StatusConflict, "quota_profiles_exceeded",
			fmt.Sprintf("%d of %d live test profiles already exist; delete one first", usage.Profiles.Used, usage.Profiles.Max))
		return
	}
	owner := testauth.Agent(r.Context())
	if err := store.RegisterTestProfile(r.Context(), id, owner, follow); err != nil {
		if errors.Is(err, storage.ErrTestProfileExists) {
			a.writeError(w, http.StatusConflict, "profile_id_taken", "this test profile id exists or was used before (deleted ids stay burned)")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "storage_error", "test profile could not be registered")
		return
	}
	parentManifest := parent.Manifest()
	revisionID, err := randomRevisionID()
	if err != nil {
		_ = store.ReleaseUnpublishedTestProfile(r.Context(), id)
		a.writeError(w, http.StatusInternalServerError, "internal_error", "revision id could not be generated")
		return
	}
	manifest := revision.Manifest{
		Revision: revision.Revision{ID: revisionID, Sequence: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339),
			ReleaseNotes: "test: creado por " + owner + " desde " + parent.ProfileID() + " rev " + parent.RevisionID()},
		Profile:     revision.Profile{ID: id, Name: name, Official: true, Description: "Perfil de prueba de " + owner + "."},
		Game:        parentManifest.Game,
		Base:        &revision.ParentRef{ProfileID: parent.ProfileID(), RevisionID: parent.RevisionID(), ManifestSHA256: parent.ManifestSHA256()},
		Permissions: testPermissions(parentManifest),
	}
	verified, published := a.publishTestManifest(w, r.Context(), manifest)
	if verified == nil {
		_ = store.ReleaseUnpublishedTestProfile(r.Context(), id)
		return
	}
	result := followResult{ProfileID: id, Revision: &published.Revision}
	if _, status, code, message := a.promoteChannel(r.Context(), id, testStableChannel, verified.RevisionID()); status != 0 {
		result.Stable, result.StableMessage = "promote_failed", code+": "+message
	} else {
		result.Stable = "promoted"
	}
	published.FollowParent, published.Stable, published.StableMessage = follow, result.Stable, result.StableMessage
	log.Printf("test API: %s created %s from %s rev %s", owner, id, parent.ProfileID(), parent.RevisionID())
	a.writeJSON(w, http.StatusCreated, published)
}

// testPermissions copies the parent's permissions so a test profile is never
// more permissive than its parent towards its own descendants.
func testPermissions(parent revision.Manifest) revision.Permissions {
	permissions := parent.Permissions
	permissions.MaxInheritanceDepth = revision.MaxInheritanceBases
	return permissions
}

// resolveParent loads and verifies a live parent profile at an exact revision
// or at its latest one ("" or "latest").
func (a *API) resolveParent(ctx context.Context, profileID, revisionID string) (*revision.VerifiedRevision, int, string, string) {
	if profileID == "" {
		return nil, http.StatusBadRequest, "invalid_parent", "parent_profile_id is required"
	}
	head, err := a.store.ProfileHead(ctx, profileID)
	if errors.Is(err, storage.ErrRevisionNotFound) {
		return nil, http.StatusNotFound, "parent_not_found", "parent profile does not exist or was deleted"
	}
	if err != nil {
		return nil, http.StatusInternalServerError, "storage_error", "parent lookup failed"
	}
	if revisionID == "" || revisionID == "latest" {
		revisionID = head.RevisionID
	}
	stored, err := a.store.SignedRevision(ctx, revisionID)
	if err != nil || stored.ProfileID != profileID {
		if err == nil || errors.Is(err, storage.ErrRevisionNotFound) {
			return nil, http.StatusNotFound, "parent_revision_not_found", "parent revision not found for that profile"
		}
		return nil, http.StatusInternalServerError, "storage_error", "parent revision lookup failed"
	}
	verified, _, err := a.verifyStoredRevision(ctx, stored)
	if err != nil {
		return nil, http.StatusInternalServerError, "revision_corrupt", "parent revision failed integrity verification"
	}
	return verified, 0, "", ""
}

// publishTestManifest signs and publishes one test revision through the same
// publishCore as every other publication, then lets its own children follow.
// On failure it writes the error and returns nil.
func (a *API) publishTestManifest(w http.ResponseWriter, ctx context.Context, manifest revision.Manifest) (*revision.VerifiedRevision, testPublished) {
	normalizeTestManifest(&manifest)
	raw, err := json.Marshal(manifest)
	if err != nil {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_manifest", err.Error())
		return nil, testPublished{}
	}
	canonical, err := revision.CanonicalizeValidatedManifest(raw)
	if err != nil {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_manifest", "test revision is invalid: "+err.Error())
		return nil, testPublished{}
	}
	request, err := json.Marshal(map[string]any{
		"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": sha256Hex(canonical), "manifest": json.RawMessage(canonical),
	})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return nil, testPublished{}
	}
	envelope, err := a.signer.SignRequest(request)
	if err != nil {
		a.writeRevisionVerifyError(w, err)
		return nil, testPublished{}
	}
	capture := &captureWriter{header: http.Header{}}
	verified, status := a.publishCore(capture, ctx, envelope)
	if verified == nil {
		var body errorBody
		if json.Unmarshal(capture.body.Bytes(), &body) == nil && body.Error.Code == "parent_verification_failed" {
			// The chain checks (config_settings rules, JVM options) rejected the content.
			a.writeError(w, http.StatusUnprocessableEntity, "inheritance_rule_violation", "the parent chain rejected this content: an inherited config_settings or jvm_options rule, or an unreadable parent")
			return nil, testPublished{}
		}
		for key, values := range capture.header {
			w.Header()[key] = values
		}
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
		return nil, testPublished{}
	}
	out := testPublished{
		SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion, ProfileID: verified.ProfileID(),
		Revision: revisionRef{RevisionID: verified.RevisionID(), Sequence: verified.Sequence(), ManifestSHA256: verified.ManifestSHA256()},
	}
	if base := verified.Parent(); base != nil {
		out.Parent = &revisionRef{RevisionID: base.RevisionID, ManifestSHA256: base.ManifestSHA256}
	}
	if status == http.StatusCreated {
		out.AutoFollow = a.followParent(ctx, verified)
	}
	return verified, out
}

// normalizeTestManifest picks the lowest schema that can carry the content and
// keeps every list present, as the signed shape requires.
func normalizeTestManifest(m *revision.Manifest) {
	switch {
	case m.Game.Loader != nil:
		m.SchemaVersion = 4
	case len(m.JVMOptions) != 0 || len(m.RemoveJVMOptions) != 0:
		m.SchemaVersion = 3
	case len(m.ConfigSettings) != 0:
		m.SchemaVersion = 2
	default:
		m.SchemaVersion = 1
	}
	if m.Mods == nil {
		m.Mods = []revision.ModEntry{}
	}
	if m.RemoveMods == nil {
		m.RemoveMods = []revision.RemoveMod{}
	}
	if m.Configs == nil {
		m.Configs = []revision.ConfigEntry{}
	}
	if m.RemoveConfigs == nil {
		m.RemoveConfigs = []revision.RemoveConfig{}
	}
	if m.Objects == nil {
		m.Objects = []revision.ObjectEntry{}
	}
	if len(m.ConfigSettings) == 0 {
		m.ConfigSettings = nil
	}
	if len(m.JVMOptions) == 0 {
		m.JVMOptions = nil
	}
	if len(m.RemoveJVMOptions) == 0 {
		m.RemoveJVMOptions = nil
	}
}

// nextTestRevision prepares the successor of a test profile's latest revision.
func nextTestRevision(latest *revision.VerifiedRevision, notes string) (revision.Manifest, error) {
	id, err := randomRevisionID()
	if err != nil {
		return revision.Manifest{}, err
	}
	manifest := latest.Manifest()
	manifest.Revision = revision.Revision{ID: id, Sequence: latest.Sequence() + 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), ReleaseNotes: notes}
	return manifest, nil
}

// advanceStable moves the test profile's stable revision along when it was
// tracking the previous latest revision, like automatic parent following.
func (a *API) advanceStable(ctx context.Context, published *testPublished, previousID string) {
	result := followResult{ProfileID: published.ProfileID, Revision: &published.Revision}
	a.followStable(ctx, &result, previousID)
	published.Stable, published.StableMessage = result.Stable, result.StableMessage
}

type testOperation struct {
	Op               string          `json:"op"`
	Name             string          `json:"name,omitempty"`
	ID               string          `json:"id,omitempty"`
	SHA256           string          `json:"sha256,omitempty"`
	Size             *int64          `json:"size,omitempty"`
	Path             string          `json:"path,omitempty"`
	Policy           string          `json:"policy,omitempty"`
	Format           string          `json:"format,omitempty"`
	Key              string          `json:"key,omitempty"`
	Value            json.RawMessage `json:"value,omitempty"`
	JVMOptions       json.RawMessage `json:"jvm_options,omitempty"`
	RemoveJVMOptions []string        `json:"remove_jvm_options,omitempty"`
}

type testChangeRequest struct {
	Notes      string          `json:"notes"`
	Operations []testOperation `json:"operations"`
}

type testOperationError struct {
	status  int
	code    string
	message string
}

func (e *testOperationError) Error() string { return e.message }

func opError(status int, code, format string, args ...any) error {
	return &testOperationError{status: status, code: code, message: fmt.Sprintf(format, args...)}
}

func (a *API) handleTestChanges(w http.ResponseWriter, r *http.Request, store testProfileStore, id string) {
	if _, ok := a.requireTestProfile(w, r.Context(), store, id); !ok {
		return
	}
	var req testChangeRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	if len(req.Operations) == 0 && strings.TrimSpace(req.Notes) == "" {
		a.writeError(w, http.StatusBadRequest, "empty_change", "a change needs at least one operation or notes")
		return
	}
	if len(req.Operations) > maxTestOperations || len(req.Notes) > testReleaseNotesMaxLength {
		a.writeError(w, http.StatusBadRequest, "change_too_large", fmt.Sprintf("at most %d operations and %d bytes of notes", maxTestOperations, testReleaseNotesMaxLength))
		return
	}
	latest, err := a.latestTestRevision(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "latest test revision unavailable")
		return
	}
	notes := strings.TrimSpace(req.Notes)
	if notes == "" {
		notes = fmt.Sprintf("test: %d operaciones de %s", len(req.Operations), testauth.Agent(r.Context()))
	}
	manifest, err := nextTestRevision(latest, notes)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "internal_error", "revision id could not be generated")
		return
	}
	chain, err := a.parentChain(r.Context(), manifest.Base)
	if err != nil {
		a.writeError(w, http.StatusConflict, "parent_not_found", "pinned parent chain is unavailable")
		return
	}
	inherited := effectiveFiles(chain)
	for i, op := range req.Operations {
		if err := applyTestOperation(&manifest, inherited, chain[len(chain)-1].Permissions, op); err != nil {
			var opErr *testOperationError
			if errors.As(err, &opErr) {
				a.writeError(w, opErr.status, opErr.code, fmt.Sprintf("operations[%d] (%s): %s", i, op.Op, opErr.message))
			} else {
				a.writeError(w, http.StatusBadRequest, "invalid_operation", fmt.Sprintf("operations[%d] (%s): %v", i, op.Op, err))
			}
			return
		}
	}
	verified, published := a.publishTestManifest(w, r.Context(), manifest)
	if verified == nil {
		return
	}
	a.advanceStable(r.Context(), &published, latest.RevisionID())
	published.FollowParent = a.followsParent(r.Context(), id)
	a.writeJSON(w, http.StatusCreated, published)
}

func (a *API) handleTestRepin(w http.ResponseWriter, r *http.Request, store testProfileStore, id string) {
	if _, ok := a.requireTestProfile(w, r.Context(), store, id); !ok {
		return
	}
	var req struct {
		ParentRevisionID string `json:"parent_revision_id"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	latest, err := a.latestTestRevision(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "latest test revision unavailable")
		return
	}
	base := latest.Parent()
	if base == nil {
		a.writeError(w, http.StatusConflict, "no_parent", "this test profile has no parent to re-pin")
		return
	}
	parent, status, code, message := a.resolveParent(r.Context(), base.ProfileID, req.ParentRevisionID)
	if status != 0 {
		a.writeError(w, status, code, message)
		return
	}
	if parent.RevisionID() == base.RevisionID {
		a.writeError(w, http.StatusConflict, "already_pinned", "the test profile is already pinned to that parent revision")
		return
	}
	manifest, err := nextTestRevision(latest, "test: repin a "+parent.ProfileID()+" rev "+parent.RevisionID())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "internal_error", "revision id could not be generated")
		return
	}
	parentManifest := parent.Manifest()
	manifest.Base = &revision.ParentRef{ProfileID: parent.ProfileID(), RevisionID: parent.RevisionID(), ManifestSHA256: parent.ManifestSHA256()}
	manifest.Game = parentManifest.Game
	manifest.Permissions = testPermissions(parentManifest)
	verified, published := a.publishTestManifest(w, r.Context(), manifest)
	if verified == nil {
		return
	}
	a.advanceStable(r.Context(), &published, latest.RevisionID())
	published.FollowParent = a.followsParent(r.Context(), id)
	a.writeJSON(w, http.StatusCreated, published)
}

func (a *API) handleTestFollow(w http.ResponseWriter, r *http.Request, store testProfileStore, id string) {
	if _, ok := a.requireTestProfile(w, r.Context(), store, id); !ok {
		return
	}
	var req struct {
		FollowParent *bool `json:"follow_parent"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil || req.FollowParent == nil {
		a.writeError(w, http.StatusBadRequest, "invalid_request", `body must be {"follow_parent": true|false}`)
		return
	}
	if err := store.SetTestProfileFollow(r.Context(), id, *req.FollowParent); err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "follow state could not be saved")
		return
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion int    `json:"schema_version"`
		ProfileID     string `json:"profile_id"`
		FollowParent  bool   `json:"follow_parent"`
	}{SchemaVersion, id, *req.FollowParent})
}

// handleTestRollback lets the server sign the rollback statement, since test
// profiles have no offline release key.
func (a *API) handleTestRollback(w http.ResponseWriter, r *http.Request, profileID, channel string) {
	signer, ok := a.signer.(rollbackSigner)
	if !ok {
		a.writeError(w, http.StatusServiceUnavailable, "publication_unavailable", "server rollback signing is unavailable")
		return
	}
	var req struct {
		RevisionID string `json:"revision_id"`
		Reason     string `json:"reason"`
	}
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "test: rollback de " + testauth.Agent(r.Context())
	}
	record, err := a.store.Channel(r.Context(), profileID, channel)
	if err != nil {
		a.writeError(w, http.StatusConflict, "channel_not_found", "rollback requires an existing channel head")
		return
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		a.writeError(w, http.StatusInternalServerError, "internal_error", "event id could not be generated")
		return
	}
	event, err := signer.SignRollback("rollback-test-"+hex.EncodeToString(suffix[:]), revision.RollbackStatement{
		ProfileID: profileID, Channel: channel, FromRevisionID: record.RevisionID, FromSequence: record.Sequence,
		ToRevisionID: req.RevisionID, Reason: reason, IssuedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "signing_failed", "rollback statement could not be signed")
		return
	}
	candidate, status, code, message := a.rollbackChannel(r.Context(), profileID, channel, req.RevisionID, event)
	if status != 0 {
		a.writeError(w, status, code, message)
		return
	}
	a.writeChannel(w, candidate)
}

// ---------------------------------------------------------------------------
// Effective content and operations.

// parentChain returns the verified manifests from the root down to the pinned
// parent (last element).
func (a *API) parentChain(ctx context.Context, pin *revision.ParentRef) ([]revision.Manifest, error) {
	var chain []revision.Manifest
	for pin != nil {
		if len(chain) > revision.MaxInheritanceBases {
			return nil, revision.ErrInheritanceTooDeep
		}
		stored, err := a.store.SignedRevision(ctx, pin.RevisionID)
		if err != nil {
			return nil, err
		}
		verified, _, err := a.verifyStoredRevision(ctx, stored)
		if err != nil {
			return nil, err
		}
		if verified.ProfileID() != pin.ProfileID || verified.ManifestSHA256() != pin.ManifestSHA256 {
			return nil, revision.ErrParentPinMismatch
		}
		manifest := verified.Manifest()
		chain = append([]revision.Manifest{manifest}, chain...)
		pin = manifest.Base
	}
	if len(chain) == 0 {
		return nil, revision.ErrParentMissing
	}
	return chain, nil
}

type effectiveFile struct {
	Kind   string
	ID     string
	Object revision.ObjectRef
}

// effectiveFiles resolves the inherited file tree by path, the same way the
// panel and the launchers apply removals and then additions per layer.
func effectiveFiles(chain []revision.Manifest) map[string]effectiveFile {
	files := make(map[string]effectiveFile)
	for _, m := range chain {
		for _, removal := range m.RemoveMods {
			for p, f := range files {
				if f.Kind == "mod" && f.ID == removal.ID {
					delete(files, p)
					break
				}
			}
		}
		for _, removal := range m.RemoveConfigs {
			delete(files, removal.Path)
		}
		for _, entry := range m.Mods {
			files[entry.Path] = effectiveFile{Kind: "mod", ID: entry.ID, Object: entry.Object}
		}
		for _, entry := range m.Configs {
			files[entry.Path] = effectiveFile{Kind: "config", Object: entry.Object}
		}
		for _, entry := range m.Objects {
			files[entry.Path] = effectiveFile{Kind: "object", ID: entry.ID, Object: entry.Object}
		}
	}
	return files
}

func applyTestOperation(m *revision.Manifest, inherited map[string]effectiveFile, parentPermissions revision.Permissions, op testOperation) error {
	switch op.Op {
	case "add_mod":
		name := op.Name
		if name == "" || strings.ContainsAny(name, `/\`) || !strings.HasSuffix(strings.ToLower(name), ".jar") {
			return opError(http.StatusBadRequest, "invalid_operation", "name must be a .jar file name without directories")
		}
		ref, err := operationObject(op, "application/java-archive")
		if err != nil {
			return err
		}
		filePath := "mods/" + name
		existing, isInherited := inherited[filePath]
		if isInherited && existing.Kind != "mod" {
			return opError(http.StatusConflict, "path_conflict", "%s is inherited as %s", filePath, existing.Kind)
		}
		if !parentPermissions.Mods.Add {
			return opError(http.StatusForbidden, "parent_permission_denied", "the parent profile does not allow adding or replacing mods")
		}
		id := op.ID
		if isInherited {
			id = existing.ID
		} else if id == "" {
			id = defaultModID(name)
			for p, f := range inherited {
				if f.Kind == "mod" && f.ID == id && p != filePath {
					id = id + "-" + ref.SHA256[:8]
					break
				}
			}
		}
		removeOwnPath(m, filePath)
		m.RemoveMods = filterRemoveMods(m.RemoveMods, id)
		entry := revision.ModEntry{ID: id, Path: filePath, Object: ref}
		if isInherited {
			expected := existing.Object.SHA256
			entry.ExpectBaseObjectSHA256 = &expected
		}
		m.Mods = append(m.Mods, entry)
	case "remove_mod":
		filePath, err := findMod(m, inherited, op)
		if err != nil {
			return err
		}
		removeOwnPath(m, filePath)
		if parent, ok := inherited[filePath]; ok {
			if !parentPermissions.Mods.Remove {
				return opError(http.StatusForbidden, "parent_permission_denied", "the parent profile does not allow removing inherited mods")
			}
			m.RemoveMods = append(filterRemoveMods(m.RemoveMods, parent.ID), revision.RemoveMod{ID: parent.ID, ExpectBaseObjectSHA256: parent.Object.SHA256})
		}
	case "set_config":
		if op.Path == "" {
			return opError(http.StatusBadRequest, "invalid_operation", "path is required")
		}
		ref, err := operationObject(op, "")
		if err != nil {
			return err
		}
		policy := op.Policy
		if policy == "" {
			policy = testDefaultConfigPolicy
		}
		existing, isInherited := inherited[op.Path]
		if isInherited && existing.Kind != "config" {
			return opError(http.StatusConflict, "path_conflict", "%s is inherited as %s", op.Path, existing.Kind)
		}
		removeOwnPath(m, op.Path)
		m.RemoveConfigs = filterRemoveConfigs(m.RemoveConfigs, op.Path)
		entry := revision.ConfigEntry{Path: op.Path, Object: ref, Policy: policy}
		if isInherited {
			expected := existing.Object.SHA256
			entry.ExpectBaseObjectSHA256 = &expected
		}
		m.Configs = append(m.Configs, entry)
	case "remove_config":
		if op.Path == "" {
			return opError(http.StatusBadRequest, "invalid_operation", "path is required")
		}
		_, own := ownConfig(m, op.Path)
		parent, isInherited := inherited[op.Path]
		if !own && (!isInherited || parent.Kind != "config") {
			return opError(http.StatusNotFound, "not_found", "config %s is not in the effective profile", op.Path)
		}
		removeOwnPath(m, op.Path)
		m.ConfigSettings = filterSettings(m.ConfigSettings, func(s revision.ConfigSetting) bool { return s.Path != op.Path })
		if isInherited && parent.Kind == "config" {
			m.RemoveConfigs = append(filterRemoveConfigs(m.RemoveConfigs, op.Path), revision.RemoveConfig{Path: op.Path, ExpectBaseObjectSHA256: parent.Object.SHA256})
		}
	case "set_config_setting":
		var value any
		if len(op.Value) == 0 || json.Unmarshal(op.Value, &value) != nil {
			return opError(http.StatusBadRequest, "invalid_operation", "value is required")
		}
		policy := op.Policy
		if policy == "" {
			policy = testDefaultConfigPolicy
		}
		setting := revision.ConfigSetting{Path: op.Path, Format: op.Format, Key: op.Key, Value: value, Policy: policy}
		m.ConfigSettings = append(filterSettings(m.ConfigSettings, func(s revision.ConfigSetting) bool { return s.Identity() != setting.Identity() }), setting)
	case "remove_config_setting":
		before := len(m.ConfigSettings)
		m.ConfigSettings = filterSettings(m.ConfigSettings, func(s revision.ConfigSetting) bool { return s.Path != op.Path || s.Key != op.Key })
		if len(m.ConfigSettings) == before {
			return opError(http.StatusNotFound, "not_found", "this profile has no own setting %s/%s", op.Path, op.Key)
		}
	case "set_jvm_options":
		var options []revision.JVMOption
		if len(op.JVMOptions) != 0 {
			if err := json.Unmarshal(op.JVMOptions, &options); err != nil {
				return opError(http.StatusBadRequest, "invalid_operation", "jvm_options must be a list of JVM option objects")
			}
		}
		m.JVMOptions, m.RemoveJVMOptions = options, op.RemoveJVMOptions
	default:
		return opError(http.StatusBadRequest, "invalid_operation", "unknown op %q", op.Op)
	}
	return nil
}

func operationObject(op testOperation, mediaType string) (revision.ObjectRef, error) {
	if !validDigest(op.SHA256) || op.Size == nil || *op.Size < 0 {
		return revision.ObjectRef{}, opError(http.StatusBadRequest, "invalid_operation", "sha256 (lowercase hex) and size are required; upload the object first")
	}
	return revision.ObjectRef{SHA256: op.SHA256, Size: *op.Size, MediaType: mediaType}, nil
}

// findMod resolves a mod by file name or sha256 in the effective profile.
func findMod(m *revision.Manifest, inherited map[string]effectiveFile, op testOperation) (string, error) {
	effective := make(map[string]effectiveFile, len(inherited))
	for p, f := range inherited {
		effective[p] = f
	}
	for _, removal := range m.RemoveMods {
		for p, f := range effective {
			if f.Kind == "mod" && f.ID == removal.ID {
				delete(effective, p)
			}
		}
	}
	for _, entry := range m.Mods {
		effective[entry.Path] = effectiveFile{Kind: "mod", ID: entry.ID, Object: entry.Object}
	}
	var matches []string
	for p, f := range effective {
		if f.Kind != "mod" {
			continue
		}
		if (op.Name != "" && (path.Base(p) == op.Name || f.ID == op.Name)) || (op.SHA256 != "" && f.Object.SHA256 == op.SHA256) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return "", opError(http.StatusNotFound, "not_found", "no mod named %q or with sha256 %q in the effective profile", op.Name, op.SHA256)
	case 1:
		return matches[0], nil
	default:
		return "", opError(http.StatusConflict, "ambiguous_mod", "%d mods match; use the exact file name", len(matches))
	}
}

func defaultModID(name string) string {
	base := strings.TrimSuffix(name, path.Ext(name))
	var b strings.Builder
	for _, c := range base {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	id := b.String()
	if len(id) > 100 {
		id = id[:100]
	}
	if id == "" {
		id = "mod"
	}
	return "mod_" + id
}

func removeOwnPath(m *revision.Manifest, filePath string) {
	mods := m.Mods[:0:0]
	for _, entry := range m.Mods {
		if entry.Path != filePath {
			mods = append(mods, entry)
		}
	}
	configs := m.Configs[:0:0]
	for _, entry := range m.Configs {
		if entry.Path != filePath {
			configs = append(configs, entry)
		}
	}
	m.Mods, m.Configs = mods, configs
}

func ownConfig(m *revision.Manifest, filePath string) (revision.ConfigEntry, bool) {
	for _, entry := range m.Configs {
		if entry.Path == filePath {
			return entry, true
		}
	}
	return revision.ConfigEntry{}, false
}

func filterRemoveMods(list []revision.RemoveMod, id string) []revision.RemoveMod {
	out := list[:0:0]
	for _, item := range list {
		if item.ID != id {
			out = append(out, item)
		}
	}
	return out
}

func filterRemoveConfigs(list []revision.RemoveConfig, filePath string) []revision.RemoveConfig {
	out := list[:0:0]
	for _, item := range list {
		if item.Path != filePath {
			out = append(out, item)
		}
	}
	return out
}

func filterSettings(list []revision.ConfigSetting, keep func(revision.ConfigSetting) bool) []revision.ConfigSetting {
	out := list[:0:0]
	for _, item := range list {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}
