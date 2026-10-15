package profileapi

import (
	"crypto/sha256"
	"encoding/base64"
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

func TestAutomaticPublicationValidatesJVMOptions(t *testing.T) {
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
	keys := testKeys{signer.KeyID(): signer.PublicKey()}
	handler, err := New(Dependencies{Objects: cas, Store: store, Keys: keys, Signer: signer, PublicKeys: map[string]string{
		signer.KeyID(): base64.RawURLEncoding.EncodeToString(signer.PublicKey()),
	}}, Options{ReadMiddleware: identity, AdminMiddleware: identity, MaxObjectBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	object := "synthetic bytes"
	digest := sha256.Sum256([]byte(object))
	hash := hex.EncodeToString(digest[:])
	if rec := call("POST", "/v1/admin/objects/sha256/"+hash, object); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	publish := func(revisionID string, sequence int64, template string) *httptest.ResponseRecorder {
		t.Helper()
		var manifest map[string]any
		if err := json.Unmarshal(testManifest(revisionID, sequence, hash, int64(len(object))), &manifest); err != nil {
			t.Fatal(err)
		}
		manifest["schema_version"] = 3
		manifest["config_settings"] = []any{}
		manifest["remove_jvm_options"] = []any{}
		manifest["jvm_options"] = []any{map[string]any{
			"id": "jit-compiler-threads", "label": "Hilos del compilador JIT", "template": template,
			"type": "int", "min": 2, "max": 4, "help": "Limita los hilos del compilador JIT.",
			"java": map[string]any{"min": 21}, "vm": []any{"hotspot"},
			"platforms": map[string]any{"pc": map[string]any{
				"default_enabled": false, "default_value": 2,
			}},
		}}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		request, err := json.Marshal(map[string]any{"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": manifestDigest(t, raw), "manifest": json.RawMessage(raw)})
		if err != nil {
			t.Fatal(err)
		}
		return call("POST", "/v1/admin/publications", string(request))
	}

	if rec := publish("rev_0000000000000001", 1, "-javaagent:{value}.jar"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("agent template published: %d %s", rec.Code, rec.Body.String())
	}
	if rec := publish("rev_0000000000000001", 1, "-XX:CICompilerCount={value}"); rec.Code != http.StatusCreated {
		t.Fatalf("valid JVM option rejected: %d %s", rec.Code, rec.Body.String())
	}
	rec := call("GET", "/v1/profiles/profile_root/revisions/rev_0000000000000001", "")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var response struct {
		Envelope json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	verified, err := revision.ParseAndVerifyRevisionEnvelope(response.Envelope, signer.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if options := verified.Manifest().JVMOptions; len(options) != 1 || options[0].Template != "-XX:CICompilerCount={value}" {
		t.Fatalf("signed revision lost its JVM options: %+v", options)
	}
}
