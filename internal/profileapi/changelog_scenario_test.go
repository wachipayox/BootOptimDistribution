package profileapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wachipayox/BootOptimDistribution/internal/revision"
)

// chainScenario builds root R1,R2; child C1 (born on R2); R3 followed as C2;
// manual C3 (drops mod_a); grandchild G1; R4 followed as C4 and G2.
type chainScenario struct {
	env            *changelogEnv
	r1, r2, r3, r4 publishedRev
	c1, c2, c3, c4 string
	g1, g2         string
}

func buildChainScenario(t *testing.T) *chainScenario {
	env := newChangelogEnv(t)
	a1, a2 := env.stage("a one"), env.stage("a two")
	b1, b2 := env.stage("b one"), env.stage("b two")
	c := env.stage("c one")
	d1, d2 := env.stage("d one"), env.stage("d two")
	s := &chainScenario{env: env}
	follow := func(p publishedRev, profile string) string {
		t.Helper()
		for _, f := range p.AutoFollow {
			if f.ProfileID == profile && f.Status == "published" {
				return f.Revision.RevisionID
			}
		}
		t.Fatalf("%s was not auto-followed: %+v", profile, p.AutoFollow)
		return ""
	}

	s.r1 = env.publish(clManifest("profile_root", "rev_r000000000000001", 1, nil, "Primera\n- uno\n- dos\n\nDetalle largo",
		clModEntry("mod_a", "a-1.jar", a1), clModEntry("mod_b", "b-1.jar", b1)))
	s.r2 = env.publish(clManifest("profile_root", "rev_r000000000000002", 2, nil, "Segunda",
		clModEntry("mod_a", "a-2.jar", a2), clModEntry("mod_b", "b-1.jar", b1)))
	c1 := env.publish(clManifest("profile_child", "rev_c000000000000001", 1, pin(s.r2, "profile_root"), "Beta inicial", clModEntry("mod_c", "c-1.jar", c)))
	s.c1 = c1.Revision.RevisionID
	s.r3 = env.publish(clManifest("profile_root", "rev_r000000000000003", 3, nil, "Tercera",
		clModEntry("mod_a", "a-2.jar", a2), clModEntry("mod_b", "b-2.jar", b2), clModEntry("mod_d", "d-1.jar", d1)))
	s.c2 = follow(s.r3, "profile_child")
	c3m := clManifest("profile_child", "rev_c000000000000003", 3, pin(s.r3, "profile_root"), "Quitar A", clModEntry("mod_c", "c-1.jar", c))
	c3m.RemoveMods = []revision.RemoveMod{{ID: "mod_a", ExpectBaseObjectSHA256: a2.SHA256}}
	c3 := env.publish(c3m)
	s.c3 = c3.Revision.RevisionID
	g1 := env.publish(clManifest("profile_grand", "rev_g000000000000001", 1, pin(c3, "profile_child"), "Nieto"))
	s.g1 = g1.Revision.RevisionID
	s.r4 = env.publish(clManifest("profile_root", "rev_r000000000000004", 4, nil, "Cuarta",
		clModEntry("mod_a", "a-2.jar", a2), clModEntry("mod_b", "b-2.jar", b2), clModEntry("mod_d", "d-2.jar", d2)))
	s.c4 = follow(s.r4, "profile_child")
	s.g2 = follow(s.r4, "profile_grand")
	return s
}

