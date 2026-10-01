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

func TestAutomaticPublicationKeepsLegacyHistory(t *testing.T) {
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
	legacy, private := testKeyPair()
	keys := testKeys{"test-key": legacy, signer.KeyID(): signer.PublicKey()}
	handler, err := New(Dependencies{Objects: cas, Store: store, Keys: keys, Signer: signer, PublicKeys: map[string]string{
		"test-key": base64.RawURLEncoding.EncodeToString(legacy), signer.KeyID(): base64.RawURLEncoding.EncodeToString(signer.PublicKey()),
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
	first := testManifest("rev_0000000000000001", 1, hash, int64(len(object)))
	if rec := call("POST", "/v1/admin/revisions", string(signEnvelope(t, first, private))); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	second := testManifest("rev_0000000000000002", 2, hash, int64(len(object)))
	request, err := json.Marshal(map[string]any{"canonicalization": revision.CanonicalizationRFC8785, "manifest_sha256": manifestDigest(t, second), "manifest": json.RawMessage(second)})
	if err != nil {
		t.Fatal(err)
	}
	if rec := call("POST", "/v1/admin/publications", string(request)); rec.Code != http.StatusCreated {
		t.Fatalf("automatic publication: %d %s", rec.Code, rec.Body.String())
	}
	for _, id := range []string{"rev_0000000000000001", "rev_0000000000000002"} {
		rec := call("GET", "/v1/profiles/profile_root/revisions/"+id, "")
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		// Both publications pass exactly the same canonical signature verification.
		var envelope revision.SignedRevisionEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if _, err := revision.ParseAndVerifyRevisionEnvelope(rec.Body.Bytes(), keys[envelope.Signature.KeyID]); err != nil {
			t.Fatal(err)
		}
	}
	rec := call("GET", "/v1/signing-keys", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), signer.KeyID()) || !strings.Contains(rec.Body.String(), "test-key") || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("bad public catalogue: %s", rec.Body.String())
	}
	if rec := call("POST", "/v1/admin/publications", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid request accepted: %d", rec.Code)
	}
}
