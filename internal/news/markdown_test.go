package news

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

const vectorsPath = "../../docs/news-markdown-vectors.json"

type vectorFile struct {
	SchemaVersion int             `json:"schema_version"`
	Description   string          `json:"description"`
	Notes         json.RawMessage `json:"notes"`
	CASExisting   []string        `json:"cas_existing"`
	Cases         []vectorCase    `json:"cases"`
}

type vectorCase struct {
	Name      string  `json:"name"`
	Markdown  string  `json:"markdown"`
	Accepted  bool    `json:"accepted"`
	Blocks    []Block `json:"blocks,omitempty"`
	Images    *int    `json:"images,omitempty"`
	Videos    *int    `json:"videos,omitempty"`
	ErrorLine int     `json:"error_line,omitempty"`
}

func loadVectors(t *testing.T) (vectorFile, func(string) bool) {
	t.Helper()
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	existing := map[string]bool{}
	for _, s := range f.CASExisting {
		existing[s] = true
	}
	return f, func(s string) bool { return existing[s] }
}

// NEWS_VECTORS_FILL=1 go test ./internal/news -run TestVectors rewrites the
// expected blocks of accepted cases from the validator; review the diff.
func TestVectors(t *testing.T) {
	f, exists := loadVectors(t)
	fill := os.Getenv("NEWS_VECTORS_FILL") == "1"
	if len(f.Cases) < 40 {
		t.Fatalf("expected the shared vectors, got %d cases", len(f.Cases))
	}
	for i := range f.Cases {
		c := &f.Cases[i]
		t.Run(c.Name, func(t *testing.T) {
			info, berr := Validate(c.Markdown, exists)
			if c.Accepted != (berr == nil) {
				t.Fatalf("accepted=%v want %v (err=%v)", berr == nil, c.Accepted, berr)
			}
			if !c.Accepted {
				if c.ErrorLine != berr.Line {
					t.Fatalf("error line %d want %d (%s)", berr.Line, c.ErrorLine, berr.Message)
				}
				return
			}
			images, videos := info.Images, info.Videos
			if fill {
				c.Blocks, c.Images, c.Videos = info.Blocks, &images, &videos
				return
			}
			if c.Images == nil || c.Videos == nil {
				t.Fatal("accepted case lacks images/videos")
			}
			if *c.Images != images || *c.Videos != videos {
				t.Fatalf("images/videos %d/%d want %d/%d", images, videos, *c.Images, *c.Videos)
			}
			want, got := normalize(c.Blocks), normalize(info.Blocks)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("blocks\n got %s\nwant %s", mustJSON(got), mustJSON(want))
			}
		})
	}
	if fill {
		out, err := json.MarshalIndent(f, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func normalize(b []Block) string { return mustJSON(b) }

func mustJSON(v interface{}) string {
	out, _ := json.Marshal(v)
	return string(out)
}

func TestLimits(t *testing.T) {
	exists := func(string) bool { return true }
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"50 images", strings.Repeat("![a](https://example.com/a.png)\n\n", 50), true},
		{"51 images", strings.Repeat("![a](https://example.com/a.png)\n\n", 51), false},
		{"10 videos", strings.Repeat("https://youtu.be/dQw4w9WgXcQ\n\n", 10), true},
		{"11 videos", strings.Repeat("https://youtu.be/dQw4w9WgXcQ\n\n", 11), false},
		{"url 2048", "[x](https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/")) + ")", true},
		{"url 2049", "[x](https://example.com/" + strings.Repeat("a", 2049-len("https://example.com/")) + ")", false},
		{"body at limit", strings.Repeat("a", MaxBodyBytes), true},
		{"body over limit", strings.Repeat("a", MaxBodyBytes+1), false},
		{"invalid utf8", "a\xffb", false},
		{"crlf lines", "Uno\r\n\r\n## Dos\r\n", true},
	}
	for _, c := range cases {
		_, err := Validate(c.body, exists)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestCheckHTTPSURL(t *testing.T) {
	for _, u := range []string{"https://example.com", "https://example.com/a?b=c#d", "https://i.example.com:8443/x"} {
		if err := CheckHTTPSURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"", "http://example.com", "https://", "https:///x", "//example.com", "HTTPS://example.com", "https://a b", "https://u@example.com", "javascript:alert(1)", "https://example.com/\n"} {
		if err := CheckHTTPSURL(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestCASImagesAreCollected(t *testing.T) {
	sha := strings.Repeat("c", 64)
	info, err := Validate("![a](cas:"+sha+")\n\n![b](cas:"+sha+")", func(string) bool { return true })
	if err != nil || len(info.CASImages) != 1 || info.CASImages[0] != sha || info.Images != 2 {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestImageHints(t *testing.T) {
	cases := []struct{ title, size, align string }{
		{"", "", ""},
		{"size=large align=center", "large", "center"},
		{"  align=center\tsize=full ", "full", "center"},
		{"size=huge align=right", "", ""},
		{"size=large size=huge", "", ""},
		{"size=small size=large", "large", ""},
		{"Size=large", "", ""},
		{"una descripcion", "", ""},
	}
	for _, c := range cases {
		size, align := ImageHints(c.title)
		if size != c.size || align != c.align {
			t.Errorf("%q: got %q/%q want %q/%q", c.title, size, align, c.size, c.align)
		}
	}
}
