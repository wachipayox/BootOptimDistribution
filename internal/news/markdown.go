// Package news validates the restricted Markdown body of a news item.
//
// The contract lives in docs/PROFILE_PROTOCOL.md («Cuerpo de la noticia») and
// the cases shared with the launcher clients in docs/news-markdown-vectors.json.
// Nothing that can carry HTML, code or an unsafe URL scheme passes validation.
package news

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

const (
	MaxBodyBytes  = 65536
	MaxURLBytes   = 2048
	MaxImages     = 50
	MaxVideos     = 10
	maxListLevels = 2
)

// BodyError is a rejected body. Line is 1-based.
type BodyError struct {
	Line    int
	Message string
}

func (e *BodyError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Message) }

// Block is the normalized description of an accepted body, shared with the
// clients through the test vectors.
type Block struct {
	Type    string  `json:"type"`
	Text    string  `json:"text,omitempty"`
	Level   int     `json:"level,omitempty"`
	Ordered bool    `json:"ordered,omitempty"`
	Items   []Item  `json:"items,omitempty"`
	Blocks  []Block `json:"blocks,omitempty"`
	Alt     string  `json:"alt,omitempty"`
	Src     string  `json:"src,omitempty"`
	ID      string  `json:"id,omitempty"`
	// Size and Align are the image presentation hints from the CommonMark
	// title ("size=medium align=center"); empty when absent or unknown.
	Size  string `json:"size,omitempty"`
	Align string `json:"align,omitempty"`
}

// Item is one list item.
type Item struct {
	Blocks []Block `json:"blocks"`
}

// Info is the result of validating a body.
type Info struct {
	Blocks []Block
	Images int
	Videos int
	// CASImages are the distinct sha256 digests referenced as cas:<sha256>.
	CASImages []string
	// Links are the distinct https link destinations found in the body.
	Links []string
}

// Validate checks body against the contract. casExists is asked about every
// cas:<sha256> image; it may be nil (then those images are not checked).
func Validate(body string, casExists func(sha256 string) bool) (*Info, *BodyError) {
	v := &validator{src: []byte(body), casExists: casExists, seenCAS: map[string]bool{}, seenLinks: map[string]bool{}}
	return v.run()
}

var (
	md          = goldmark.New()
	refDefRe    = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:`)
	fenceRe     = regexp.MustCompile("^ {0,3}(```|~~~)")
	tableSepRe  = regexp.MustCompile(`^ {0,3}\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)+\|?\s*$|^ {0,3}\|\s*:?-+:?\s*\|?\s*$`)
	youtubeIDRe = `[A-Za-z0-9_-]{11}`
	youtubeRe   = regexp.MustCompile(`^https://(?:(?:www\.|m\.)?youtube\.com/(?:watch\?(?:[^#\s]*&)?v=(` + youtubeIDRe + `)(?:&[^#\s]*)?|shorts/(` + youtubeIDRe + `)(?:\?[^#\s]*)?)|youtu\.be/(` + youtubeIDRe + `)(?:\?[^#\s]*)?)$`)
	casRe       = regexp.MustCompile(`^cas:([0-9a-f]{64})$`)
)

type validator struct {
	src       []byte
	lines     []string
	casExists func(string) bool
	info      Info
	seenCAS   map[string]bool
	seenLinks map[string]bool
	err       *BodyError
}

func (v *validator) fail(line int, format string, args ...interface{}) {
	if v.err == nil {
		if line < 1 {
			line = 1
		}
		v.err = &BodyError{Line: line, Message: fmt.Sprintf(format, args...)}
	}
}

func (v *validator) run() (*Info, *BodyError) {
	if len(v.src) > MaxBodyBytes {
		return nil, &BodyError{Line: 1, Message: fmt.Sprintf("body exceeds %d bytes", MaxBodyBytes)}
	}
	if !utf8.Valid(v.src) {
		return nil, &BodyError{Line: 1, Message: "body is not valid UTF-8"}
	}
	v.lines = strings.Split(strings.ReplaceAll(string(v.src), "\r\n", "\n"), "\n")
	for i, line := range v.lines {
		for _, r := range line {
			if r == 0 || (r < 0x20 && r != '\t' && r != '\r') || r == 0x7f {
				return nil, &BodyError{Line: i + 1, Message: "control character"}
			}
		}
	}
	ctx := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(v.src), parser.WithContext(ctx))
	if refs := ctx.References(); len(refs) > 0 {
		line := 1
		for i, l := range v.lines {
			if refDefRe.MatchString(l) {
				line = i + 1
				break
			}
		}
		return nil, &BodyError{Line: line, Message: "link reference definitions and footnotes are not allowed"}
	}
	blocks := v.blocks(doc, 0)
	if v.err != nil {
		return nil, v.err
	}
	if v.info.Images > MaxImages {
		return nil, &BodyError{Line: 1, Message: fmt.Sprintf("more than %d images", MaxImages)}
	}
	if v.info.Videos > MaxVideos {
		return nil, &BodyError{Line: 1, Message: fmt.Sprintf("more than %d videos", MaxVideos)}
	}
	v.info.Blocks = blocks
	return &v.info, nil
}

