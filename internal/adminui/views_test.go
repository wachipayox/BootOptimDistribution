package adminui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// Every routed view must be a sibling: a view left inside another (an
// unclosed section) stays hidden and the page renders empty.
func TestViewsAreNotNested(t *testing.T) {
	handler := NewHandler(EmptyReadModel{}, BuildInfo{Version: "test", Commit: "test"})
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	body := page.Body.String()
	tag := regexp.MustCompile(`<section\b[^>]*>|</section>`)
	view := regexp.MustCompile(`data-view="([a-z]+)"`)
	depth, base, seen := 0, -1, 0
	for _, m := range tag.FindAllString(body, -1) {
		if strings.HasPrefix(m, "</") {
			depth--
			continue
		}
		if v := view.FindStringSubmatch(m); v != nil {
			if base < 0 {
				base = depth
			}
			if depth != base {
				t.Errorf("view %q is nested at depth %d (views start at %d)", v[1], depth, base)
			}
			seen++
		}
		depth++
	}
	if depth != 0 || seen < 7 {
		t.Fatalf("unbalanced sections (%d) or missing views (%d)", depth, seen)
	}
}
