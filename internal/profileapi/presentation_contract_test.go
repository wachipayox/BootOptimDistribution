package profileapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
)

func presentationPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func presentationJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func transparentPNG(t *testing.T, w, h int) []byte {
	return presentationPNG(t, image.NewNRGBA(image.Rect(0, 0, w, h)))
}

func opaquePNG(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	return presentationPNG(t, img)
}

// ref stages content in the CAS and returns its object reference as JSON.
func (e *testEnv) ref(content []byte, mediaType string) string {
	e.t.Helper()
	digest, size := e.stageAdmin(string(content))
	return fmt.Sprintf(`{"sha256":%q,"size":%d,"media_type":%q}`, digest, size, mediaType)
}

func refDigest(ref string) string {
	var r revision.ObjectRef
	_ = json.Unmarshal([]byte(ref), &r)
	return r.SHA256
}

func (e *testEnv) artwork(content []byte, mediaType, author string) string {
	return fmt.Sprintf(`{"image":%s,"author":%q}`, e.ref(content, mediaType), author)
}

func (e *testEnv) put(profileID, body string) *httptest.ResponseRecorder {
	return e.admin("PUT", "/v1/admin/profiles/"+profileID+"/presentation", body)
}

func (e *testEnv) catalogEntry(path, profileID string) map[string]any {
	e.t.Helper()
	rec := e.admin("GET", path, "")
	if rec.Code != 200 {
		e.t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
	}
	var out struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	for _, p := range out.Profiles {
		if p["profile_id"] == profileID {
			return p
		}
	}
	e.t.Fatalf("%s: profile %s not listed", path, profileID)
	return nil
}

func (e *testEnv) publicEntry(profileID string) map[string]any {
	return e.catalogEntry("/v1/profiles?include_test=1", profileID)
}

func (e *testEnv) objectStatus(digest string) int {
	return e.admin("GET", "/v1/objects/sha256/"+digest, "").Code
}

func inherited(entry map[string]any, field string) any {
	m, _ := entry["inherited_from"].(map[string]any)
	return m[field]
}

func mediaOf(entry map[string]any, field string) string {
	m, _ := entry[field].(map[string]any)
	s, _ := m["sha256"].(string)
	return s
}