// lineOf returns the 1-based line where a node starts (best effort).
func (v *validator) lineOf(n ast.Node) int {
	for node := n; node != nil; node = node.Parent() {
		if t, ok := node.(*ast.Text); ok {
			return v.offsetLine(t.Segment.Start)
		}
		if node.Type() == ast.TypeBlock {
			if lines := node.Lines(); lines != nil && lines.Len() > 0 {
				return v.offsetLine(lines.At(0).Start)
			}
		}
		if node.Type() == ast.TypeBlock {
			// Container without own lines: use its first descendant text.
			if l := v.firstDescendantLine(node); l > 0 {
				return l
			}
		}
	}
	return 1
}

func (v *validator) firstDescendantLine(n ast.Node) int {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			return v.offsetLine(t.Segment.Start)
		}
		if c.Type() == ast.TypeBlock {
			if lines := c.Lines(); lines != nil && lines.Len() > 0 {
				return v.offsetLine(lines.At(0).Start)
			}
		}
		if l := v.firstDescendantLine(c); l > 0 {
			return l
		}
	}
	return 0
}

func (v *validator) offsetLine(offset int) int {
	if offset > len(v.src) {
		offset = len(v.src)
	}
	return 1 + strings.Count(string(v.src[:offset]), "\n")
}

func (v *validator) scanLine(re *regexp.Regexp) int {
	for i, l := range v.lines {
		if re.MatchString(l) {
			return i + 1
		}
	}
	return 1
}

// blocks walks the children of a container and describes them.
func (v *validator) blocks(parent ast.Node, listDepth int) []Block {
	var out []Block
	for n := parent.FirstChild(); n != nil && v.err == nil; n = n.NextSibling() {
		switch node := n.(type) {
		case *ast.Paragraph:
			out = append(out, v.paragraph(node))
		case *ast.TextBlock:
			out = append(out, v.paragraph(node))
		case *ast.Heading:
			if node.Level != 2 && node.Level != 3 {
				v.fail(v.lineOf(node), "heading level %d is not allowed (only ## and ###)", node.Level)
				break
			}
			out = append(out, Block{Type: "heading", Level: node.Level, Text: v.plain(node)})
			v.inlineChecks(node)
		case *ast.ThematicBreak:
			out = append(out, Block{Type: "rule"})
		case *ast.Blockquote:
			out = append(out, Block{Type: "quote", Blocks: v.blocks(node, listDepth)})
		case *ast.List:
			if listDepth+1 > maxListLevels {
				v.fail(v.lineOf(node), "lists nested more than %d levels", maxListLevels)
				break
			}
			b := Block{Type: "list", Ordered: node.IsOrdered()}
			for item := node.FirstChild(); item != nil; item = item.NextSibling() {
				b.Items = append(b.Items, Item{Blocks: v.blocks(item, listDepth+1)})
			}
			out = append(out, b)
		case *ast.HTMLBlock:
			v.fail(v.lineOf(node), "HTML is not allowed")
		case *ast.FencedCodeBlock:
			v.fail(v.scanLine(fenceRe), "code blocks are not allowed")
		case *ast.CodeBlock:
			v.fail(v.lineOf(node), "code blocks are not allowed")
		default:
			v.fail(v.lineOf(n), "unsupported element %s", n.Kind().String())
		}
	}
	return out
}

// paragraph describes a paragraph, recognizing image-only and YouTube-only ones.
func (v *validator) paragraph(p ast.Node) Block {
	v.tableCheck(p)
	if id := v.youtubeID(p); id != "" {
		v.info.Videos++
		return Block{Type: "youtube", ID: id}
	}
	v.inlineChecks(p)
	if only := p.FirstChild(); only != nil && only.NextSibling() == nil {
		if img, ok := only.(*ast.Image); ok {
			size, align := ImageHints(string(img.Title))
			return Block{Type: "image", Alt: v.plain(img), Src: string(img.Destination), Size: size, Align: align}
		}
	}
	return Block{Type: "paragraph", Text: v.plain(p)}
}

func (v *validator) tableCheck(p ast.Node) {
	lines := p.Lines()
	if lines == nil {
		return
	}
	for i := 1; i < lines.Len(); i++ {
		curSeg, prevSeg := lines.At(i), lines.At(i-1)
		cur := strings.TrimRight(string(curSeg.Value(v.src)), "\r\n")
		prev := string(prevSeg.Value(v.src))
		if strings.Contains(cur, "|") && strings.Contains(prev, "|") && tableSepRe.MatchString(cur) {
			v.fail(v.offsetLine(prevSeg.Start), "tables are not allowed")
			return
		}
	}
}

