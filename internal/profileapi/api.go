// Package profileapi exposes the versioned HTTP boundary for global profiles.
//
// Integration contract:
//
//	handler, err := profileapi.New(profileapi.Dependencies{
//	    Objects: cas, Store: sqliteStore, Keys: trustedReleaseKeys,
//	}, profileapi.Options{
//	    ReadMiddleware:  clientAuthMiddleware,
//	    AdminMiddleware: adminSessionAndCSRFMiddleware,
//	})
//
// The package never owns a release private key, listener, TLS configuration or
// administrator session. Both middleware hooks are mandatory so wiring cannot
// accidentally expose the private distribution or mutation routes.
package profileapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

const (
	SchemaVersion   = 1
	ProtocolVersion = 1

	defaultMaxJSONBytes   int64 = 4 << 20
	defaultMaxObjectBytes int64 = storage.DefaultMaxObjectBytes
	defaultListLimit            = 500
)

var (
	ErrKeyNotFound           = errors.New("release public key not found")
	ErrStoredRevisionCorrupt = errors.New("stored signed revision is inconsistent")
)

type Middleware func(http.Handler) http.Handler

type KeyResolver interface {
	PublicKey(context.Context, string) (ed25519.PublicKey, error)
}

type ObjectStore interface {
	Put(context.Context, storage.Object, io.Reader) error
	Verify(context.Context, storage.Object) error
	Open(context.Context, string) (io.ReadCloser, int64, error)
}

type RevisionStore interface {
	PublishSignedRevision(context.Context, storage.RevisionPublication, []byte) error
	SignedRevision(context.Context, string) (storage.StoredSignedRevision, error)
	ListSignedRevisions(context.Context, int) ([]storage.StoredSignedRevision, error)
	ProfileHead(context.Context, string) (storage.StoredRevision, error)
	PublishedObject(context.Context, string) (storage.Object, error)
	Channel(context.Context, string, string) (storage.ChannelRecord, error)
	ListChannels(context.Context) ([]storage.ChannelRecord, error)
	CompareAndSetChannel(context.Context, *storage.ChannelRecord, storage.ChannelRecord) error
}

type Dependencies struct {
	Objects ObjectStore
	Store   RevisionStore
	Keys    KeyResolver
}

type Options struct {
	ReadMiddleware  Middleware
	AdminMiddleware Middleware
	MaxJSONBytes    int64
	MaxObjectBytes  int64
	ListLimit       int
}

type API struct {
	objects        ObjectStore
	store          RevisionStore
	keys           KeyResolver
	maxJSONBytes   int64
	maxObjectBytes int64
	listLimit      int
}

func New(deps Dependencies, opts Options) (http.Handler, error) {
	if deps.Objects == nil || deps.Store == nil || deps.Keys == nil {
		return nil, errors.New("profileapi requires object store, revision store and public-key resolver")
	}
	if opts.ReadMiddleware == nil {
		return nil, errors.New("profileapi requires authenticated read middleware")
	}
	if opts.AdminMiddleware == nil {
		return nil, errors.New("profileapi requires administrator session/CSRF middleware")
	}
	if opts.MaxJSONBytes <= 0 {
		opts.MaxJSONBytes = defaultMaxJSONBytes
	}
	if opts.MaxObjectBytes <= 0 {
		opts.MaxObjectBytes = defaultMaxObjectBytes
	}
	if opts.ListLimit < 1 || opts.ListLimit > 1000 {
		opts.ListLimit = defaultListLimit
	}

	api := &API{
		objects: deps.Objects, store: deps.Store, keys: deps.Keys,
		maxJSONBytes: opts.MaxJSONBytes, maxObjectBytes: opts.MaxObjectBytes,
		listLimit: opts.ListLimit,
	}
	root := http.NewServeMux()
	root.Handle("/v1/admin/", opts.AdminMiddleware(http.HandlerFunc(api.serveAdmin)))
	root.Handle("/v1/", opts.ReadMiddleware(http.HandlerFunc(api.serveRead)))
	return root, nil
}