func TestPresentationPartialUpdateAbsentVersusNull(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.seedNormal()
	art := env.artwork(presentationJPEG(t, 1920, 760), "image/jpeg", "Autora")
	logo := env.ref(transparentPNG(t, 200, 80), "image/png")

	if rec := env.put("profile_child", `{"name":"Hijo","description":"Texto","artwork":`+art+`,"logo":`+logo+`}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	// Absent fields keep their value.
	if rec := env.put("profile_child", `{"description":"Otro"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	entry := env.publicEntry("profile_child")
	if entry["name"] != "Hijo" || entry["description"] != "Otro" || entry["artwork"] == nil || entry["logo"] == nil {
		t.Fatalf("absent field changed something: %v", entry)
	}
	// null removes only the own value of that field.
	if rec := env.put("profile_child", `{"artwork":null}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	entry = env.publicEntry("profile_child")
	if entry["artwork"] != nil || entry["logo"] == nil || entry["name"] != "Hijo" {
		t.Fatalf("null removed the wrong thing: %v", entry)
	}
	// A first partial write on a never-edited profile keeps the signed name.
	if rec := env.put("profile_root", `{"logo":`+logo+`}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if env.publicEntry("profile_root")["name"] != "Synthetic root" {
		t.Fatal("signed name lost")
	}
	for _, body := range []string{`{"name":null}`, `{"name":""}`, `{"name":"  "}`, `{"name":5}`, `{"description":5}`} {
		if rec := env.put("profile_child", body); rec.Code != 422 || errorCode(rec) != "invalid_presentation" {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := env.put("profile_child", `{"unknown":1}`); rec.Code != 400 {
		t.Errorf("unknown field: %d", rec.Code)
	}
	if rec := env.put("profile_missing", `{"name":"x"}`); rec.Code != 404 {
		t.Errorf("missing profile: %d", rec.Code)
	}
}

func TestPresentationArtworkAndLogoValidation(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.seedNormal()
	paletted := image.NewPaletted(image.Rect(0, 0, 100, 50), color.Palette{color.NRGBA{0, 0, 0, 0}, color.NRGBA{255, 0, 0, 255}})
	opaquePalette := image.NewPaletted(image.Rect(0, 0, 100, 50), color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 0, 0, 255}})
	jpg := presentationJPEG(t, 1920, 760)
	pngArt := opaquePNG(t, 1920, 760)

	cases := []struct {
		name, body, code string
		want             int
	}{
		{"artwork jpeg ok", `{"artwork":` + env.artwork(jpg, "image/jpeg", "A") + `}`, "", 200},
		{"artwork png ok", `{"artwork":` + env.artwork(pngArt, "image/png", "A") + `}`, "", 200},
		{"artwork wrong width", `{"artwork":` + env.artwork(presentationJPEG(t, 1919, 760), "image/jpeg", "A") + `}`, "invalid_artwork", 422},
		{"artwork wrong height", `{"artwork":` + env.artwork(presentationJPEG(t, 1920, 761), "image/jpeg", "A") + `}`, "invalid_artwork", 422},
		{"artwork png declared jpeg", `{"artwork":` + env.artwork(pngArt, "image/jpeg", "A") + `}`, "invalid_artwork", 422},
		{"artwork webp type", `{"artwork":` + env.artwork(jpg, "image/webp", "A") + `}`, "invalid_artwork", 422},
		{"artwork not an image", `{"artwork":` + env.artwork([]byte("plain text"), "image/png", "A") + `}`, "invalid_artwork", 422},
		{"artwork no author", `{"artwork":` + env.artwork(jpg, "image/jpeg", "") + `}`, "invalid_artwork", 422},
		{"artwork multiline author", `{"artwork":` + env.artwork(jpg, "image/jpeg", "a\nb") + `}`, "invalid_artwork", 422},
		{"artwork long author", `{"artwork":` + env.artwork(jpg, "image/jpeg", strings.Repeat("a", 65)) + `}`, "invalid_artwork", 422},
		{"artwork author 64 bytes", `{"artwork":` + env.artwork(jpg, "image/jpeg", strings.Repeat("a", 64)) + `}`, "", 200},
		{"artwork wrong size", `{"artwork":{"image":{"sha256":"` + refDigest(env.ref(jpg, "image/jpeg")) + `","size":1,"media_type":"image/jpeg"},"author":"A"}}`, "invalid_artwork", 422},
		{"artwork unknown object", `{"artwork":{"image":{"sha256":"` + strings.Repeat("c", 64) + `","size":10,"media_type":"image/jpeg"},"author":"A"}}`, "invalid_artwork", 422},
		{"artwork without image", `{"artwork":{"author":"A"}}`, "invalid_artwork", 422},
		{"logo rgba ok", `{"logo":` + env.ref(transparentPNG(t, 200, 80), "image/png") + `}`, "", 200},
		{"logo min size ok", `{"logo":` + env.ref(transparentPNG(t, 16, 16), "image/png") + `}`, "", 200},
		{"logo max size ok", `{"logo":` + env.ref(transparentPNG(t, 8192, 16), "image/png") + `}`, "", 200},
		{"logo palette with tRNS ok", `{"logo":` + env.ref(presentationPNG(t, paletted), "image/png") + `}`, "", 200},
		{"logo opaque rgb", `{"logo":` + env.ref(opaquePNG(t, 200, 80), "image/png") + `}`, "invalid_logo", 422},
		{"logo opaque palette", `{"logo":` + env.ref(presentationPNG(t, opaquePalette), "image/png") + `}`, "invalid_logo", 422},
		{"logo too narrow", `{"logo":` + env.ref(transparentPNG(t, 15, 80), "image/png") + `}`, "invalid_logo", 422},
		{"logo too short", `{"logo":` + env.ref(transparentPNG(t, 200, 15), "image/png") + `}`, "invalid_logo", 422},
		{"logo too wide", `{"logo":` + env.ref(transparentPNG(t, 8193, 16), "image/png") + `}`, "invalid_logo", 422},
		{"logo too tall", `{"logo":` + env.ref(transparentPNG(t, 16, 8193), "image/png") + `}`, "invalid_logo", 422},
		{"logo jpeg", `{"logo":` + env.ref(jpg, "image/jpeg") + `}`, "invalid_logo", 422},
		{"logo jpeg declared png", `{"logo":` + env.ref(jpg, "image/png") + `}`, "invalid_logo", 422},
	}
	for _, c := range cases {
		rec := env.put("profile_child", c.body)
		if rec.Code != c.want || (c.code != "" && errorCode(rec) != c.code) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
}

func TestPresentationInheritsThroughThreeLevelsWithReplacement(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	_, c1 := env.seedNormal()
	// profile_example-style chain: root -> child -> grandchild.
	pin := revision.ParentRef{ProfileID: "profile_child", RevisionID: c1.Revision.RevisionID, ManifestSHA256: c1.Revision.ManifestSHA256}
	var grand revision.Manifest
	_ = json.Unmarshal(testChildManifest("profile_grand", "rev_00000000000000g1", pin.ManifestSHA256), &grand)
	grand.Base = &pin
	env.publishAdmin(grand)

	icon := env.ref(presentationPNG(t, image.NewRGBA(image.Rect(0, 0, 16, 16))), "image/png")
	art := env.artwork(presentationJPEG(t, 1920, 760), "image/jpeg", "Autora")
	logo := env.ref(transparentPNG(t, 200, 80), "image/png")
	if rec := env.put("profile_root", `{"icon":`+icon+`,"artwork":`+art+`,"logo":`+logo+`}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	// A child without its own icon inherits its mother's (Beta -> main profile case).
	child := env.publicEntry("profile_child")
	if mediaOf(child, "icon") != refDigest(icon) || inherited(child, "icon") != "profile_root" {
		t.Fatalf("child did not inherit the icon: %v", child)
	}
	if child["parent_profile_id"] != "profile_root" || env.publicEntry("profile_root")["parent_profile_id"] != nil {
		t.Fatalf("parent_profile_id: %v / %v", child["parent_profile_id"], env.publicEntry("profile_root")["parent_profile_id"])
	}
	grandEntry := env.publicEntry("profile_grand")
	if grandEntry["parent_profile_id"] != "profile_child" || inherited(grandEntry, "artwork") != "profile_root" || inherited(grandEntry, "logo") != "profile_root" {
		t.Fatalf("grandchild chain: %v", grandEntry)
	}
	root := env.publicEntry("profile_root")
	if inherited(root, "icon") != nil || inherited(root, "artwork") != nil || mediaOf(root, "logo") != refDigest(logo) {
		t.Fatalf("own values must not be reported as inherited: %v", root)
	}
	// name and description are not inherited.
	if env.put("profile_root", `{"name":"Madre","description":"Texto madre"}`).Code != 200 {
		t.Fatal("rename")
	}
	if g := env.publicEntry("profile_grand"); g["name"] != "Synthetic child" || g["description"] == "Texto madre" {
		t.Fatalf("name/description inherited: %v", g)
	}

	// The middle profile replaces the artwork; the grandchild follows the nearest one.
	art2 := env.artwork(opaquePNG(t, 1920, 760), "image/png", "Otra")
	if rec := env.put("profile_child", `{"artwork":`+art2+`}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	grandEntry = env.publicEntry("profile_grand")
	if inherited(grandEntry, "artwork") != "profile_child" || inherited(grandEntry, "icon") != "profile_root" {
		t.Fatalf("replacement: %v", grandEntry)
	}
	if a, _ := grandEntry["artwork"].(map[string]any); a["author"] != "Otra" {
		t.Fatalf("artwork author: %v", grandEntry["artwork"])
	}
	// Removing the own value returns to inheriting.
	if env.put("profile_child", `{"artwork":null}`).Code != 200 {
		t.Fatal("remove")
	}
	if inherited(env.publicEntry("profile_grand"), "artwork") != "profile_root" {
		t.Fatal("did not go back to inheriting")
	}

	// The chain is followed through a deleted mother.
	if rec := env.admin("DELETE", "/v1/admin/profiles/profile_child", `{"confirm_profile_id":"profile_child"}`); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if g := env.publicEntry("profile_grand"); inherited(g, "icon") != "profile_root" || g["parent_profile_id"] != "profile_child" {
		t.Fatalf("through deleted mother: %v", g)
	}
}

func TestPresentationOwnIconIncludesSignedIconAndInheritsIt(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	iconBytes := presentationPNG(t, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	digest, size := env.stageAdmin(string(iconBytes))
	modDigest, modSize := env.stageAdmin("mod bytes")
	var root revision.Manifest
	_ = json.Unmarshal(testManifest("rev_0000000000000001", 1, modDigest, modSize), &root)
	root.Profile.ID = "profile_example"
	root.Profile.Icon = &revision.ObjectRef{SHA256: digest, Size: size, MediaType: "image/png"}
	r1 := env.publishAdmin(root)
	var beta revision.Manifest
	_ = json.Unmarshal(testChildManifest("profile_example-beta", "rev_00000000000000b1", r1.Revision.ManifestSHA256), &beta)
	beta.Base = &revision.ParentRef{ProfileID: "profile_example", RevisionID: r1.Revision.RevisionID, ManifestSHA256: r1.Revision.ManifestSHA256}
	env.publishAdmin(beta)

	parent := env.publicEntry("profile_example")
	child := env.publicEntry("profile_example-beta")
	if mediaOf(parent, "icon") != digest || inherited(parent, "icon") != nil {
		t.Fatalf("signed icon is the parent's own: %v", parent)
	}
	if mediaOf(child, "icon") != digest || inherited(child, "icon") != "profile_example" {
		t.Fatalf("child must inherit the signed icon: %v", child)
	}
}

func TestPresentationCatalogShapeOwnPresentationAndETag(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.seedNormal()
	if env.put("profile_child", `{"name":"Hijo","logo":`+env.ref(transparentPNG(t, 100, 50), "image/png")+`}`).Code != 200 {
		t.Fatal("put")
	}
	public := env.publicEntry("profile_child")
	if _, ok := public["presentation"]; ok {
		t.Fatal("public catalog still has the presentation subobject")
	}
	if _, ok := public["own_presentation"]; ok {
		t.Fatal("public catalog exposes own_presentation")
	}
	for _, key := range []string{"parent_profile_id", "artwork", "logo", "inherited_from"} {
		if _, ok := public[key]; !ok {
			t.Errorf("public catalog misses %s: %v", key, public)
		}
	}
	admin := env.catalogEntry("/v1/admin/profiles", "profile_child")
	own, _ := admin["own_presentation"].(map[string]any)
	if own == nil || own["name"] != "Hijo" || own["logo"] == nil || own["artwork"] != nil {
		t.Fatalf("own_presentation: %v", admin["own_presentation"])
	}
	if env.catalogEntry("/v1/admin/profiles", "profile_root")["own_presentation"] != nil {
		t.Fatal("never-edited profile has own_presentation")
	}

	rec := env.admin("GET", "/v1/profiles", "")
	etag := rec.Header().Get("ETag")
	if rec.Code != 200 || etag == "" {
		t.Fatalf("catalog ETag: %d %q", rec.Code, etag)
	}
	req := httptest.NewRequest("GET", "/v1/profiles", nil)
	req.Header.Set("If-None-Match", etag)
	cached := httptest.NewRecorder()
	env.handler.ServeHTTP(cached, req)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("If-None-Match: %d %q", cached.Code, cached.Body.String())
	}
	if env.put("profile_child", `{"description":"cambia"}`).Code != 200 {
		t.Fatal("put")
	}
	req = httptest.NewRequest("GET", "/v1/profiles", nil)
	req.Header.Set("If-None-Match", etag)
	changed := httptest.NewRecorder()
	env.handler.ServeHTTP(changed, req)
	if changed.Code != 200 || changed.Header().Get("ETag") == etag {
		t.Fatalf("changed catalog kept its ETag: %d", changed.Code)
	}
}

func TestPresentationObjectsArePublicOnlyWhileReferenced(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.seedNormal()
	artRef := env.ref(presentationJPEG(t, 1920, 760), "image/jpeg")
	art := `{"image":` + artRef + `,"author":"Autora"}`
	logo := env.ref(transparentPNG(t, 200, 80), "image/png")
	artDigest, logoDigest := refDigest(artRef), refDigest(logo)
	if env.objectStatus(artDigest) != 404 || env.objectStatus(logoDigest) != 404 {
		t.Fatal("staged objects exposed before being referenced")
	}
	if env.put("profile_child", `{"artwork":`+art+`,"logo":`+logo+`}`).Code != 200 {
		t.Fatal("put")
	}
	for _, d := range []string{artDigest, logoDigest} {
		rec := env.admin("GET", "/v1/objects/sha256/"+d, "")
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Fatalf("referenced object not served: %d", rec.Code)
		}
	}
	if env.put("profile_child", `{"artwork":null}`).Code != 200 {
		t.Fatal("put")
	}
	if env.objectStatus(artDigest) != 404 || env.objectStatus(logoDigest) != 200 {
		t.Fatalf("artwork/logo availability after removing artwork: %d %d", env.objectStatus(artDigest), env.objectStatus(logoDigest))
	}
	if env.put("profile_child", `{"logo":null}`).Code != 200 || env.objectStatus(logoDigest) != 404 {
		t.Fatal("logo still served after removal")
	}
}

func TestTestAPIPutsPresentationOfItsOwnProfilesOnly(t *testing.T) {
	env := newTestEnv(t, TestQuotas{})
	env.rotate()
	env.seedNormal()
	decodePublished(t, env.test("POST", "/v1/test/profiles", `{"slug":"a","parent_profile_id":"profile_root"}`), http.StatusCreated)
	art := env.artwork(presentationJPEG(t, 1920, 760), "image/jpeg", "Autora")
	if rec := env.test("PUT", "/v1/test/profiles/profile_test-a/presentation", `{"name":"Prueba","artwork":`+art+`}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	entry := env.publicEntry("profile_test-a")
	if entry["name"] != "Prueba" || entry["artwork"] == nil || entry["parent_profile_id"] != "profile_root" {
		t.Fatalf("test profile entry: %v", entry)
	}
	if rec := env.test("PUT", "/v1/test/profiles/profile_test-a/presentation", `{"logo":`+env.ref(opaquePNG(t, 100, 50), "image/png")+`}`); rec.Code != 422 || errorCode(rec) != "invalid_logo" {
		t.Fatalf("opaque logo: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.test("PUT", "/v1/test/profiles/profile_root/presentation", `{"name":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("normal profile through the test API: %d", rec.Code)
	}
	if rec := env.admin("PUT", "/v1/admin/profiles/profile_test-a/presentation", `{"name":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("test profile through the admin API: %d", rec.Code)
	}
}