// youtubeID returns the video id when the paragraph is only a YouTube link.
func (v *validator) youtubeID(p ast.Node) string {
	first := p.FirstChild()
	if first == nil {
		return ""
	}
	var candidate string
	if first.NextSibling() == nil {
		switch c := first.(type) {
		case *ast.Link:
			candidate = string(c.Destination)
		case *ast.AutoLink:
			if c.AutoLinkType == ast.AutoLinkURL {
				candidate = string(c.URL(v.src))
			}
		}
	}
	if candidate == "" {
		// A bare URL: the whole paragraph text, a single line without spaces.
		lines := p.Lines()
		if lines == nil || lines.Len() != 1 {
			return ""
		}
		seg := lines.At(0)
		candidate = strings.TrimSpace(string(seg.Value(v.src)))
		if strings.ContainsAny(candidate, " \t") {
			return ""
		}
	}
	m := youtubeRe.FindStringSubmatch(candidate)
	if m == nil {
		return ""
	}
	if len(candidate) > MaxURLBytes {
		v.fail(v.lineOf(p), "URL longer than %d bytes", MaxURLBytes)
		return ""
	}
	for _, id := range m[1:] {
		if id != "" {
			return id
		}
	}
	return ""
}

// inlineChecks validates every inline node under n (it does not descend into
// block children, which blocks() handles).
func (v *validator) inlineChecks(n ast.Node) {
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || v.err != nil {
			return ast.WalkContinue, nil
		}
		switch c := node.(type) {
		case *ast.RawHTML:
			v.fail(v.lineOf(c), "HTML is not allowed")
		case *ast.CodeSpan:
			v.fail(v.lineOf(c), "code spans are not allowed")
		case *ast.Link:
			v.checkURL(c, string(c.Destination), false)
		case *ast.AutoLink:
			if c.AutoLinkType != ast.AutoLinkURL {
				v.fail(v.lineOf(c), "only https links are allowed")
			} else {
				v.checkURL(c, string(c.URL(v.src)), false)
			}
		case *ast.Image:
			v.info.Images++
			v.checkURL(c, string(c.Destination), true)
		}
		return ast.WalkContinue, nil
	})
}

func (v *validator) checkURL(n ast.Node, dest string, image bool) {
	line := v.lineOf(n)
	if len(dest) > MaxURLBytes {
		v.fail(line, "URL longer than %d bytes", MaxURLBytes)
		return
	}
	if image {
		if m := casRe.FindStringSubmatch(dest); m != nil {
			if v.casExists != nil && !v.casExists(m[1]) {
				v.fail(line, "image cas:%s does not exist", m[1])
				return
			}
			if !v.seenCAS[m[1]] {
				v.seenCAS[m[1]] = true
				v.info.CASImages = append(v.info.CASImages, m[1])
			}
			return
		}
	}
	if err := CheckHTTPSURL(dest); err != nil {
		v.fail(line, "%v", err)
		return
	}
	if !image && !v.seenLinks[dest] {
		v.seenLinks[dest] = true
		v.info.Links = append(v.info.Links, dest)
	}
}

var (
	imageSizes  = map[string]bool{"small": true, "medium": true, "large": true, "full": true}
	imageAligns = map[string]bool{"left": true, "center": true}
)

// ImageHints reads the presentation hints of an image from its CommonMark
// title: whitespace-separated size=<small|medium|large|full> and
// align=<left|center> tokens. The last token of each key wins; an unknown
// value or key is ignored (the result for that key is empty, which clients
// render with the defaults medium and left). It never fails.
func ImageHints(title string) (size, align string) {
	for _, token := range strings.Fields(title) {
		switch {
		case strings.HasPrefix(token, "size="):
			size = token[len("size="):]
			if !imageSizes[size] {
				size = ""
			}
		case strings.HasPrefix(token, "align="):
			align = token[len("align="):]
			if !imageAligns[align] {
				align = ""
			}
		}
	}
	return size, align
}

// CheckHTTPSURL accepts only absolute https URLs with a host, no credentials
// and no whitespace or control characters.
func CheckHTTPSURL(raw string) error {
	if len(raw) > MaxURLBytes {
		return fmt.Errorf("URL longer than %d bytes", MaxURLBytes)
	}
	if !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("only https URLs are allowed")
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("URL contains whitespace or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("invalid https URL")
	}
	if u.User != nil {
		return fmt.Errorf("URLs with credentials are not allowed")
	}
	return nil
}

// plain renders the text of a block with emphasis and link markup removed;
// images contribute their alt text and line breaks become "\n".
func (v *validator) plain(n ast.Node) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			switch t := c.(type) {
			case *ast.Text:
				b.Write(t.Segment.Value(v.src))
				if t.HardLineBreak() || t.SoftLineBreak() {
					b.WriteByte('\n')
				}
			case *ast.String:
				b.Write(t.Value)
			case *ast.AutoLink:
				b.Write(t.URL(v.src))
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return strings.TrimRight(b.String(), "\n")
}