type errorBody struct {
	SchemaVersion   int      `json:"schema_version"`
	ProtocolVersion int      `json:"protocol_version"`
	Error           apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type okBody struct {
	SchemaVersion   int    `json:"schema_version"`
	ProtocolVersion int    `json:"protocol_version"`
	Status          string `json:"status"`
}

type revisionRef struct {
	RevisionID     string `json:"revision_id"`
	Sequence       int64  `json:"sequence"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

type channelView struct {
	Name string `json:"name"`
	revisionRef
}

type profileView struct {
	ProfileID       string        `json:"profile_id"`
	Name            string        `json:"name"`
	LatestRevision  revisionRef   `json:"latest_revision"`
	Channels        []channelView `json:"channels"`
	PublishedCount  int           `json:"published_revision_count,omitempty"`
}

func (a *API) serveRead(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/profiles" {
		if r.Method != http.MethodGet {
			a.methodNotAllowed(w, http.MethodGet)
			return
		}
		a.handleProfiles(w, r, false)
		return
	}
	parts := pathParts(r.URL.Path)
	if len(parts) == 5 && parts[0] == "v1" && parts[1] == "profiles" && parts[3] == "revisions" {
		if r.Method != http.MethodGet {
			a.methodNotAllowed(w, http.MethodGet)
			return
		}
		a.handleRevision(w, r, parts[2], parts[4])
		return
	}
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "objects" && parts[2] == "sha256" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			a.methodNotAllowed(w, http.MethodGet, http.MethodHead)
			return
		}
		a.handleObjectDownload(w, r, parts[3])
		return
	}
	a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a *API) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/admin/profiles" {
		if r.Method != http.MethodGet {
			a.methodNotAllowed(w, http.MethodGet)
			return
		}
		a.handleProfiles(w, r, true)
		return
	}
	if r.URL.Path == "/v1/admin/revisions" {
		if r.Method != http.MethodPost {
			a.methodNotAllowed(w, http.MethodPost)
			return
		}
		a.handlePublishRevision(w, r)
		return
	}
	parts := pathParts(r.URL.Path)
	if len(parts) == 5 && parts[0] == "v1" && parts[1] == "admin" && parts[2] == "objects" && parts[3] == "sha256" {
		if r.Method != http.MethodPost {
			a.methodNotAllowed(w, http.MethodPost)
			return
		}
		a.handleObjectUpload(w, r, parts[4])
		return
	}
	if len(parts) == 7 && parts[0] == "v1" && parts[1] == "admin" && parts[2] == "profiles" && parts[4] == "channels" {
		if r.Method != http.MethodPost {
			a.methodNotAllowed(w, http.MethodPost)
			return
		}
		switch parts[6] {
		case "promote":
			a.handlePromote(w, r, parts[3], parts[5])
		case "rollback":
			a.handleRollback(w, r, parts[3], parts[5])
		default:
			a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
		}
		return
	}
	a.writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a *API) handleObjectUpload(w http.ResponseWriter, r *http.Request, digest string) {
	if !validDigest(digest) {
		a.writeError(w, http.StatusBadRequest, "invalid_digest", "sha256 must be lowercase 64-hex")
		return
	}
	if r.ContentLength < 0 {
		a.writeError(w, http.StatusLengthRequired, "content_length_required", "object upload requires Content-Length")
		return
	}
	if r.ContentLength > a.maxObjectBytes {
		a.writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "object exceeds configured upload limit")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.maxObjectBytes+1)
	err := a.objects.Put(r.Context(), storage.Object{SHA256: digest, Size: r.ContentLength}, r.Body)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrDigestMismatch):
			a.writeError(w, http.StatusUnprocessableEntity, "object_hash_mismatch", "uploaded bytes do not match requested sha256")
		case errors.Is(err, storage.ErrSizeMismatch):
			a.writeError(w, http.StatusUnprocessableEntity, "object_size_mismatch", "uploaded bytes do not match Content-Length")
		case errors.Is(err, storage.ErrInvalidDigest):
			a.writeError(w, http.StatusBadRequest, "invalid_digest", "sha256 is invalid")
		default:
			a.writeError(w, http.StatusInternalServerError, "storage_error", "object could not be staged")
		}
		return
	}
	a.writeJSON(w, http.StatusOK, okBody{SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion, Status: "staged"})
}

func (a *API) handleObjectDownload(w http.ResponseWriter, r *http.Request, digest string) {
	if !validDigest(digest) {
		a.writeError(w, http.StatusNotFound, "not_found", "object not found")
		return
	}
	object, err := a.store.PublishedObject(r.Context(), digest)
	if err != nil {
		if errors.Is(err, storage.ErrObjectMissing) || errors.Is(err, storage.ErrInvalidDigest) {
			a.writeError(w, http.StatusNotFound, "not_found", "object not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "storage_error", "object metadata unavailable")
		return
	}
	if err := a.objects.Verify(r.Context(), object); err != nil {
		a.writeError(w, http.StatusInternalServerError, "object_corrupt", "published object failed integrity verification")
		return
	}
	f, size, err := a.objects.Open(r.Context(), digest)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "object_unavailable", "published object unavailable")
		return
	}
	defer f.Close()
	if size != object.Size {
		a.writeError(w, http.StatusInternalServerError, "object_corrupt", "published object size changed")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("ETag", `"sha256-`+digest+`"`)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-BootOptim-Protocol-Version", strconv.Itoa(ProtocolVersion))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		return
	}
}

func (a *API) handlePublishRevision(w http.ResponseWriter, r *http.Request) {
	raw, err := a.readJSONBody(w, r)
	if err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	verified, canonicalEnvelope, err := a.verifyRevisionEnvelope(r.Context(), raw)
	if err != nil {
		a.writeRevisionVerifyError(w, err)
		return
	}

	if existing, err := a.store.SignedRevision(r.Context(), verified.RevisionID()); err == nil {
		if existing.ProfileID != verified.ProfileID() || existing.ManifestSHA256 != verified.ManifestSHA256() ||
			!bytes.Equal(existing.Envelope, canonicalEnvelope) {
			a.writeError(w, http.StatusConflict, "immutable_conflict", "revision id already identifies different immutable content")
			return
		}
		objects, collectErr := a.expectedObjects(verified.Manifest())
		if collectErr != nil {
			a.writeError(w, http.StatusUnprocessableEntity, "invalid_object_set", collectErr.Error())
			return
		}
		pub := storage.RevisionPublication{
			RevisionID: verified.RevisionID(), ProfileID: verified.ProfileID(), Sequence: verified.Sequence(),
			Manifest: storage.ValidatedManifest{Bytes: verified.CanonicalManifest(), SHA256: verified.ManifestSHA256()},
			ExpectedObjects: objects,
		}
		if err := a.store.PublishSignedRevision(r.Context(), pub, canonicalEnvelope); err != nil {
			a.writeStoragePublishError(w, err)
			return
		}
		a.writePublished(w, http.StatusOK, verified)
		return
	} else if !errors.Is(err, storage.ErrRevisionNotFound) {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "revision lookup failed")
		return
	}

	if head, err := a.store.ProfileHead(r.Context(), verified.ProfileID()); err == nil {
		if verified.Sequence() <= head.Sequence {
			a.writeError(w, http.StatusConflict, "sequence_conflict", "revision sequence must advance the profile sequence")
			return
		}
	} else if !errors.Is(err, storage.ErrRevisionNotFound) {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "profile sequence lookup failed")
		return
	}

	if err := a.validateParentChain(r.Context(), verified); err != nil {
		switch {
		case errors.Is(err, revision.ErrParentMissing), errors.Is(err, storage.ErrRevisionNotFound):
			a.writeError(w, http.StatusConflict, "parent_not_found", "pinned parent revision is unavailable")
		case errors.Is(err, revision.ErrParentPinMismatch):
			a.writeError(w, http.StatusConflict, "parent_pin_mismatch", "pinned parent identity or digest does not match")
		case errors.Is(err, revision.ErrInheritanceCycle):
			a.writeError(w, http.StatusUnprocessableEntity, "inheritance_cycle", "inheritance chain contains a cycle")
		case errors.Is(err, revision.ErrInheritanceTooDeep):
			a.writeError(w, http.StatusUnprocessableEntity, "inheritance_too_deep", "inheritance chain exceeds the signed or implementation cap")
		case errors.Is(err, revision.ErrGameMismatch):
			a.writeError(w, http.StatusUnprocessableEntity, "game_mismatch", "parent and child game versions do not match")
		default:
			a.writeError(w, http.StatusInternalServerError, "parent_verification_failed", "stored parent could not be verified")
		}
		return
	}

	objects, err := a.expectedObjects(verified.Manifest())
	if err != nil {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_object_set", err.Error())
		return
	}
	pub := storage.RevisionPublication{
		RevisionID: verified.RevisionID(), ProfileID: verified.ProfileID(), Sequence: verified.Sequence(),
		Manifest: storage.ValidatedManifest{Bytes: verified.CanonicalManifest(), SHA256: verified.ManifestSHA256()},
		ExpectedObjects: objects,
	}
	if err := a.store.PublishSignedRevision(r.Context(), pub, canonicalEnvelope); err != nil {
		a.writeStoragePublishError(w, err)
		return
	}
	a.writePublished(w, http.StatusCreated, verified)
}

func (a *API) expectedObjects(manifest revision.Manifest) ([]storage.Object, error) {
	byHash := make(map[string]int64)
	add := func(ref revision.ObjectRef) error {
		if ref.Size > a.maxObjectBytes {
			return fmt.Errorf("object %s exceeds configured object limit", ref.SHA256)
		}
		if previous, ok := byHash[ref.SHA256]; ok && previous != ref.Size {
			return fmt.Errorf("object %s is referenced with conflicting sizes", ref.SHA256)
		}
		byHash[ref.SHA256] = ref.Size
		return nil
	}
	for _, entry := range manifest.Mods {
		if err := add(entry.Object); err != nil {
			return nil, err
		}
	}
	for _, entry := range manifest.Configs {
		if err := add(entry.Object); err != nil {
			return nil, err
		}
	}
	for _, entry := range manifest.Objects {
		if err := add(entry.Object); err != nil {
			return nil, err
		}
	}
	out := make([]storage.Object, 0, len(byHash))
	for digest, size := range byHash {
		out = append(out, storage.Object{SHA256: digest, Size: size})
	}
	return out, nil
}

func (a *API) validateParentChain(ctx context.Context, target *revision.VerifiedRevision) error {
	if target.Parent() == nil {
		return nil
	}
	parents := make(map[revision.RevisionKey]*revision.VerifiedRevision)
	seen := make(map[revision.RevisionKey]struct{})
	pin := target.Parent()
	for pin != nil {
		key := revision.RevisionKey{ProfileID: pin.ProfileID, RevisionID: pin.RevisionID}
		if _, ok := seen[key]; ok {
			return revision.ErrInheritanceCycle
		}
		seen[key] = struct{}{}
		stored, err := a.store.SignedRevision(ctx, pin.RevisionID)
		if err != nil {
			return err
		}
		parent, _, err := a.verifyStoredRevision(ctx, stored)
		if err != nil {
			return err
		}
		parents[key] = parent
		pin = parent.Parent()
		if len(parents) > revision.MaxInheritanceBases {
			return revision.ErrInheritanceTooDeep
		}
	}
	_, err := revision.ValidateInheritanceChain(target, parents, revision.MaxInheritanceBases)
	return err
}

func (a *API) handleRevision(w http.ResponseWriter, r *http.Request, profileID, revisionID string) {
	stored, err := a.store.SignedRevision(r.Context(), revisionID)
	if err != nil || stored.ProfileID != profileID {
		if err == nil || errors.Is(err, storage.ErrRevisionNotFound) {
			a.writeError(w, http.StatusNotFound, "not_found", "revision not found")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "storage_error", "revision unavailable")
		return
	}
	if _, canonicalEnvelope, err := a.verifyStoredRevision(r.Context(), stored); err != nil {
		a.writeError(w, http.StatusInternalServerError, "revision_corrupt", "stored revision failed integrity verification")
		return
	} else {
		a.writeJSON(w, http.StatusOK, struct {
			SchemaVersion   int             `json:"schema_version"`
			ProtocolVersion int             `json:"protocol_version"`
			Envelope        json.RawMessage `json:"envelope"`
		}{SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion, Envelope: canonicalEnvelope})
	}
}

func (a *API) handleProfiles(w http.ResponseWriter, r *http.Request, admin bool) {
	stored, err := a.store.ListSignedRevisions(r.Context(), a.listLimit)
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
			Name: ch.Channel,
			revisionRef: revisionRef{RevisionID: ch.RevisionID, Sequence: ch.Sequence, ManifestSHA256: ch.ManifestSHA256},
		})
	}

	views := make([]profileView, 0)
	index := make(map[string]int)
	for _, item := range stored {
		verified, _, err := a.verifyStoredRevision(r.Context(), item)
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "revision_corrupt", "stored revision failed integrity verification")
			return
		}
		i, exists := index[item.ProfileID]
		if !exists {
			manifest := verified.Manifest()
			i = len(views)
			index[item.ProfileID] = i
			views = append(views, profileView{
				ProfileID: item.ProfileID,
				Name: manifest.Profile.Name,
				LatestRevision: revisionRef{RevisionID: item.RevisionID, Sequence: item.Sequence, ManifestSHA256: item.ManifestSHA256},
				Channels: channelByProfile[item.ProfileID],
			})
		}
		if admin {
			views[i].PublishedCount++
		}
	}
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion   int           `json:"schema_version"`
		ProtocolVersion int           `json:"protocol_version"`
		Profiles        []profileView `json:"profiles"`
		Truncated       bool          `json:"truncated"`
	}{
		SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion,
		Profiles: views, Truncated: len(stored) == a.listLimit,
	})
}

type promoteRequest struct {
	RevisionID string `json:"revision_id"`
}

type rollbackRequest struct {
	RevisionID    string          `json:"revision_id"`
	RollbackEvent json.RawMessage `json:"rollback_event"`
}

func (a *API) handlePromote(w http.ResponseWriter, r *http.Request, profileID, channel string) {
	var req promoteRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	target, err := a.loadCandidate(r.Context(), profileID, channel, req.RevisionID)
	if err != nil {
		a.writeCandidateError(w, err)
		return
	}
	knownRecord, knownState, err := a.loadChannel(r.Context(), profileID, channel)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "channel state unavailable")
		return
	}
	if err := revision.DecideChannelTransition(knownState, target, nil); err != nil {
		if errors.Is(err, revision.ErrRollbackRequired) {
			a.writeError(w, http.StatusConflict, "rollback_required", "downgrade requires a valid signed rollback event")
		} else {
			a.writeError(w, http.StatusConflict, "channel_conflict", "channel transition is ambiguous")
		}
		return
	}
	candidate := channelRecord(target)
	if err := a.store.CompareAndSetChannel(r.Context(), knownRecord, candidate); err != nil {
		if errors.Is(err, storage.ErrChannelConflict) {
			a.writeError(w, http.StatusConflict, "channel_conflict", "channel state changed concurrently")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "storage_error", "channel could not be updated")
		return
	}
	a.writeChannel(w, candidate)
}

func (a *API) handleRollback(w http.ResponseWriter, r *http.Request, profileID, channel string) {
	var req rollbackRequest
	if err := a.decodeJSON(w, r, &req); err != nil {
		a.writeBodyReadError(w, err)
		return
	}
	if len(req.RollbackEvent) == 0 {
		a.writeError(w, http.StatusBadRequest, "invalid_request", "rollback_event is required")
		return
	}
	target, err := a.loadCandidate(r.Context(), profileID, channel, req.RevisionID)
	if err != nil {
		a.writeCandidateError(w, err)
		return
	}
	knownRecord, knownState, err := a.loadChannel(r.Context(), profileID, channel)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "storage_error", "channel state unavailable")
		return
	}
	if knownState == nil {
		a.writeError(w, http.StatusConflict, "channel_not_found", "rollback requires an existing channel head")
		return
	}

	keyID, err := signatureKeyID(req.RollbackEvent)
	if err != nil {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_rollback", "rollback event is malformed")
		return
	}
	pub, err := a.keys.PublicKey(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			a.writeError(w, http.StatusUnprocessableEntity, "unknown_release_key", "rollback signature key is not trusted")
		} else {
			a.writeError(w, http.StatusInternalServerError, "key_resolver_error", "release key lookup failed")
		}
		return
	}
	event, err := revision.ParseAndVerifyRollbackEvent(req.RollbackEvent, pub)
	if err != nil {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_rollback", "rollback signature or statement is invalid")
		return
	}
	if err := revision.DecideChannelTransition(knownState, target, event); err != nil {
		switch {
		case errors.Is(err, revision.ErrRollbackRequired):
			a.writeError(w, http.StatusConflict, "rollback_required", "downgrade requires a valid signed rollback event")
		default:
			a.writeError(w, http.StatusConflict, "channel_conflict", "rollback statement does not bind the current and target channel heads")
		}
		return
	}
	candidate := channelRecord(target)
	if err := a.store.CompareAndSetChannel(r.Context(), knownRecord, candidate); err != nil {
		if errors.Is(err, storage.ErrChannelConflict) {
			a.writeError(w, http.StatusConflict, "channel_conflict", "channel state changed concurrently")
			return
		}
		a.writeError(w, http.StatusInternalServerError, "storage_error", "channel could not be updated")
		return
	}
	a.writeChannel(w, candidate)
}

func (a *API) loadCandidate(ctx context.Context, profileID, channel, revisionID string) (revision.ChannelState, error) {
	if revisionID == "" {
		return revision.ChannelState{}, storage.ErrRevisionNotFound
	}
	stored, err := a.store.SignedRevision(ctx, revisionID)
	if err != nil {
		return revision.ChannelState{}, err
	}
	verified, _, err := a.verifyStoredRevision(ctx, stored)
	if err != nil {
		return revision.ChannelState{}, err
	}
	if verified.ProfileID() != profileID {
		return revision.ChannelState{}, storage.ErrRevisionNotFound
	}
	return revision.ChannelState{
		ProfileID: profileID, Channel: channel, RevisionID: verified.RevisionID(),
		Sequence: verified.Sequence(), ManifestSHA256: verified.ManifestSHA256(),
	}, nil
}

func (a *API) loadChannel(ctx context.Context, profileID, channel string) (*storage.ChannelRecord, *revision.ChannelState, error) {
	record, err := a.store.Channel(ctx, profileID, channel)
	if errors.Is(err, storage.ErrRevisionNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	state := revision.ChannelState{
		ProfileID: record.ProfileID, Channel: record.Channel, RevisionID: record.RevisionID,
		Sequence: record.Sequence, ManifestSHA256: record.ManifestSHA256,
	}
	return &record, &state, nil
}

func channelRecord(state revision.ChannelState) storage.ChannelRecord {
	return storage.ChannelRecord{
		ProfileID: state.ProfileID, Channel: state.Channel, RevisionID: state.RevisionID,
		Sequence: state.Sequence, ManifestSHA256: state.ManifestSHA256,
	}
}

func (a *API) verifyRevisionEnvelope(ctx context.Context, raw []byte) (*revision.VerifiedRevision, []byte, error) {
	keyID, err := signatureKeyID(raw)
	if err != nil {
		return nil, nil, revision.ErrInvalidEnvelope
	}
	pub, err := a.keys.PublicKey(ctx, keyID)
	if err != nil {
		return nil, nil, err
	}
	verified, err := revision.ParseAndVerifyRevisionEnvelope(raw, pub)
	if err != nil {
		return nil, nil, err
	}
	canonicalEnvelope, err := revision.CanonicalizeJSON(raw)
	if err != nil {
		return nil, nil, revision.ErrInvalidEnvelope
	}
	return verified, canonicalEnvelope, nil
}

func (a *API) verifyStoredRevision(ctx context.Context, stored storage.StoredSignedRevision) (*revision.VerifiedRevision, []byte, error) {
	verified, canonicalEnvelope, err := a.verifyRevisionEnvelope(ctx, stored.Envelope)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrStoredRevisionCorrupt, err)
	}
	if verified.RevisionID() != stored.RevisionID || verified.ProfileID() != stored.ProfileID ||
		verified.Sequence() != stored.Sequence || verified.ManifestSHA256() != stored.ManifestSHA256 ||
		!bytes.Equal(verified.CanonicalManifest(), stored.Manifest) {
		return nil, nil, ErrStoredRevisionCorrupt
	}
	return verified, canonicalEnvelope, nil
}

func signatureKeyID(raw []byte) (string, error) {
	var peek struct {
		Signature struct {
			KeyID string `json:"key_id"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return "", err
	}
	if len(peek.Signature.KeyID) < 1 || len(peek.Signature.KeyID) > 128 {
		return "", errors.New("invalid key id")
	}
	return peek.Signature.KeyID, nil
}

