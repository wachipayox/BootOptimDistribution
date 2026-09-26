package profileapi

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

type testKeys map[string]ed25519.PublicKey

func (k testKeys) PublicKey(_ context.Context, id string) (ed25519.PublicKey, error) {
	key, ok := k[id]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return key, nil
}

func identity(next http.Handler) http.Handler { return next }

func TestSyntheticPublicationReadPromotionAndRollback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cas, err := storage.OpenCAS(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenSQLite(root+"/metadata.sqlite3", cas)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pub, priv := testKeyPair()
	handler, err := New(Dependencies{
		Objects: cas,
		Store:   store,
		Keys:    testKeys{"test-key": pub},
	}, Options{
		ReadMiddleware:  identity,
		AdminMiddleware: identity,
		MaxObjectBytes:  1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	objectBytes := []byte("synthetic mod bytes")
	objectDigest := sha256.Sum256(objectBytes)
	objectHex := hex.EncodeToString(objectDigest[:])
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/objects/sha256/"+objectHex, strings.NewReader(string(objectBytes)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stage object status=%d body=%s", rec.Code, rec.Body.String())
	}

	firstManifest := testManifest("rev_0000000000000001", 1, objectHex, int64(len(objectBytes)))
	firstEnvelope := signEnvelope(t, firstManifest, priv)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/revisions", strings.NewReader(string(firstEnvelope)))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish first status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/profiles", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "profile_root") {
		t.Fatalf("profiles status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/profiles/profile_root/revisions/rev_0000000000000001", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "manifest_sha256") {
		t.Fatalf("revision status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/objects/sha256/"+objectHex, nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != string(objectBytes) {
		t.Fatalf("object status=%d body=%q", rec.Code, rec.Body.String())
	}

	secondManifest := testManifest("rev_0000000000000002", 2, objectHex, int64(len(objectBytes)))
	secondEnvelope := signEnvelope(t, secondManifest, priv)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/revisions", strings.NewReader(string(secondEnvelope)))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish second status=%d body=%s", rec.Code, rec.Body.String())
	}

	for _, revisionID := range []string{"rev_0000000000000001", "rev_0000000000000002"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/v1/admin/profiles/profile_root/channels/stable/promote",
			strings.NewReader(`{"revision_id":"`+revisionID+`"}`))
		handler.ServeHTTP(rec, req)
		if revisionID == "rev_0000000000000001" && rec.Code != http.StatusOK {
			t.Fatalf("initial promotion status=%d body=%s", rec.Code, rec.Body.String())
		}
		if revisionID == "rev_0000000000000002" && rec.Code != http.StatusOK {
			t.Fatalf("second promotion status=%d body=%s", rec.Code, rec.Body.String())
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/profiles/profile_root/channels/stable/promote",
		strings.NewReader(`{"revision_id":"rev_0000000000000001"}`))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "rollback_required") {
		t.Fatalf("unsigned downgrade status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := signRollback(t, revision.RollbackStatement{
		ProfileID: "profile_root", Channel: "stable",
		FromRevisionID: "rev_0000000000000002", FromSequence: 2,
		ToRevisionID: "rev_0000000000000001",
		Reason: "synthetic rollback", IssuedAt: "2026-09-26T12:00:00Z",
	}, priv)
	rollbackBody, err := json.Marshal(map[string]interface{}{
		"revision_id":    "rev_0000000000000001",
		"rollback_event": json.RawMessage(event),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/profiles/profile_root/channels/stable/rollback",
		strings.NewReader(string(rollbackBody)))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rec.Code, rec.Body.String())
	}

	channel, err := store.Channel(ctx, "profile_root", "stable")
	if err != nil {
		t.Fatal(err)
	}
	if channel.RevisionID != "rev_0000000000000001" || channel.Sequence != 1 {
		t.Fatalf("channel after rollback=%+v", channel)
	}
}