func TestChangelogOriginAttributionAndAncestorHistory(t *testing.T) {
	s := buildChainScenario(t)
	got, rec := s.env.changelog("/v1/profiles/profile_child/changelog")
	if rec.Code != http.StatusOK || got.SchemaVersion != 1 || got.ProfileID != "profile_child" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	r := func(p publishedRev) string { return p.Revision.RevisionID }
	want := []string{r(s.r4), s.c3, r(s.r3), s.c1, r(s.r2), r(s.r1)}
	if strings.Join(originsOf(got.Entries), " ") != strings.Join(want, " ") {
		t.Fatalf("origins %v, want %v", originsOf(got.Entries), want)
	}
	// R4 arrived through the auto revision C4 and appears once, attributed to the parent.
	e := got.Entries[0]
	if e.Origin.ProfileID != "profile_root" || e.Origin.Sequence != 4 || e.Revision == nil || e.Revision.RevisionID != s.c4 || e.Revision.Sequence != 4 {
		t.Fatalf("R4 entry: %+v", e)
	}
	if e.Title != "Cuarta" || e.CreatedAt != "2026-10-04T12:00:00Z" {
		t.Fatalf("R4 text/date: %+v", e)
	}
	if got.Entries[2].Revision == nil || got.Entries[2].Revision.RevisionID != s.c2 {
		t.Fatalf("R3 should be brought by C2: %+v", got.Entries[2])
	}
	// Parents' revisions from before the child existed carry no revision of this profile.
	for _, i := range []int{4, 5} {
		if got.Entries[i].Revision != nil || got.Entries[i].Origin.ProfileID != "profile_root" {
			t.Fatalf("entry %d: %+v", i, got.Entries[i])
		}
	}
	if len(got.Lineage) != 2 || got.Lineage[0] != (lineageView{"profile_child", "Perfil profile_child", 0}) || got.Lineage[1].ProfileID != "profile_root" || got.Lineage[1].Depth != 1 {
		t.Fatalf("lineage: %+v", got.Lineage)
	}
	if got.Entries[5].Title != "Primera" || strings.Join(got.Entries[5].Changes, "|") != "uno|dos" || got.Entries[5].Desc != "Detalle largo" {
		t.Fatalf("derived text: %+v", got.Entries[5])
	}
	if strings.Contains(rec.Body.String(), "auto: sigue") || strings.Contains(rec.Body.String(), `"changes":null`) {
		t.Fatalf("body leaks auto notes or null lists: %s", rec.Body.String())
	}

	// Two levels of auto-follow: the grandchild's G2 is attributed to the root revision, once.
	grand, rec := s.env.changelog("/v1/profiles/profile_grand/changelog")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	wantGrand := []string{r(s.r4), s.g1, s.c3, r(s.r3), s.c1, r(s.r2), r(s.r1)}
	if strings.Join(originsOf(grand.Entries), " ") != strings.Join(wantGrand, " ") {
		t.Fatalf("grandchild origins %v, want %v", originsOf(grand.Entries), wantGrand)
	}
	if grand.Entries[0].Revision == nil || grand.Entries[0].Revision.RevisionID != s.g2 || grand.Entries[0].Revision.Sequence != 2 {
		t.Fatalf("grandchild R4 entry: %+v", grand.Entries[0])
	}
	if len(grand.Lineage) != 3 || grand.Lineage[1].ProfileID != "profile_child" || grand.Lineage[2].ProfileID != "profile_root" {
		t.Fatalf("grandchild lineage: %+v", grand.Lineage)
	}
	// The root never lists revisions of its descendants.
	root, _ := s.env.changelog("/v1/profiles/profile_root/changelog")
	if len(root.Entries) != 4 || len(root.Lineage) != 1 {
		t.Fatalf("root changelog: %+v", root)
	}
}

func TestChangelogModsDiff(t *testing.T) {
	s := buildChainScenario(t)
	got, _ := s.env.changelog("/v1/profiles/profile_child/changelog")
	check := func(i int, added, removed, updated string) {
		t.Helper()
		m := got.Entries[i].Mods
		var up []string
		for _, u := range m.Updated {
			up = append(up, u.ID+":"+u.From+">"+u.To)
		}
		if modFiles(m.Added) != added || modFiles(m.Removed) != removed || strings.Join(up, ",") != updated {
			t.Fatalf("entry %d mods: %+v (want +%q -%q ~%q)", i, m, added, removed, updated)
		}
	}
	check(0, "", "", "mod_d:d-1.jar>d-2.jar")              // R4 via auto C4: inherited update
	check(1, "", "mod_a=a-2.jar", "")                      // C3 removes an inherited mod
	check(2, "mod_d=d-1.jar", "", "mod_b:b-1.jar>b-2.jar") // R3 via auto C2
	check(3, "mod_c=c-1.jar", "", "")                      // C1: first revision against its branch point
	check(4, "", "", "mod_a:a-1.jar>a-2.jar")              // R2, ancestor entry without revision
	check(5, "mod_a=a-1.jar,mod_b=b-1.jar", "", "")        // R1: root, everything is new
	if m := got.Entries[0].Mods; m.Added == nil || m.Removed == nil || m.Updated == nil {
		t.Fatalf("empty lists must encode as []: %+v", m)
	}
}