func (a *API) writeRevisionVerifyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		a.writeError(w, http.StatusUnprocessableEntity, "unknown_release_key", "revision signature key is not trusted")
	case errors.Is(err, revision.ErrInvalidSignature):
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_signature", "revision signature is invalid")
	case errors.Is(err, revision.ErrDigestMismatch):
		a.writeError(w, http.StatusUnprocessableEntity, "manifest_digest_mismatch", "manifest digest does not match canonical manifest")
	case errors.Is(err, revision.ErrInvalidManifest), errors.Is(err, revision.ErrInvalidEnvelope), errors.Is(err, revision.ErrInvalidJSON):
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_envelope", "signed revision envelope is invalid")
	default:
		a.writeError(w, http.StatusInternalServerError, "key_resolver_error", "release key lookup failed")
	}
}

func (a *API) writeStoragePublishError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrObjectMissing):
		a.writeError(w, http.StatusConflict, "object_missing", "one or more referenced objects have not been staged")
	case errors.Is(err, storage.ErrCorruptObject), errors.Is(err, storage.ErrDigestMismatch), errors.Is(err, storage.ErrSizeMismatch):
		a.writeError(w, http.StatusUnprocessableEntity, "object_verification_failed", "referenced object failed size or hash verification")
	case errors.Is(err, storage.ErrImmutableConflict):
		a.writeError(w, http.StatusConflict, "immutable_conflict", "revision identity or sequence conflicts with immutable history")
	default:
		a.writeError(w, http.StatusInternalServerError, "storage_error", "revision could not be published")
	}
}

