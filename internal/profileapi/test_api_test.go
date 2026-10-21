package profileapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/releasesigning"
	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
	"github.com/wachipayox/BootOptimDistribution/internal/testauth"
)

type testEnv struct {
	t       *testing.T
	handler http.Handler
	store   *storage.SQLiteStore
	signer  *releasesigning.Signer
	tokens  *testauth.Manager
	cas     *storage.CAS
	token   string
	free    uint64
}

func newTestEnv(t *testing.T, quotas TestQuotas) *testEnv {
	t.Helper()
	root := t.TempDir()
	cas, err := storage.OpenCAS(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenSQLite(root+"/metadata.sqlite3", cas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	signer, err := releasesigning.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := testauth.New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{t: t, store: store, signer: signer, tokens: tokens, cas: cas, free: 500 << 30}
	env.handler, err = New(Dependencies{
		Objects: cas, Store: store, Keys: testKeys{signer.KeyID(): signer.PublicKey()}, Signer: signer,
		TestTokens: tokens, DiskUsage: func() (uint64, uint64, error) { return env.free, 1000 << 30, nil },
	}, Options{ReadMiddleware: identity, AdminMiddleware: identity, TestMiddleware: tokens.Middleware, TestQuotas: quotas, MaxObjectBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func (e *testEnv) do(method, path, token, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Wbombo-Agent", "agente-test")
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) admin(method, path, body string) *httptest.ResponseRecorder {
	return e.do(method, path, "", body)
}

func (e *testEnv) test(method, path, body string) *httptest.ResponseRecorder {
	return e.do(method, path, e.token, body)
}

func (e *testEnv) rotate() {
	e.t.Helper()
	rec := e.admin("POST", "/v1/admin/test-api/token", "")
	if rec.Code != 200 {
		e.t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	e.token = out.Token
}

func (e *testEnv) stageAdmin(content string) (string, int64) {
	e.t.Helper()
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	if rec := e.admin("POST", "/v1/admin/objects/sha256/"+digest, content); rec.Code != 200 {
		e.t.Fatal(rec.Body.String())
	}
	return digest, int64(len(content))
}

func (e *testEnv) stageTest(content string) (string, int64) {
	e.t.Helper()
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	if rec := e.test("POST", "/v1/test/objects/sha256/"+digest, content); rec.Code != 200 {
		e.t.Fatalf("test upload: %d %s", rec.Code, rec.Body.String())
	}
	return digest, int64(len(content))
}

func (e *testEnv) publishAdmin(m revision.Manifest) published {
	e.t.Helper()
	raw, _ := json.Marshal(m)
	body, _ := json.Marshal(map[string]any{"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": manifestDigest(e.t, raw), "manifest": json.RawMessage(raw)})
	rec := e.admin("POST", "/v1/admin/publications", string(body))
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("admin publish: %d %s", rec.Code, rec.Body.String())
	}
	var out published
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func (e *testEnv) head(profileID string) storage.StoredRevision {
	e.t.Helper()
	head, err := e.store.ProfileHead(context.Background(), profileID)
	if err != nil {
		e.t.Fatalf("head %s: %v", profileID, err)
	}
	return head
}

func (e *testEnv) manifest(profileID string) revision.Manifest {
	e.t.Helper()
	var m revision.Manifest
	if err := json.Unmarshal(e.head(profileID).Manifest, &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

type published struct {
	Revision   revisionRef    `json:"revision"`
	AutoFollow []followResult `json:"auto_follow"`
}

func decodePublished(t *testing.T, rec *httptest.ResponseRecorder, want int) testPublished {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("got %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
	var out testPublished
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func errorCode(rec *httptest.ResponseRecorder) string {
	var body errorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

// seedNormal publishes profile_root (rev 1) and its normal child profile_child.
func (e *testEnv) seedNormal() (published, published) {
	digest, size := e.stageAdmin("root mod bytes")
	var root revision.Manifest
	_ = json.Unmarshal(testManifest("rev_0000000000000001", 1, digest, size), &root)
	r1 := e.publishAdmin(root)
	pin := revision.ParentRef{ProfileID: "profile_root", RevisionID: r1.Revision.RevisionID, ManifestSHA256: r1.Revision.ManifestSHA256}
	var child revision.Manifest
	_ = json.Unmarshal(testChildManifest("profile_child", "rev_00000000000000c1", pin.ManifestSHA256), &child)
	child.Base = &pin
	c1 := e.publishAdmin(child)
	return r1, c1
}

func TestTestAPITokenLifecycle(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	if rec := env.do("GET", "/v1/test/profiles", "anything", ""); rec.Code != 503 || errorCode(rec) != "test_token_not_configured" {
		t.Fatalf("before generation: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.admin("POST", "/v1/test/profiles", `{}`); rec.Code != 503 {
		t.Fatalf("write before generation: %d", rec.Code)
	}
	env.rotate()
	first := env.token
	if !strings.HasPrefix(first, "wbt_") {
		t.Fatalf("token format: %q", first)
	}
	if rec := env.do("GET", "/v1/test/profiles", "", ""); rec.Code != 401 {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := env.do("GET", "/v1/test/profiles", "wbt_wrong", ""); rec.Code != 401 {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	if rec := env.test("GET", "/v1/test/profiles", ""); rec.Code != 200 {
		t.Fatalf("valid token: %d %s", rec.Code, rec.Body.String())
	}
	env.rotate()
	if rec := env.do("GET", "/v1/test/profiles", first, ""); rec.Code != 401 {
		t.Fatalf("token from before the rotation must fail: %d", rec.Code)
	}
	if rec := env.test("GET", "/v1/test/profiles", ""); rec.Code != 200 {
		t.Fatalf("rotated token: %d", rec.Code)
	}
	// The verifier survives a restart (a new manager on the same store).
	reloaded, err := testauth.New(context.Background(), env.store)
	if err != nil || !reloaded.Status().Configured {
		t.Fatalf("reload: %v %+v", err, reloaded.Status())
	}
	// The plaintext is never shown again.
	status := env.admin("GET", "/v1/admin/test-api", "")
	if status.Code != 200 || strings.Contains(status.Body.String(), env.token) || !strings.Contains(status.Body.String(), `"configured":true`) {
		t.Fatalf("status leaks or misses token state: %s", status.Body.String())
	}
	// Repeated failures from one address are throttled, even with the right token afterwards.
	for i := 0; i < 20; i++ {
		env.do("GET", "/v1/test/usage", "wbt_guess", "")
	}
	if rec := env.test("GET", "/v1/test/usage", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", rec.Code)
	}
}

func TestTestProfilesNeverWriteNormalProfiles(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	env.seedNormal()
	rootBefore, childBefore := env.head("profile_root"), env.head("profile_child")

	for _, c := range []struct{ method, path, body string }{
		{"POST", "/v1/test/profiles/profile_root/changes", `{"notes":"x"}`},
		{"POST", "/v1/test/profiles/profile_child/repin", `{"parent_revision_id":"latest"}`},
		{"PUT", "/v1/test/profiles/profile_child/follow", `{"follow_parent":false}`},
		{"PUT", "/v1/test/profiles/profile_root/presentation", `{"name":"x"}`},
		{"DELETE", "/v1/test/profiles/profile_root", `{"confirm_profile_id":"profile_root"}`},
		{"POST", "/v1/test/profiles/profile_root/channels/stable/promote", `{"revision_id":"rev_0000000000000001"}`},
		{"POST", "/v1/test/profiles/profile_root/channels/stable/rollback", `{"revision_id":"rev_0000000000000001"}`},
		// The prefix alone is not enough: the id must be registered by the test API.
		{"POST", "/v1/test/profiles/profile_test-ghost/changes", `{"notes":"x"}`},
	} {
		rec := env.test(c.method, c.path, c.body)
		if rec.Code != http.StatusForbidden || errorCode(rec) != "not_a_test_profile" {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	if env.head("profile_root").RevisionID != rootBefore.RevisionID || env.head("profile_child").RevisionID != childBefore.RevisionID {
		t.Fatal("a normal profile changed")
	}

	// The admin API cannot create test ids nor derive a normal profile from a test profile.
	rec := env.test("POST", "/v1/test/profiles", `{"slug":"a","parent_profile_id":"profile_root"}`)
	decodePublished(t, rec, http.StatusCreated)
	var spoof revision.Manifest
	_ = json.Unmarshal(testChildManifest("profile_test-admin", "rev_00000000000000a1", rootBefore.ManifestSHA256), &spoof)
	spoof.Base = &revision.ParentRef{ProfileID: "profile_root", RevisionID: rootBefore.RevisionID, ManifestSHA256: rootBefore.ManifestSHA256}
	raw, _ := json.Marshal(spoof)
	body, _ := json.Marshal(map[string]any{"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": manifestDigest(t, raw), "manifest": json.RawMessage(raw)})
	if rec := env.admin("POST", "/v1/admin/publications", string(body)); rec.Code != 403 || errorCode(rec) != "test_profile_admin_write" {
		t.Fatalf("admin created a test id: %d %s", rec.Code, rec.Body.String())
	}
	testHead := env.head("profile_test-a")
	var derived revision.Manifest
	_ = json.Unmarshal(testChildManifest("profile_from_test", "rev_00000000000000d1", testHead.ManifestSHA256), &derived)
	derived.Base = &revision.ParentRef{ProfileID: "profile_test-a", RevisionID: testHead.RevisionID, ManifestSHA256: testHead.ManifestSHA256}
	raw, _ = json.Marshal(derived)
	body, _ = json.Marshal(map[string]any{"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": manifestDigest(t, raw), "manifest": json.RawMessage(raw)})
	if rec := env.admin("POST", "/v1/admin/publications", string(body)); rec.Code != 403 || errorCode(rec) != "test_parent_not_allowed" {
		t.Fatalf("admin derived from a test profile: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.admin("POST", "/v1/admin/profiles/profile_test-a/channels/stable/promote", `{"revision_id":"`+testHead.RevisionID+`"}`); rec.Code != 403 {
		t.Fatalf("admin promote of a test profile: %d", rec.Code)
	}

	// The public catalog hides test profiles unless asked; exact reads stay public.
	if rec := env.admin("GET", "/v1/profiles", ""); strings.Contains(rec.Body.String(), "profile_test-a") {
		t.Fatal("catalog shows a test profile")
	}
	if rec := env.admin("GET", "/v1/profiles?include_test=1", ""); !strings.Contains(rec.Body.String(), "profile_test-a") {
		t.Fatal("include_test=1 misses the test profile")
	}
	if rec := env.admin("GET", "/v1/profiles/profile_test-a/revisions/"+testHead.RevisionID, ""); rec.Code != 200 {
		t.Fatalf("exact test revision read: %d", rec.Code)
	}

	// The admin may delete test profiles.
	if rec := env.admin("DELETE", "/v1/admin/profiles/profile_test-a", `{"confirm_profile_id":"profile_test-a"}`); rec.Code != 200 {
		t.Fatalf("admin delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTestChildrenFollowOrStayPinned(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	r1, _ := env.seedNormal()

	// a: test child of the normal root, following. b: test grandchild pinned. c: test grandchild following.
	a := decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"a","name":"Prueba A","parent_profile_id":"profile_root"}`), 201)
	if a.Stable != "promoted" || !a.FollowParent || a.ProfileID != "profile_test-a" {
		t.Fatalf("create: %+v", a)
	}
	b := decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"b","parent_profile_id":"profile_test-a","follow_parent":false}`), 201)
	c := decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"c","parent_profile_id":"profile_test-a","parent_revision_id":"latest"}`), 201)
	if b.FollowParent || !c.FollowParent {
		t.Fatalf("follow flags: b=%v c=%v", b.FollowParent, c.FollowParent)
	}
	if got := env.manifest("profile_test-a"); got.Base.RevisionID != r1.Revision.RevisionID || got.Game != env.manifest("profile_root").Game {
		t.Fatalf("test child pin/game: %+v", got)
	}

	// A change on the test profile: add a mod uploaded through the test API.
	digest, size := env.stageTest("agent mod bytes")
	change := decodePublished(t, env.test("POST", "/v1/test/profiles/profile_test-a/changes",
		`{"notes":"prueba de mod","operations":[{"op":"add_mod","name":"agent.jar","sha256":"`+digest+`","size":`+jsonInt(size)+`}]}`), 201)
	if change.Revision.Sequence != 2 || change.Stable != "promoted" {
		t.Fatalf("change: %+v", change)
	}
	// Its following child c was re-pinned; pinned b was not.
	followed := map[string]bool{}
	for _, f := range change.AutoFollow {
		followed[f.ProfileID] = f.Status == "published"
	}
	if !followed["profile_test-c"] || followed["profile_test-b"] {
		t.Fatalf("auto-follow from the test change: %+v", change.AutoFollow)
	}

	// The test revision verifies with the existing envelope verification.
	rec := env.admin("GET", "/v1/profiles/profile_test-a/revisions/"+change.Revision.RevisionID, "")
	var read struct {
		Envelope json.RawMessage `json:"envelope"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &read)
	verified, err := revision.ParseAndVerifyRevisionEnvelope(read.Envelope, env.signer.PublicKey())
	if err != nil || verified.ProfileID() != "profile_test-a" || len(verified.Manifest().Mods) != 1 {
		t.Fatalf("signature verification: %v", err)
	}

	// Publishing the normal root re-pins the normal child and following test descendants, never pinned ones.
	childBefore := env.head("profile_child")
	aBefore, bBefore, cBefore := env.head("profile_test-a"), env.head("profile_test-b"), env.head("profile_test-c")
	digest2, size2 := env.stageAdmin("root v2 bytes")
	var rootV2 revision.Manifest
	_ = json.Unmarshal(testManifest("rev_0000000000000002", 2, digest2, size2), &rootV2)
	r2 := env.publishAdmin(rootV2)
	statuses := map[string]string{}
	for _, f := range r2.AutoFollow {
		statuses[f.ProfileID] = f.Status
	}
	if statuses["profile_child"] != "published" || statuses["profile_test-a"] != "published" || statuses["profile_test-c"] != "published" {
		t.Fatalf("followers: %+v", r2.AutoFollow)
	}
	if _, listed := statuses["profile_test-b"]; listed {
		t.Fatalf("pinned test profile was touched: %+v", r2.AutoFollow)
	}
	if env.head("profile_test-b").RevisionID != bBefore.RevisionID {
		t.Fatal("pinned test grandchild moved")
	}
	if env.head("profile_child").Sequence != childBefore.Sequence+1 || env.head("profile_test-a").Sequence != aBefore.Sequence+1 || env.head("profile_test-c").Sequence != cBefore.Sequence+1 {
		t.Fatal("following profiles were not re-pinned exactly once")
	}
	// The test child kept its own mod after following.
	if a := env.manifest("profile_test-a"); a.Base.RevisionID != r2.Revision.RevisionID || len(a.Mods) != 1 {
		t.Fatalf("followed test child: %+v", a)
	}

	// Repin to the old parent revision, then back to latest.
	old := decodePublished(t, env.test("POST", "/v1/test/profiles/profile_test-a/repin", `{"parent_revision_id":"`+r1.Revision.RevisionID+`"}`), 201)
	if env.manifest("profile_test-a").Base.RevisionID != r1.Revision.RevisionID || old.Revision.Sequence != env.head("profile_test-a").Sequence {
		t.Fatalf("repin to old: %+v", old)
	}
	latest := decodePublished(t, env.test("POST", "/v1/test/profiles/profile_test-a/repin", `{"parent_revision_id":"latest"}`), 201)
	if env.manifest("profile_test-a").Base.RevisionID != r2.Revision.RevisionID || latest.Revision.Sequence != old.Revision.Sequence+1 {
		t.Fatalf("repin to latest: %+v", latest)
	}
	if rec := env.test("POST", "/v1/test/profiles/profile_test-a/repin", `{"parent_revision_id":"latest"}`); rec.Code != 409 {
		t.Fatalf("repin to the current pin: %d", rec.Code)
	}

	// follow off: the next root publication leaves profile_test-a alone.
	if rec := env.test("PUT", "/v1/test/profiles/profile_test-a/follow", `{"follow_parent":false}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	aPinned := env.head("profile_test-a")
	digest3, size3 := env.stageAdmin("root v3 bytes")
	var rootV3 revision.Manifest
	_ = json.Unmarshal(testManifest("rev_0000000000000003", 3, digest3, size3), &rootV3)
	r3 := env.publishAdmin(rootV3)
	if env.head("profile_test-a").RevisionID != aPinned.RevisionID {
		t.Fatal("test profile with follow off moved")
	}
	for _, f := range r3.AutoFollow {
		if f.ProfileID != "profile_child" {
			t.Fatalf("unexpected follower after follow off: %+v", f)
		}
	}

	// Removing the agent's own mod and an inherited one in one revision.
	removed := decodePublished(t, env.test("POST", "/v1/test/profiles/profile_test-a/changes",
		`{"operations":[{"op":"remove_mod","name":"agent.jar"},{"op":"remove_mod","name":"synthetic.jar"}]}`), 201)
	m := env.manifest("profile_test-a")
	if len(m.Mods) != 0 || len(m.RemoveMods) != 1 || m.RemoveMods[0].ID != "mod_synthetic" || removed.Revision.Sequence != aPinned.Sequence+1 {
		t.Fatalf("remove: %+v", m)
	}
}

func TestTestProfileParentPermissionsAndValidation(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	digest, size := env.stageAdmin("strict root")
	var root revision.Manifest
	_ = json.Unmarshal(testManifest("rev_0000000000000001", 1, digest, size), &root)
	root.Permissions.Mods = revision.ModPermissions{Add: false, Remove: false}
	root.Profile.ID = "profile_strict"
	env.publishAdmin(root)
	decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"strict","parent_profile_id":"profile_strict"}`), 201)
	if got := env.manifest("profile_test-strict").Permissions.Mods; got.Add || got.Remove {
		t.Fatalf("test child is more permissive than its parent: %+v", got)
	}
	modDigest, modSize := env.stageTest("blocked mod")
	rec := env.test("POST", "/v1/test/profiles/profile_test-strict/changes",
		`{"operations":[{"op":"add_mod","name":"x.jar","sha256":"`+modDigest+`","size":`+jsonInt(modSize)+`}]}`)
	if rec.Code != 403 || errorCode(rec) != "parent_permission_denied" {
		t.Fatalf("add against parent permissions: %d %s", rec.Code, rec.Body.String())
	}
	rec = env.test("POST", "/v1/test/profiles/profile_test-strict/changes", `{"operations":[{"op":"remove_mod","name":"synthetic.jar"}]}`)
	if rec.Code != 403 || errorCode(rec) != "parent_permission_denied" {
		t.Fatalf("remove against parent permissions: %d %s", rec.Code, rec.Body.String())
	}
	// Normal validation still applies: a config setting needs its config.
	rec = env.test("POST", "/v1/test/profiles/profile_test-strict/changes",
		`{"operations":[{"op":"set_config_setting","path":"config/missing.toml","format":"toml","key":"a.b","value":true}]}`)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("invalid setting accepted: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.test("POST", "/v1/test/profiles", `{"slug":"Bad Slug","parent_profile_id":"profile_strict"}`); rec.Code != 400 {
		t.Fatalf("bad slug: %d", rec.Code)
	}
	if rec := env.test("POST", "/v1/test/profiles", `{"slug":"orphan","parent_profile_id":"profile_missing"}`); rec.Code != 404 {
		t.Fatalf("missing parent: %d", rec.Code)
	}
}

func TestTestProfileDeletionBurnsID(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	env.seedNormal()
	created := decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"gone","parent_profile_id":"profile_child"}`), 201)
	if rec := env.test("DELETE", "/v1/test/profiles/profile_test-gone", `{"confirm_profile_id":"wrong"}`); rec.Code != 400 {
		t.Fatalf("delete without exact confirmation: %d", rec.Code)
	}
	if rec := env.test("DELETE", "/v1/test/profiles/profile_test-gone", `{"confirm_profile_id":"profile_test-gone"}`); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.test("POST", "/v1/test/profiles/profile_test-gone/changes", `{"notes":"x"}`); rec.Code != 404 {
		t.Fatalf("write after delete: %d", rec.Code)
	}
	if rec := env.test("POST", "/v1/test/profiles", `{"slug":"gone","parent_profile_id":"profile_child"}`); rec.Code != 409 || errorCode(rec) != "profile_id_taken" {
		t.Fatalf("burned id reused: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.admin("GET", "/v1/profiles/profile_test-gone/revisions/"+created.Revision.RevisionID, ""); rec.Code != 200 {
		t.Fatalf("tombstoned revision must stay readable by id: %d", rec.Code)
	}
	if rec := env.test("GET", "/v1/test/profiles", ""); strings.Contains(rec.Body.String(), "profile_test-gone") {
		t.Fatal("deleted profile listed as live")
	}
}

func TestTestQuotas(t *testing.T) {
	env := newTestEnv(t, TestQuotas{MaxProfiles: 2, MaxObjectBytes: 64, MaxBytes: 100, DiskMarginBytes: 1 << 30})
	env.rotate()
	env.seedNormal()
	decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"q1","parent_profile_id":"profile_root"}`), 201)
	decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"q2","parent_profile_id":"profile_root"}`), 201)
	if rec := env.test("POST", "/v1/test/profiles", `{"slug":"q3","parent_profile_id":"profile_root"}`); rec.Code != 409 || errorCode(rec) != "quota_profiles_exceeded" {
		t.Fatalf("profile quota: %d %s", rec.Code, rec.Body.String())
	}
	big := strings.Repeat("x", 65)
	sum := sha256.Sum256([]byte(big))
	if rec := env.test("POST", "/v1/test/objects/sha256/"+hex.EncodeToString(sum[:]), big); rec.Code != 413 || errorCode(rec) != "quota_object_too_large" {
		t.Fatalf("object quota: %d %s", rec.Code, rec.Body.String())
	}
	env.stageTest(strings.Repeat("a", 60))
	// Re-uploading bytes already in CAS costs nothing.
	env.stageTest(strings.Repeat("a", 60))
	env.stageTest("root mod bytes")
	over := strings.Repeat("b", 50)
	sum = sha256.Sum256([]byte(over))
	if rec := env.test("POST", "/v1/test/objects/sha256/"+hex.EncodeToString(sum[:]), over); rec.Code != 409 || errorCode(rec) != "quota_bytes_exceeded" {
		t.Fatalf("byte quota: %d %s", rec.Code, rec.Body.String())
	}
	env.free = 1<<30 + 10
	small := strings.Repeat("c", 20)
	sum = sha256.Sum256([]byte(small))
	if rec := env.test("POST", "/v1/test/objects/sha256/"+hex.EncodeToString(sum[:]), small); rec.Code != 507 || errorCode(rec) != "disk_space_low" {
		t.Fatalf("disk margin: %d %s", rec.Code, rec.Body.String())
	}
	var usage testUsageView
	rec := env.test("GET", "/v1/test/usage", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &usage)
	if usage.Profiles.Used != 2 || usage.Profiles.Max != 2 || !usage.Profiles.Warning || usage.Bytes.Used != 60 || usage.Bytes.Percent != 60 || usage.Bytes.Warning {
		t.Fatalf("usage: %+v", usage)
	}
	// Deleting a profile frees its slot.
	if rec := env.test("DELETE", "/v1/test/profiles/profile_test-q1", `{"confirm_profile_id":"profile_test-q1"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"q3","parent_profile_id":"profile_root"}`), 201)
}

func TestComputeTestUsage(t *testing.T) {
	q := TestQuotas{MaxProfiles: 200, MaxObjectBytes: 2 << 30, MaxBytes: 100 << 30, DiskMarginBytes: 10 << 30}
	view := computeTestUsage(q, storage.TestUsage{LiveProfiles: 170, CountedBytes: 40 << 30}, 30<<30, 100<<30, nil)
	if view.Profiles.Percent != 85 || !view.Profiles.Warning || view.Bytes.Percent != 40 || view.Bytes.Warning {
		t.Fatalf("percentages: %+v", view)
	}
	// 30 GiB free - 10 GiB margin = 20 GiB room: the effective cap is 40+20 GiB.
	if view.EffectiveMaxBytes != 60<<30 || view.Disk.UsedPercent != 70 || view.Disk.Warning {
		t.Fatalf("disk cap: %+v", view)
	}
	view = computeTestUsage(q, storage.TestUsage{}, 5<<30, 100<<30, nil)
	if view.EffectiveMaxBytes != 0 || !view.Disk.Warning || len(view.Warnings) != 1 {
		t.Fatalf("full disk: %+v", view)
	}
	view = computeTestUsage(q, storage.TestUsage{}, 0, 0, context.Canceled)
	if view.Disk.Available || view.EffectiveMaxBytes != q.MaxBytes {
		t.Fatalf("unknown disk: %+v", view)
	}
}

func TestTestRollbackIsServerSigned(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	env.seedNormal()
	first := decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"rb","parent_profile_id":"profile_root"}`), 201)
	second := decodePublished(t, env.test("POST", "/v1/test/profiles/profile_test-rb/changes", `{"notes":"segunda"}`), 201)
	if second.Stable != "promoted" {
		t.Fatalf("stable did not track: %+v", second)
	}
	rec := env.test("POST", "/v1/test/profiles/profile_test-rb/channels/stable/rollback", `{"revision_id":"`+first.Revision.RevisionID+`","reason":"probar rollback"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), first.Revision.RevisionID) {
		t.Fatalf("rollback: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogIsNotTruncatedByTestRevisions(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	_, c1 := env.seedNormal()
	decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"aaa","parent_profile_id":"profile_root"}`), 201)
	for i := 0; i < 12; i++ {
		decodePublished(t, env.test("POST", "/v1/test/profiles/profile_test-aaa/changes", `{"notes":"n"}`), 201)
	}
	// profile_zchild sorts after the test revisions; a revision-count limit used to drop it.
	pin := revision.ParentRef{ProfileID: "profile_child", RevisionID: c1.Revision.RevisionID, ManifestSHA256: c1.Revision.ManifestSHA256}
	var z revision.Manifest
	_ = json.Unmarshal(testChildManifest("profile_zchild", "rev_00000000000000z1", pin.ManifestSHA256), &z)
	z.Base = &pin
	env.publishAdmin(z)
	handler, err := New(Dependencies{Objects: env.cas, Store: env.store, Keys: testKeys{env.signer.KeyID(): env.signer.PublicKey()}, Signer: env.signer},
		Options{ReadMiddleware: identity, AdminMiddleware: identity, ListLimit: 5})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/profiles", nil))
	for _, id := range []string{"profile_root", "profile_child", "profile_zchild"} {
		if !strings.Contains(rec.Body.String(), id) {
			t.Fatalf("%s missing from the catalog: %s", id, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), "profile_test-") || strings.Contains(rec.Body.String(), `"truncated":true`) {
		t.Fatalf("catalog: %s", rec.Body.String())
	}
}

func jsonInt(v int64) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
