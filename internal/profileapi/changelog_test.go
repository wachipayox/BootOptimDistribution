package profileapi

import (
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

func TestDeriveChangelogText(t *testing.T) {
	got := deriveChangelogText("\nTítulo\n- uno\nPárrafo 1\n\n- dos\nPárrafo 2\n\n")
	if got.Title != "Título" || len(got.Changes) != 2 || got.Changes[0] != "uno" || got.Changes[1] != "dos" ||
		got.Description != "Párrafo 1\n\nPárrafo 2" {
		t.Fatalf("derived: %+v", got)
	}
	if got := deriveChangelogText("- solo cambio"); got.Title != "" || len(got.Changes) != 1 {
		t.Fatalf("bullet-only notes: %+v", got)
	}
	if got := deriveChangelogText("auto: sigue a profile_root rev rev_1"); got.Title != "" || got.Description != "" || len(got.Changes) != 0 || got.Changes == nil {
		t.Fatalf("auto notes must stay hidden: %+v", got)
	}
	long := deriveChangelogText(strings.Repeat("é", 100) + "\n- " + strings.Repeat("x", 400))
	if len(long.Title) > maxChangelogTitle || len(long.Changes[0]) != maxChangelogChange {
		t.Fatalf("derived text must respect contract limits: %d %d", len(long.Title), len(long.Changes[0]))
	}
}

type changelogEnv struct {
	t       *testing.T
	handler http.Handler
}

func newChangelogEnv(t *testing.T) *changelogEnv {
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
	handler, err := New(Dependencies{Objects: cas, Store: store, Keys: testKeys{signer.KeyID(): signer.PublicKey()}, Signer: signer},
		Options{ReadMiddleware: identity, AdminMiddleware: identity, MaxObjectBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return &changelogEnv{t: t, handler: handler}
}

func (e *changelogEnv) call(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func (e *changelogEnv) stage(content string) revision.ObjectRef {
	e.t.Helper()
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	if rec := e.call("POST", "/v1/admin/objects/sha256/"+hash, content); rec.Code != http.StatusOK {
		e.t.Fatal(rec.Body.String())
	}
	return revision.ObjectRef{SHA256: hash, Size: int64(len(content)), MediaType: "application/java-archive"}
}

type publishedRev struct {
	Revision   revisionRef    `json:"revision"`
	AutoFollow []followResult `json:"auto_follow"`
}

func (e *changelogEnv) publish(m revision.Manifest) publishedRev {
	e.t.Helper()
	raw, _ := json.Marshal(m)
	body, _ := json.Marshal(map[string]any{"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": manifestDigest(e.t, raw), "manifest": json.RawMessage(raw)})
	rec := e.call("POST", "/v1/admin/publications", string(body))
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("publish %s: %d %s", m.Revision.ID, rec.Code, rec.Body.String())
	}
	var out publishedRev
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func clManifest(profile, id string, seq int64, base *revision.ParentRef, notes string, mods ...revision.ModEntry) revision.Manifest {
	return revision.Manifest{
		SchemaVersion: 1,
		Revision:      revision.Revision{ID: id, Sequence: seq, CreatedAt: "2026-10-0" + string(rune('0'+seq)) + "T12:00:00Z", ReleaseNotes: notes},
		Profile:       revision.Profile{ID: profile, Name: "Perfil " + profile, Official: true},
		Game:          revision.Game{Minecraft: "1.21.1", NeoForge: "21.1.0"},
		Base:          base,
		Permissions: revision.Permissions{DeriveLocal: true, Mods: revision.ModPermissions{Add: true, Remove: true},
			Configs: revision.ConfigPermissions{OverrideDefaultOnce: true}, MaxInheritanceDepth: 8},
		Mods: append([]revision.ModEntry{}, mods...), RemoveMods: []revision.RemoveMod{}, Configs: []revision.ConfigEntry{},
		RemoveConfigs: []revision.RemoveConfig{}, Objects: []revision.ObjectEntry{},
	}
}

func clModEntry(id, file string, ref revision.ObjectRef) revision.ModEntry {
	return revision.ModEntry{ID: id, Path: "mods/" + file, Object: ref}
}

type clResponse struct {
	SchemaVersion int                  `json:"schema_version"`
	ProfileID     string               `json:"profile_id"`
	Lineage       []lineageView        `json:"lineage"`
	Entries       []changelogEntryView `json:"entries"`
	NextCursor    *string              `json:"next_cursor"`
}

func (e *changelogEnv) changelog(query string) (clResponse, *httptest.ResponseRecorder) {
	e.t.Helper()
	return decodeChangelog(e.t, e.call("GET", query, ""))
}

func decodeChangelog(t *testing.T, rec *httptest.ResponseRecorder) (clResponse, *httptest.ResponseRecorder) {
	t.Helper()
	var out clResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err, rec.Body.String())
		}
	}
	return out, rec
}

func pin(p publishedRev, profile string) *revision.ParentRef {
	return &revision.ParentRef{ProfileID: profile, RevisionID: p.Revision.RevisionID, ManifestSHA256: p.Revision.ManifestSHA256}
}

func originsOf(entries []changelogEntryView) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Origin.RevisionID
	}
	return out
}

func modFiles(refs []modRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.ID + "=" + r.File
	}
	return strings.Join(parts, ",")
}
