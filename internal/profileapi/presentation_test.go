package profileapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPresentationDoesNotPublishRevisionAndPublishesOnlyItsIcon(t *testing.T) {
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
	h, err := New(Dependencies{Objects: cas, Store: store, Keys: testKeys{"test-key": pub}}, Options{ReadMiddleware: identity, AdminMiddleware: identity})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("synthetic")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if err := cas.Put(context.Background(), storage.Object{SHA256: digest, Size: int64(len(data))}, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	manifest := testManifest("rev_0000000000000001", 1, digest, int64(len(data)))
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, path, strings.NewReader(body)))
		return r
	}
	if r := call("POST", "/v1/admin/revisions", string(signEnvelope(t, manifest, priv))); r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	var icon bytes.Buffer
	png.Encode(&icon, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	sum = sha256.Sum256(icon.Bytes())
	iconDigest := hex.EncodeToString(sum[:])
	cas.Put(context.Background(), storage.Object{SHA256: iconDigest, Size: int64(icon.Len())}, bytes.NewReader(icon.Bytes()))
	if r := call("GET", "/v1/objects/sha256/"+iconDigest, ""); r.Code != 404 {
		t.Fatal("staged icon exposed")
	}
	body, _ := json.Marshal(map[string]interface{}{"name": "Edited", "description": "New description", "icon": map[string]interface{}{"sha256": iconDigest, "size": icon.Len(), "media_type": "image/png"}})
	// Fixture identity is obtained from its signed manifest, never guessed.
	var decoded struct {
		Profile struct {
			ID string `json:"id"`
		} `json:"profile"`
	}
	json.Unmarshal(manifest, &decoded)
	path := "/v1/admin/profiles/" + decoded.Profile.ID + "/presentation"
	if r := call(http.MethodPut, path, string(body)); r.Code != 200 {
		t.Fatalf("save %d %s", r.Code, r.Body.String())
	}
	head, err := store.ProfileHead(context.Background(), decoded.Profile.ID)
	if err != nil || head.Sequence != 1 {
		t.Fatal("presentation changed revision")
	}
	if r := call("GET", "/v1/objects/sha256/"+iconDigest, ""); r.Code != 200 {
		t.Fatalf("icon unavailable: %s", r.Body.String())
	}
	if r := call("GET", "/v1/profiles", ""); !strings.Contains(r.Body.String(), "New description") {
		t.Fatal(r.Body.String())
	}
	if r := call(http.MethodPut, path, `{"name":"Edited","description":"","icon":null}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := call("GET", "/v1/objects/sha256/"+iconDigest, ""); r.Code != 404 {
		t.Fatal("removed icon exposed")
	}
}
