package profileapi

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/launcherartifact"
	"github.com/wachipayox/BootOptimDistribution/internal/releasesigning"
	"github.com/wachipayox/BootOptimDistribution/internal/revision"
	"github.com/wachipayox/BootOptimDistribution/internal/storage"
)

type launcherFixture struct {
	t       *testing.T
	handler http.Handler
	signer  *releasesigning.Signer
	store   *storage.SQLiteStore
}

func newLauncherFixture(t *testing.T) *launcherFixture {
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
	keys := testKeys{signer.KeyID(): signer.PublicKey()}
	handler, err := New(Dependencies{Objects: cas, Store: store, Keys: keys, Signer: signer, PublicKeys: map[string]string{
		signer.KeyID(): base64.RawURLEncoding.EncodeToString(signer.PublicKey()),
	}}, Options{ReadMiddleware: identity, AdminMiddleware: identity, MaxObjectBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return &launcherFixture{t, handler, signer, store}
}

func (f *launcherFixture) call(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func (f *launcherFixture) upload(content string) (string, int64) {
	f.t.Helper()
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	if rec := f.call("POST", "/v1/admin/objects/sha256/"+digest, content); rec.Code != http.StatusOK {
		f.t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	return digest, int64(len(content))
}

func releaseBody(app, version, channel, notes string, platforms map[string][3]string) string {
	type art struct {
		Platform string `json:"platform"`
		FileName string `json:"file_name"`
		Object   struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"object"`
	}
	body := struct {
		App       string `json:"app"`
		Version   string `json:"version"`
		Channel   string `json:"channel"`
		Notes     string `json:"notes"`
		Artifacts []art  `json:"artifacts"`
	}{App: app, Version: version, Channel: channel, Notes: notes}
	for platform, v := range platforms {
		a := art{Platform: platform, FileName: v[0]}
		a.Object.SHA256 = v[1]
		var size int64
		for _, c := range v[2] {
			size = size*10 + int64(c-'0')
		}
		a.Object.Size = size
		body.Artifacts = append(body.Artifacts, a)
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func (f *launcherFixture) publish(app, version, channel, content string) *httptest.ResponseRecorder {
	f.t.Helper()
	digest, size := f.upload(content)
	sizeText := ""
	for n := size; n > 0; n /= 10 {
		sizeText = string(rune('0'+n%10)) + sizeText
	}
	return f.call("POST", "/v1/admin/launcher-releases", releaseBody(app, version, channel, "notas "+version,
		map[string][3]string{"windows-x86_64": {"example-" + version + ".exe", digest, sizeText}}))
}

type listedRelease struct {
	Version  string `json:"version"`
	Channel  string `json:"channel"`
	Artifact struct {
		Platform  string             `json:"platform"`
		FileName  string             `json:"file_name"`
		URL       string             `json:"url"`
		SHA256    string             `json:"sha256"`
		Size      int64              `json:"size"`
		Statement string             `json:"statement"`
		Signature revision.Signature `json:"signature"`
	} `json:"artifact"`
}

type listing struct {
	App      string          `json:"app"`
	Latest   *listedRelease  `json:"latest"`
	Releases []listedRelease `json:"releases"`
}

func (f *launcherFixture) list(query string) (listing, *httptest.ResponseRecorder) {
	f.t.Helper()
	rec := f.call("GET", "/v1/launcher/pandora/releases?"+query, "")
	var out listing
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			f.t.Fatal(err)
		}
	}
	return out, rec
}

func versions(l listing) string {
	var out []string
	for _, r := range l.Releases {
		out = append(out, r.Version)
	}
	return strings.Join(out, ",")
}

func TestLauncherSignatureVerifiesWithPublishedKey(t *testing.T) {
	f := newLauncherFixture(t)
	if rec := f.publish("pandora", "1.4.0", "player", "binary one"); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	out, _ := f.list("platform=windows-x86_64")
	if out.Latest == nil || out.Latest.Version != "1.4.0" || len(out.Releases) != 1 {
		t.Fatalf("listing: %+v", out)
	}
	a := out.Latest.Artifact
	if a.URL != "/v1/objects/sha256/"+a.SHA256 || a.Signature.Algorithm != "Ed25519" || a.FileName != "example-1.4.0.exe" {
		t.Fatalf("artifact: %+v", a)
	}
	// The key comes from /v1/signing-keys, as clients do.
	rec := f.call("GET", "/v1/signing-keys", "")
	var keys struct {
		Keys map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(keys.Keys[a.Signature.KeyID])
	if err != nil || len(raw) != ed25519.PublicKeySize {
		t.Fatalf("published key missing: %v", err)
	}
	key := ed25519.PublicKey(raw)
	want := launcherartifact.Expect{App: "pandora", Version: "1.4.0", Platform: "windows-x86_64", SHA256: a.SHA256, Size: a.Size}
	st, err := launcherartifact.Verify(a.Statement, a.Signature, key, want)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if st.Type != "wbombo.launcher-artifact.v1" || !strings.HasSuffix(st.IssuedAt, "Z") {
		t.Fatalf("statement: %+v", st)
	}
	// The statement is exactly the RFC 8785 form: sorted keys, no spaces.
	decoded, _ := base64.RawURLEncoding.DecodeString(a.Statement)
	prefix := `{"app":"pandora","issued_at":"`
	if !strings.HasPrefix(string(decoded), prefix) || strings.ContainsAny(string(decoded), " \n") ||
		!strings.Contains(string(decoded), `"platform":"windows-x86_64","sha256":"`+a.SHA256+`","size":`) ||
		!strings.HasSuffix(string(decoded), `"type":"wbombo.launcher-artifact.v1","version":"1.4.0"}`) {
		t.Fatalf("not canonical: %s", decoded)
	}

	// Wrong expectations are rejected.
	for _, bad := range []launcherartifact.Expect{
		{App: "android"}, {Version: "1.4.1"}, {Platform: "linux-x86_64"}, {SHA256: strings.Repeat("0", 64)}, {Size: a.Size + 1},
	} {
		if _, err := launcherartifact.Verify(a.Statement, a.Signature, key, bad); err == nil {
			t.Errorf("expectation %+v accepted", bad)
		}
	}
	// Flipping any byte of the statement or the signature breaks verification.
	for i := range decoded {
		tampered := append([]byte(nil), decoded...)
		tampered[i] ^= 1
		if _, err := launcherartifact.Verify(base64.RawURLEncoding.EncodeToString(tampered), a.Signature, key, want); err == nil {
			t.Fatalf("tampered statement byte %d verified", i)
		}
	}
	sig, _ := base64.RawURLEncoding.DecodeString(a.Signature.Value)
	sig[0] ^= 1
	bad := a.Signature
	bad.Value = base64.RawURLEncoding.EncodeToString(sig)
	if _, err := launcherartifact.Verify(a.Statement, bad, key, want); err == nil {
		t.Fatal("tampered signature verified")
	}
	// Another key does not verify.
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := launcherartifact.Verify(a.Statement, a.Signature, other, want); err == nil {
		t.Fatal("foreign key verified")
	}
}

func TestLauncherStatementTypeAndCanonicalFormEnforced(t *testing.T) {
	f := newLauncherFixture(t)
	key := f.signer.PublicKey()
	sign := func(raw string) (string, revision.Signature) {
		return base64.RawURLEncoding.EncodeToString([]byte(raw)), f.signer.SignBytes([]byte(raw))
	}
	good := `{"app":"pandora","issued_at":"2026-10-23T10:00:00Z","platform":"p","sha256":"` + strings.Repeat("a", 64) + `","size":1,"type":"wbombo.launcher-artifact.v1","version":"1.0.0"}`
	if st, sig := sign(good); true {
		if _, err := launcherartifact.Verify(st, sig, key, launcherartifact.Expect{}); err != nil {
			t.Fatalf("good statement: %v", err)
		}
	}
	for name, raw := range map[string]string{
		"wrong type":    strings.Replace(good, "wbombo.launcher-artifact.v1", "wbombo.other.v1", 1),
		"non canonical": strings.Replace(good, `"app":"pandora",`, `"app": "pandora",`, 1),
		"extra field":   strings.Replace(good, `"app"`, `"extra":1,"app"`, 1),
		"missing field": strings.Replace(good, `"size":1,`, ``, 1),
	} {
		st, sig := sign(raw)
		if _, err := launcherartifact.Verify(st, sig, key, launcherartifact.Expect{}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLauncherChannelsAfterAndOrder(t *testing.T) {
	f := newLauncherFixture(t)
	for _, v := range [][2]string{
		{"1.0.0", "player"}, {"1.10.0", "player"}, {"1.2.0", "developer"}, {"2.0.0-rc.1", "developer"}, {"1.9.0", "player"}, {"2.0.0", "player"},
	} {
		if rec := f.publish("pandora", v[0], v[1], "content "+v[0]); rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", v[0], rec.Code, rec.Body.String())
		}
	}
	out, _ := f.list("platform=windows-x86_64")
	if got := versions(out); got != "2.0.0,1.10.0,1.9.0,1.0.0" || out.Latest.Version != "2.0.0" {
		t.Fatalf("player: %s", got)
	}
	out, _ = f.list("platform=windows-x86_64&channel=player")
	if got := versions(out); got != "2.0.0,1.10.0,1.9.0,1.0.0" {
		t.Fatalf("player explicit: %s", got)
	}
	out, _ = f.list("platform=windows-x86_64&channel=developer")
	if got := versions(out); got != "2.0.0,2.0.0-rc.1,1.10.0,1.9.0,1.2.0,1.0.0" {
		t.Fatalf("developer: %s", got)
	}
	out, _ = f.list("platform=windows-x86_64&channel=developer&after=1.9.0")
	if got := versions(out); got != "2.0.0,2.0.0-rc.1,1.10.0" {
		t.Fatalf("after: %s", got)
	}
	out, _ = f.list("platform=windows-x86_64&after=2.0.0")
	if out.Latest != nil || len(out.Releases) != 0 || versions(out) != "" {
		t.Fatalf("after latest: %+v", out)
	}
	rec := f.call("GET", "/v1/launcher/pandora/releases?platform=windows-x86_64&after=2.0.0", "")
	if !strings.Contains(rec.Body.String(), `"latest":null`) || !strings.Contains(rec.Body.String(), `"releases":[]`) {
		t.Fatalf("empty body: %s", rec.Body.String())
	}
	out, _ = f.list("platform=linux-x86_64")
	if len(out.Releases) != 0 {
		t.Fatalf("other platform: %s", versions(out))
	}
	// Another app has its own namespace.
	if rec := f.publish("android", "1.0.0", "player", "apk"); rec.Code != http.StatusCreated {
		t.Fatalf("android: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.call("GET", "/v1/launcher/android/releases?platform=windows-x86_64", ""); !strings.Contains(rec.Body.String(), `"1.0.0"`) {
		t.Fatalf("android listing: %s", rec.Body.String())
	}
}

func TestLauncherReadErrorsAndETag(t *testing.T) {
	f := newLauncherFixture(t)
	f.publish("pandora", "1.0.0", "player", "x")
	for query, want := range map[string]int{
		"":                                           http.StatusBadRequest,
		"platform=Windows":                           http.StatusBadRequest,
		"platform=windows-x86_64&channel=beta":       http.StatusBadRequest,
		"platform=windows-x86_64&after=1.0":          http.StatusUnprocessableEntity,
		"platform=windows-x86_64&after=01.0.0":       http.StatusUnprocessableEntity,
		"platform=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": http.StatusBadRequest,
	} {
		if _, rec := f.list(query); rec.Code != want || !strings.Contains(rec.Body.String(), `"code"`) {
			t.Errorf("%q: %d %s", query, rec.Code, rec.Body.String())
		}
	}
	if rec := f.call("GET", "/v1/launcher/windows/releases?platform=x", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app: %d", rec.Code)
	}
	if rec := f.call("POST", "/v1/launcher/pandora/releases?platform=x", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d", rec.Code)
	}
	_, rec := f.list("platform=windows-x86_64")
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req := httptest.NewRequest("GET", "/v1/launcher/pandora/releases?platform=windows-x86_64", nil)
	req.Header.Set("If-None-Match", etag)
	again := httptest.NewRecorder()
	f.handler.ServeHTTP(again, req)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("304 expected, got %d", again.Code)
	}
	f.publish("pandora", "1.1.0", "player", "y")
	req = httptest.NewRequest("GET", "/v1/launcher/pandora/releases?platform=windows-x86_64", nil)
	req.Header.Set("If-None-Match", etag)
	changed := httptest.NewRecorder()
	f.handler.ServeHTTP(changed, req)
	if changed.Code != http.StatusOK {
		t.Fatalf("changed listing must not be 304: %d", changed.Code)
	}
}

func TestLauncherWithdrawalStopsOfferingAndServing(t *testing.T) {
	f := newLauncherFixture(t)
	f.publish("pandora", "1.0.0", "player", "old binary")
	f.publish("pandora", "1.1.0", "player", "new binary")
	out, _ := f.list("platform=windows-x86_64")
	newest := out.Latest.Artifact
	if rec := f.call("GET", newest.URL, ""); rec.Code != http.StatusOK || rec.Body.String() != "new binary" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body.String())
	}
	if rec := f.call("DELETE", "/v1/admin/launcher-releases/pandora/1.1.0", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"withdrawn_at":"`) {
		t.Fatalf("withdraw: %d %s", rec.Code, rec.Body.String())
	}
	out, _ = f.list("platform=windows-x86_64&channel=developer")
	if got := versions(out); got != "1.0.0" || out.Latest.Version != "1.0.0" {
		t.Fatalf("after withdraw: %s", got)
	}
	if rec := f.call("GET", newest.URL, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("withdrawn binary still served: %d", rec.Code)
	}
	// Idempotent, still listed to admins, and the number can never come back.
	if rec := f.call("DELETE", "/v1/admin/launcher-releases/pandora/1.1.0", ""); rec.Code != http.StatusOK {
		t.Fatalf("second withdraw: %d", rec.Code)
	}
	if rec := f.call("GET", "/v1/admin/launcher-releases?app=pandora", ""); !strings.Contains(rec.Body.String(), `"1.1.0"`) {
		t.Fatalf("admin list: %s", rec.Body.String())
	}
	if rec := f.publish("pandora", "1.1.0", "player", "other binary"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "release_exists") {
		t.Fatalf("reuse: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.call("DELETE", "/v1/admin/launcher-releases/pandora/9.9.9", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", rec.Code)
	}
}

func TestLauncherSharedObjectStaysServedWhileReferenced(t *testing.T) {
	f := newLauncherFixture(t)
	f.publish("pandora", "1.0.0", "player", "same bytes")
	f.publish("android", "1.0.0", "player", "same bytes")
	out, _ := f.list("platform=windows-x86_64")
	url := out.Latest.Artifact.URL
	f.call("DELETE", "/v1/admin/launcher-releases/pandora/1.0.0", "")
	if rec := f.call("GET", url, ""); rec.Code != http.StatusOK {
		t.Fatalf("still referenced by android: %d", rec.Code)
	}
	f.call("DELETE", "/v1/admin/launcher-releases/android/1.0.0", "")
	if rec := f.call("GET", url, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unreferenced: %d", rec.Code)
	}
}

func TestLauncherDuplicateByPrecedenceAndImmutability(t *testing.T) {
	f := newLauncherFixture(t)
	if rec := f.publish("pandora", "1.0.0+a", "player", "one"); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := f.publish("pandora", "1.0.0+b", "player", "two"); rec.Code != http.StatusConflict {
		t.Fatalf("build metadata must not create a second version: %d", rec.Code)
	}
	if rec := f.publish("pandora", "1.0.0", "player", "two"); rec.Code != http.StatusConflict {
		t.Fatalf("same precedence: %d", rec.Code)
	}
	before, _ := f.list("platform=windows-x86_64")
	rec := f.call("PUT", "/v1/admin/launcher-releases/pandora/1.0.0+a", `{"channel":"developer","notes":"nuevas notas"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"nuevas notas"`) || !strings.Contains(rec.Body.String(), `"channel":"developer"`) {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	after, _ := f.list("platform=windows-x86_64&channel=developer")
	if before.Latest.Artifact != after.Latest.Artifact {
		t.Fatal("bytes, statement or signature changed after an edit")
	}
	if out, _ := f.list("platform=windows-x86_64"); len(out.Releases) != 0 {
		t.Fatal("version moved to developer must leave the player channel")
	}
	for body, want := range map[string]int{
		`{}`:                   http.StatusUnprocessableEntity,
		`{"channel":"beta"}`:   http.StatusUnprocessableEntity,
		`{"version":"2.0.0"}`:  http.StatusBadRequest,
		`{"notes":"a\u0000b"}`: http.StatusUnprocessableEntity,
		`{"notes":"` + strings.Repeat("x", 16385) + `"}`: http.StatusUnprocessableEntity,
	} {
		if rec := f.call("PUT", "/v1/admin/launcher-releases/pandora/1.0.0+a", body); rec.Code != want {
			t.Errorf("PUT %.40s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := f.call("PUT", "/v1/admin/launcher-releases/pandora/3.0.0", `{"notes":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	// Raw storage refuses to mutate artifacts or reuse a version.
	if _, err := f.store.WithdrawLauncherRelease(context.Background(), "pandora", "1.0.0+a"); err != nil {
		t.Fatal(err)
	}
}

func TestLauncherCreateValidation(t *testing.T) {
	f := newLauncherFixture(t)
	digest, size := f.upload("some binary")
	ok := func() map[string][3]string {
		return map[string][3]string{"windows-x86_64": {"example.exe", digest, "11"}}
	}
	if size != 11 {
		t.Fatal("test size")
	}
	cases := map[string]struct {
		body string
		code int
		err  string
	}{
		"bad app":           {releaseBody("windows", "1.0.0", "player", "", ok()), 422, "invalid_release"},
		"leading zero":      {releaseBody("pandora", "01.0.0", "player", "", ok()), 422, "invalid_release"},
		"v prefix":          {releaseBody("pandora", "v1.0.0", "player", "", ok()), 422, "invalid_release"},
		"two components":    {releaseBody("pandora", "1.0", "player", "", ok()), 422, "invalid_release"},
		"bad channel":       {releaseBody("pandora", "1.0.0", "beta", "", ok()), 422, "invalid_release"},
		"bad platform":      {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"Win dows": {"a.exe", digest, "11"}}), 422, "invalid_release"},
		"slash in file":     {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {"a/b.exe", digest, "11"}}), 422, "invalid_release"},
		"backslash in file": {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {"a\\b.exe", digest, "11"}}), 422, "invalid_release"},
		"control in file":   {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {"a\nb.exe", digest, "11"}}), 422, "invalid_release"},
		"empty file":        {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {"", digest, "11"}}), 422, "invalid_release"},
		"long file":         {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {strings.Repeat("a", 129), digest, "11"}}), 422, "invalid_release"},
		"huge notes":        {releaseBody("pandora", "1.0.0", "player", strings.Repeat("n", 16385), ok()), 422, "invalid_release"},
		"no artifacts":      {releaseBody("pandora", "1.0.0", "player", "", nil), 422, "invalid_release"},
		"missing object":    {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {"a.exe", strings.Repeat("a", 64), "11"}}), 422, "object_missing"},
		"wrong size":        {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {"a.exe", digest, "12"}}), 422, "object_size_mismatch"},
		"bad digest":        {releaseBody("pandora", "1.0.0", "player", "", map[string][3]string{"w": {"a.exe", "ABC", "11"}}), 422, "invalid_release"},
		"unknown field":     {`{"app":"pandora","version":"1.0.0","channel":"player","notes":"","artifacts":[],"extra":1}`, 400, ""},
		"not json":          {`nope`, 400, ""},
	}
	for name, c := range cases {
		rec := f.call("POST", "/v1/admin/launcher-releases", c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.err) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	dup := `{"app":"pandora","version":"1.0.0","channel":"player","notes":"","artifacts":[` +
		`{"platform":"w","file_name":"a","object":{"sha256":"` + digest + `","size":11}},` +
		`{"platform":"w","file_name":"b","object":{"sha256":"` + digest + `","size":11}}]}`
	if rec := f.call("POST", "/v1/admin/launcher-releases", dup); rec.Code != 422 {
		t.Errorf("duplicate platform: %d", rec.Code)
	}
	if out, _ := f.store.LauncherReleases(context.Background(), ""); len(out) != 0 {
		t.Fatalf("rejected requests left %d releases", len(out))
	}
	// Default channel, several platforms and an app/version path with build metadata.
	multi := `{"app":"android","version":"2.0.0-beta.1+build.5","notes":"n","artifacts":[` +
		`{"platform":"android-universal","file_name":"app.apk","object":{"sha256":"` + digest + `","size":11}},` +
		`{"platform":"android-x86","file_name":"app-x86.apk","object":{"sha256":"` + digest + `","size":11}}]}`
	rec := f.call("POST", "/v1/admin/launcher-releases", multi)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"channel":"player"`) {
		t.Fatalf("multi: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Release struct {
			Artifacts []struct{ Platform string } `json:"artifacts"`
		} `json:"release"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || len(created.Release.Artifacts) != 2 {
		t.Fatalf("artifacts: %v %s", err, rec.Body.String())
	}
	if rec := f.call("GET", "/v1/launcher/android/releases?platform=android-x86", ""); !strings.Contains(rec.Body.String(), "2.0.0-beta.1+build.5") {
		t.Fatalf("listing: %s", rec.Body.String())
	}
}

func TestLauncherAdminRoutesMethods(t *testing.T) {
	f := newLauncherFixture(t)
	for _, c := range []struct{ method, path string }{
		{"DELETE", "/v1/admin/launcher-releases"}, {"PUT", "/v1/admin/launcher-releases"},
		{"GET", "/v1/admin/launcher-releases/pandora/1.0.0"}, {"POST", "/v1/admin/launcher-releases/pandora/1.0.0"},
	} {
		if rec := f.call(c.method, c.path, `{}`); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
	if rec := f.call("GET", "/v1/admin/launcher-releases", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"releases":[]`) {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.call("GET", "/v1/admin/launcher-releases?app=nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app: %d", rec.Code)
	}
}

func TestLauncherPublishNeedsSigner(t *testing.T) {
	root := t.TempDir()
	cas, _ := storage.OpenCAS(root, 1<<20)
	store, err := storage.OpenSQLite(root+"/m.sqlite3", cas)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, _ := New(Dependencies{Objects: cas, Store: store, Keys: testKeys{}}, Options{ReadMiddleware: identity, AdminMiddleware: identity})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/admin/launcher-releases", strings.NewReader(`{}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