func TestChangelogWindowRevisionPaginationAndETag(t *testing.T) {
	s := buildChainScenario(t)
	base := "/v1/profiles/profile_child/changelog"

	got, rec := s.env.changelog(base + "?after=" + s.c1 + "&until=" + s.c3)
	if rec.Code != 200 || len(got.Entries) != 2 || got.Entries[0].Origin.RevisionID != s.c3 || got.Entries[1].Revision.RevisionID != s.c2 {
		t.Fatalf("window (c1,c3]: %d %s", rec.Code, rec.Body.String())
	}
	got, _ = s.env.changelog(base + "?after=" + s.c3)
	if len(got.Entries) != 1 || got.Entries[0].Revision.RevisionID != s.c4 {
		t.Fatalf("default until is the latest revision: %+v", got.Entries)
	}
	if got, _ = s.env.changelog(base + "?after=" + s.c4); len(got.Entries) != 0 || got.Entries == nil {
		t.Fatalf("nothing newer must be an empty array: %+v", got)
	}
	for _, q := range []string{"?after=" + s.r1.Revision.RevisionID, "?after=" + s.c1 + "&until=rev_unknown", "?revision=" + s.g1} {
		if _, rec := s.env.changelog(base + q); rec.Code != 422 || !strings.Contains(rec.Body.String(), "revision_not_in_profile") {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
	}

	one, rec := s.env.changelog(base + "?revision=" + s.c2)
	if rec.Code != 200 || len(one.Entries) != 1 || one.Entries[0].Title != "Tercera" || one.Entries[0].Revision.RevisionID != s.c2 || one.NextCursor != nil {
		t.Fatalf("revision=C2: %d %s", rec.Code, rec.Body.String())
	}
	one, _ = s.env.changelog(base + "?revision=" + s.r2.Revision.RevisionID)
	if len(one.Entries) != 1 || one.Entries[0].Revision != nil || one.Entries[0].Title != "Segunda" {
		t.Fatalf("revision=<ancestor>: %+v", one.Entries)
	}

	full, _ := s.env.changelog(base)
	var paged []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		q := base + "?limit=2"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		page, rec := s.env.changelog(q)
		if rec.Code != 200 || len(page.Entries) > 2 {
			t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
		}
		paged = append(paged, originsOf(page.Entries)...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if strings.Join(paged, " ") != strings.Join(originsOf(full.Entries), " ") {
		t.Fatalf("pagination %v != %v", paged, originsOf(full.Entries))
	}
	for _, q := range []string{"?limit=0", "?limit=201", "?limit=x"} {
		if rec := s.env.call("GET", base+q, ""); rec.Code != 422 || !strings.Contains(rec.Body.String(), "invalid_limit") {
			t.Fatalf("%s: %d", q, rec.Code)
		}
	}
	if rec := s.env.call("GET", base+"?cursor=bogus", ""); rec.Code != 422 || !strings.Contains(rec.Body.String(), "invalid_cursor") {
		t.Fatalf("bad cursor: %d", rec.Code)
	}
	if rec := s.env.call("GET", "/v1/profiles/profile_missing/changelog", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "profile_not_found") {
		t.Fatalf("missing profile: %d %s", rec.Code, rec.Body.String())
	}

	first := s.env.call("GET", base, "")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("changelog must carry an ETag")
	}
	if rec := s.env.call("GET", base, "", "If-None-Match", etag); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("If-None-Match: %d %q", rec.Code, rec.Body.String())
	}
	// Editing the text changes the document and therefore the ETag.
	if rec := s.env.call("PUT", "/v1/admin/revisions/"+s.r4.Revision.RevisionID+"/changelog", `{"title":"Nuevo","description":"","changes":["x"]}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec := s.env.call("GET", base, "", "If-None-Match", etag); rec.Code != 200 || rec.Header().Get("ETag") == etag {
		t.Fatalf("edited changelog must not be 304: %d", rec.Code)
	}
}

func changeList(n int, size int) []string {
	list := make([]string, n)
	for i := range list {
		list[i] = strings.Repeat("c", size)
	}
	return list
}

func TestPutRevisionChangelogLimitsAndPrecedence(t *testing.T) {
	s := buildChainScenario(t)
	id := s.r3.Revision.RevisionID
	put := func(rev, body string) *httptest.ResponseRecorder {
		return s.env.call("PUT", "/v1/admin/revisions/"+rev+"/changelog", body)
	}
	enc := func(title, desc string, changes []string) string {
		raw, _ := json.Marshal(map[string]any{"title": title, "description": desc, "changes": changes})
		return string(raw)
	}
	ok := []string{
		enc("", "", []string{}),
		enc(strings.Repeat("t", 120), strings.Repeat("d", 8192), []string{strings.Repeat("c", 300)}),
		enc("Título", "Uno\n\nDos", changeList(100, 1)),
		`{"title":"sin changes","description":""}`,
	}
	for _, body := range ok {
		if rec := put(id, body); rec.Code != 200 {
			t.Fatalf("accepted body rejected: %d %s", rec.Code, rec.Body.String())
		}
	}
	bad := map[string]string{
		"title 121":     enc(strings.Repeat("t", 121), "", nil),
		"title newline": enc("a\nb", "", nil),
		"desc 8193":     enc("", strings.Repeat("d", 8193), nil),
		"101 changes":   enc("", "", changeList(101, 1)),
		"change 301":    enc("", "", changeList(1, 301)),
		"change empty":  enc("", "", []string{" "}),
		"change nl":     enc("", "", []string{"a\nb"}),
	}
	for name, body := range bad {
		if rec := put(id, body); rec.Code != 422 || !strings.Contains(rec.Body.String(), "invalid_changelog") {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := put(id, `{"title":"x","extra":1}`); rec.Code != 400 {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if rec := put("rev_unknown", enc("x", "", nil)); rec.Code != 404 || !strings.Contains(rec.Body.String(), "revision_not_found") {
		t.Fatalf("unknown revision: %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.env.call("GET", "/v1/admin/revisions/"+id+"/changelog", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on admin route: %d", rec.Code)
	}

	// The stored text wins over the signed notes, also through auto-follow.
	if rec := put(id, enc("Create 6.0.1", "Motivo", []string{"Uno", "Dos"})); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	got, _ := s.env.changelog("/v1/profiles/profile_child/changelog?revision=" + s.c2)
	e := got.Entries[0]
	if e.Title != "Create 6.0.1" || e.Desc != "Motivo" || strings.Join(e.Changes, "|") != "Uno|Dos" {
		t.Fatalf("stored text not used: %+v", e)
	}
}