func (a *API) writeCandidateError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrRevisionNotFound) {
		a.writeError(w, http.StatusNotFound, "not_found", "target revision not found")
		return
	}
	if errors.Is(err, ErrStoredRevisionCorrupt) {
		a.writeError(w, http.StatusInternalServerError, "revision_corrupt", "stored target revision failed integrity verification")
		return
	}
	a.writeError(w, http.StatusInternalServerError, "storage_error", "target revision unavailable")
}

func (a *API) writePublished(w http.ResponseWriter, status int, verified *revision.VerifiedRevision) {
	a.writeJSON(w, status, struct {
		SchemaVersion   int         `json:"schema_version"`
		ProtocolVersion int         `json:"protocol_version"`
		Revision        revisionRef `json:"revision"`
	}{
		SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion,
		Revision: revisionRef{RevisionID: verified.RevisionID(), Sequence: verified.Sequence(), ManifestSHA256: verified.ManifestSHA256()},
	})
}

func (a *API) writeChannel(w http.ResponseWriter, record storage.ChannelRecord) {
	a.writeJSON(w, http.StatusOK, struct {
		SchemaVersion   int         `json:"schema_version"`
		ProtocolVersion int         `json:"protocol_version"`
		ProfileID       string      `json:"profile_id"`
		Channel         channelView `json:"channel"`
	}{
		SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion, ProfileID: record.ProfileID,
		Channel: channelView{Name: record.Channel, revisionRef: revisionRef{
			RevisionID: record.RevisionID, Sequence: record.Sequence, ManifestSHA256: record.ManifestSHA256,
		}},
	})
}

func (a *API) readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxJSONBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty body")
	}
	return raw, nil
}

func (a *API) decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) error {
	raw, err := a.readJSONBody(w, r)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (a *API) writeBodyReadError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		a.writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "JSON payload exceeds configured limit")
		return
	}
	a.writeError(w, http.StatusBadRequest, "invalid_request", "request body is invalid JSON")
}

func (a *API) methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	a.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func (a *API) writeError(w http.ResponseWriter, status int, code, message string) {
	a.writeJSON(w, status, errorBody{
		SchemaVersion: SchemaVersion, ProtocolVersion: ProtocolVersion,
		Error: apiError{Code: code, Message: message},
	})
}

func (a *API) writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-BootOptim-Protocol-Version", strconv.Itoa(ProtocolVersion))
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func pathParts(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
