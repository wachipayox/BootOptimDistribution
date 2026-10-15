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
)

func TestPublishingParentAutoRepinsChildrenAndSkipsConflicts(t *testing.T) {
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
	signer, err := releasesigning.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Dependencies{Objects: cas, Store: store, Keys: testKeys{signer.KeyID(): signer.PublicKey()}, Signer: signer},
		Options{ReadMiddleware: identity, AdminMiddleware: identity, MaxObjectBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	stage := func(content string) string {
		sum := sha256.Sum256([]byte(content))
		hash := hex.EncodeToString(sum[:])
		if rec := call("POST", "/v1/admin/objects/sha256/"+hash, content); rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		return hash
	}
	type published struct {
		Revision   revisionRef    `json:"revision"`
		AutoFollow []followResult `json:"auto_follow"`
	}
	publish := func(manifest []byte) published {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": manifestDigest(t, manifest), "manifest": json.RawMessage(manifest)})
		rec := call("POST", "/v1/admin/publications", string(body))
		if rec.Code != http.StatusCreated {
			t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
		}
		var out published
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	child := func(profileID, revisionID string, parent revision.ParentRef, edit func(*revision.Manifest)) []byte {
		var m revision.Manifest
		if err := json.Unmarshal(testChildManifest(profileID, revisionID, parent.ManifestSHA256), &m); err != nil {
			t.Fatal(err)
		}
		m.Base = &parent
		if edit != nil {
			edit(&m)
		}
		raw, _ := json.Marshal(m)
		return raw
	}

	first, second := "first bytes", "second bytes"
	hashA, hashB := stage(first), stage(second)
	var rootV1 revision.Manifest
	if err := json.Unmarshal(testManifest("rev_0000000000000001", 1, hashA, int64(len(first))), &rootV1); err != nil {
		t.Fatal(err)
	}
	rootV1.Configs = []revision.ConfigEntry{{Path: "config/shared.toml", Object: revision.ObjectRef{SHA256: hashA, Size: int64(len(first))}, Policy: "default_once"}}
	rawV1, _ := json.Marshal(rootV1)
	r1 := publish(rawV1)
	pinRoot := revision.ParentRef{ProfileID: "profile_root", RevisionID: r1.Revision.RevisionID, ManifestSHA256: r1.Revision.ManifestSHA256}
	c1 := publish(child("profile_child", "rev_00000000000000c1", pinRoot, func(m *revision.Manifest) {
		m.Configs = []revision.ConfigEntry{{Path: "config/own.toml", Object: revision.ObjectRef{SHA256: hashB, Size: int64(len(second))}, Policy: "enforced"}}
	}))
	pinChild := revision.ParentRef{ProfileID: "profile_child", RevisionID: c1.Revision.RevisionID, ManifestSHA256: c1.Revision.ManifestSHA256}
	g1 := publish(child("profile_grandchild", "rev_00000000000000g1", pinChild, nil))
	promote := func(profileID, revisionID string) {
		t.Helper()
		if rec := call("POST", "/v1/admin/profiles/"+profileID+"/channels/stable/promote", `{"revision_id":"`+revisionID+`"}`); rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
	}
	// child's stable tracks its latest; grandchild's stable is held on an older revision.
	promote("profile_child", c1.Revision.RevisionID)
	promote("profile_grandchild", g1.Revision.RevisionID)
	publish(child("profile_grandchild", "rev_00000000000000g2", pinChild, func(m *revision.Manifest) { m.Revision.Sequence = 2 }))
	publish(child("profile_conflict", "rev_00000000000000f1", pinRoot, func(m *revision.Manifest) {
		m.SchemaVersion = 2
		m.ConfigSettings = []revision.ConfigSetting{{Path: "config/shared.toml", Format: "toml", Key: "client.enabled", Value: true, Policy: "default_once"}}
	}))

	// The new root drops the config the conflicting child configures.
	r2 := publish(testManifest("rev_0000000000000002", 2, hashB, int64(len(second))))
	if r2.Revision.RevisionID != "rev_0000000000000002" {
		t.Fatalf("parent publication changed: %+v", r2.Revision)
	}
	byProfile := map[string]followResult{}
	for _, f := range r2.AutoFollow {
		byProfile[f.ProfileID] = f
	}
	if len(byProfile) != 3 {
		t.Fatalf("unexpected follow results: %+v", r2.AutoFollow)
	}
	if f := byProfile["profile_conflict"]; f.Status != "skipped" || f.Code != "auto_follow_conflict" {
		t.Fatalf("conflicting child should be skipped with warning: %+v", f)
	}
	for _, id := range []string{"profile_child", "profile_grandchild"} {
		if f := byProfile[id]; f.Status != "published" || f.Revision == nil || f.Revision.Sequence < 2 {
			t.Fatalf("%s not auto-published: %+v", id, f)
		}
	}

	if f := byProfile["profile_child"]; f.Stable != "promoted" {
		t.Fatalf("tracking stable not promoted: %+v", f)
	}
	if f := byProfile["profile_grandchild"]; f.Stable != "stable_held" {
		t.Fatalf("held stable moved: %+v", f)
	}
	stableOf := func(profileID string) string {
		record, err := store.Channel(context.Background(), profileID, "stable")
		if err != nil {
			t.Fatal(err)
		}
		return record.RevisionID
	}
	if stableOf("profile_child") != byProfile["profile_child"].Revision.RevisionID || stableOf("profile_grandchild") != g1.Revision.RevisionID {
		t.Fatal("stable channels not as expected")
	}

	manifestOf := func(profileID, revisionID string) revision.Manifest {
		rec := call("GET", "/v1/profiles/"+profileID+"/revisions/"+revisionID, "")
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		var resp struct {
			Envelope revision.SignedRevisionEnvelope `json:"envelope"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		var m revision.Manifest
		if err := json.Unmarshal(resp.Envelope.Manifest, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	childRef := byProfile["profile_child"].Revision
	cm := manifestOf("profile_child", childRef.RevisionID)
	if cm.Base.RevisionID != "rev_0000000000000002" || cm.Base.ManifestSHA256 != r2.Revision.ManifestSHA256 {
		t.Fatalf("child not re-pinned: %+v", cm.Base)
	}
	if len(cm.Configs) != 1 || cm.Configs[0].Object.SHA256 != hashB || !strings.HasPrefix(cm.Revision.ReleaseNotes, "auto: sigue a profile_root rev rev_0000000000000002") {
		t.Fatalf("child own content or note changed: %+v", cm)
	}
	gm := manifestOf("profile_grandchild", byProfile["profile_grandchild"].Revision.RevisionID)
	if gm.Base.ProfileID != "profile_child" || gm.Base.RevisionID != childRef.RevisionID || gm.Base.ManifestSHA256 != childRef.ManifestSHA256 {
		t.Fatalf("grandchild not cascaded: %+v", gm.Base)
	}
}