func TestInvalidSignatureAndPayloadLimitStayInvisible(t *testing.T) {
	root := t.TempDir()
	cas, err := storage.OpenCAS(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenSQLite(root+"/metadata.sqlite3", cas)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pub, priv := testKeyPair()
	handler, err := New(Dependencies{Objects: cas, Store: store, Keys: testKeys{"test-key": pub}}, Options{
		ReadMiddleware: identity, AdminMiddleware: identity, MaxJSONBytes: 2048, MaxObjectBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	manifest := testManifest("rev_0000000000000003", 3, strings.Repeat("a", 64), 1)
	envelope := signEnvelope(t, manifest, priv)
	var wire map[string]interface{}
	if err := json.Unmarshal(envelope, &wire); err != nil {
		t.Fatal(err)
	}
	wire["signature"].(map[string]interface{})["value"] =
		base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	bad, _ := json.Marshal(wire)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/revisions", strings.NewReader(string(bad)))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "invalid_signature") {
		t.Fatalf("bad signature status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := store.Revision(context.Background(), "rev_0000000000000003"); !errors.Is(err, storage.ErrRevisionNotFound) {
		t.Fatalf("invalid revision visible: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/revisions", strings.NewReader(strings.Repeat("x", 4096)))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "payload_too_large") {
		t.Fatalf("oversize status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNewRequiresAuthHooks(t *testing.T) {
	_, err := New(Dependencies{}, Options{})
	if err == nil {
		t.Fatal("missing dependencies unexpectedly accepted")
	}
}

func testKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := sha256.Sum256([]byte("profileapi relay 211 deterministic test key only"))
	private := ed25519.NewKeyFromSeed(seed[:])
	return private.Public().(ed25519.PublicKey), private
}

func testManifest(revisionID string, sequence int64, digest string, size int64) []byte {
	manifest := revision.Manifest{
		SchemaVersion: 1,
		Revision: revision.Revision{
			ID: revisionID, Sequence: sequence, CreatedAt: "2026-09-26T12:00:00Z",
		},
		Profile: revision.Profile{ID: "profile_root", Name: "Synthetic root", Official: true},
		Game:    revision.Game{Minecraft: "1.21.1", NeoForge: "21.1.0"},
		Base:    nil,
		Permissions: revision.Permissions{
			DeriveLocal: true,
			Mods: revision.ModPermissions{Add: true, Remove: true},
			Configs: revision.ConfigPermissions{OverrideEnforced: false, OverrideDefaultOnce: true},
			MaxInheritanceDepth: 8,
		},
		Mods: []revision.ModEntry{{
			ID: "mod_synthetic", Path: "mods/synthetic.jar",
			Object: revision.ObjectRef{SHA256: digest, Size: size, MediaType: "application/java-archive"},
		}},
		RemoveMods: []revision.RemoveMod{}, Configs: []revision.ConfigEntry{},
		RemoveConfigs: []revision.RemoveConfig{}, Objects: []revision.ObjectEntry{},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	return raw
}

func signEnvelope(t *testing.T, manifest []byte, private ed25519.PrivateKey) []byte {
	t.Helper()
	canonical, err := jcs.Transform(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	envelope := revision.SignedRevisionEnvelope{
		Canonicalization: revision.CanonicalizationRFC8785,
		ManifestSHA256:   hex.EncodeToString(digest[:]),
		Manifest:         manifest,
		Signature: revision.Signature{
			KeyID: "test-key", Algorithm: revision.SignatureAlgorithm,
			Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, canonical)),
		},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signRollback(t *testing.T, statement revision.RollbackStatement, private ed25519.PrivateKey) []byte {
	t.Helper()
	statementRaw, err := json.Marshal(statement)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(statementRaw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	event := revision.RollbackEvent{
		EventID: "rollback-relay211-0001",
		StatementSHA256: hex.EncodeToString(digest[:]),
		Statement: statementRaw,
		Signature: revision.Signature{
			KeyID: "test-key", Algorithm: revision.SignatureAlgorithm,
			Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, canonical)),
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
