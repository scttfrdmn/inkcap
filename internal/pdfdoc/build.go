package pdfdoc

import (
	"bufio"
	"bytes"
	"fmt"
	stdhtml "html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/tdewolff/canvas"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	xast "github.com/yuin/goldmark/extension/ast"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// Doc is a paginate-ready flow plus the side tables the renderer needs.
type Doc struct {
	Items []Item
	Links map[*canvas.FontFace]string // face -> URL
	Notes map[*canvas.FontFace]int    // face -> footnote number (the ref markers)
	Body  map[int]Note                // footnote number -> content
	Title string
	Meta  Meta // from front matter, if any
	Warn  []string
	T     *Theme
}

type builder struct {
	t       *Theme
	src     []byte
	base    string // directory for resolving relative image paths
	items   []Item
	links   map[*canvas.FontFace]string
	notes   map[*canvas.FontFace]int
	body    map[int]Note
	title   string
	warn    []string
	missing map[rune]bool                           // runes already warned about, deduped
	quotes  int                                     // blockquotes seen, for Bar IDs
	ids     map[string]bool                         // heading anchors handed out so far
	monoFB  map[*canvas.FontFace][]*canvas.FontFace // code fallback faces, per primary
	targets []string                                // "#..." link targets, checked once all ids exist
}

// Parse converts markdown source into a block flow.
func Parse(src []byte, baseDir string, t *Theme) (*Doc, error) {
	md := goldmark.New(goldmark.WithExtensions(
		extension.GFM, // tables, strikethrough, autolinks, task lists
		extension.Footnote,
		extension.DefinitionList,
	))
	meta, src, err := splitFrontMatter(src)
	if err != nil {
		return nil, err
	}
	root := md.Parser().Parse(text.NewReader(src))

	b := &builder{
		t: t, src: src, base: baseDir,
		links: map[*canvas.FontFace]string{},
		notes: map[*canvas.FontFace]int{},
		body:  map[int]Note{},
	}
	b.title = meta.Title // front matter wins over the first H1
	b.blocks(root, 0, nil, nil, 0)
	b.checkTargets()
	return &Doc{
		Items: b.items, Links: b.links, Notes: b.notes, Body: b.body,
		Title: b.title, Meta: meta, Warn: b.warn, T: t,
	}, nil
}

// ---------------------------------------------------------------------------
// inline
// ---------------------------------------------------------------------------

type style struct {
	size   float64
	weight canvas.FontStyle
	italic bool
	strike bool
	code   bool
	link   string
	color  colorish
}

type colorish int

const (
	colFG colorish = iota
	colMuted
)

// faceArgs computes the point size and the canvas.Face() arguments (colour,
// style, decorations) for a style, independent of which family renders it.
func (b *builder) faceArgs(s style) (size float64, args []any) {
	col := b.t.FG
	if s.color == colMuted {
		col = b.t.Muted
	}
	var deco []any
	if s.strike {
		deco = append(deco, canvas.FontStrikethrough)
	}
	if s.link != "" {
		col = b.t.Link
		deco = append(deco, canvas.FontUnderline)
	}

	args = []any{col}
	if s.code {
		st := canvas.FontRegular
		if s.weight >= canvas.FontBold {
			st = canvas.FontBold
		}
		if s.italic {
			st |= canvas.FontItalic
		}
		args = append(args, st)
		args = append(args, deco...)
		return s.size * 0.92, args
	}
	st := s.weight
	if s.italic {
		st |= canvas.FontItalic
	}
	args = append(args, st)
	args = append(args, deco...)
	return s.size, args
}

func (b *builder) face(s style) *canvas.FontFace {
	size, args := b.faceArgs(s)
	if s.code {
		return b.t.Mono.Face(size, args...)
	}
	return b.t.Text.Face(size, args...)
}

// covers reports whether face has a glyph for r. Control characters (newlines,
// the paragraph/line separators) have no glyph but are handled by the layout,
// so they always count as covered — otherwise every line break would look like
// a missing glyph.
func covers(f *canvas.FontFace, r rune) bool {
	if r < 0x20 || r == 0x2028 || r == 0x2029 || r == 0xFEFF {
		return true
	}
	return f.Font.GlyphIndex(r) != 0
}

// faceFor returns the face that should render r: the primary face if it covers
// r, else the first fallback family that does. A rune covered by nothing is
// reported once and rendered (as .notdef) by the primary face.
func (b *builder) faceFor(s style, primary *canvas.FontFace, r rune) *canvas.FontFace {
	if covers(primary, r) {
		return primary
	}
	size, args := b.faceArgs(s)
	for _, fam := range b.t.Fallback {
		ff := fam.Face(size, args...)
		if covers(ff, r) {
			if s.link != "" {
				b.links[ff] = s.link
			}
			return ff
		}
	}
	b.warnRune(r)
	return primary
}

// run allocates a face and, when the style carries a URL, records it so the
// renderer can attach a link annotation to the resulting spans. It does not do
// glyph fallback; use emit for text that may contain non-Latin runes.
func (b *builder) run(s style, txt string) Run {
	f := b.face(s)
	if s.link != "" {
		b.links[f] = s.link
	}
	return Run{Face: f, Text: txt, Link: s.link}
}

// emit appends txt to out as one or more runs, switching to a fallback face for
// any run of characters the primary face can't render. Adjacent characters that
// share a face are coalesced, so ASCII text still produces a single run.
func (b *builder) emit(s style, txt string, out *[]Run) {
	if txt == "" {
		return
	}
	primary := b.face(s)
	if s.link != "" {
		b.links[primary] = s.link
	}
	var cur strings.Builder
	curFace := primary
	flush := func() {
		if cur.Len() > 0 {
			*out = append(*out, Run{Face: curFace, Text: cur.String(), Link: s.link})
			cur.Reset()
		}
	}
	for _, r := range txt {
		f := b.faceFor(s, primary, r)
		if f != curFace {
			flush()
			curFace = f
		}
		cur.WriteRune(r)
	}
	flush()
}

// warnRune records a rune that no configured font can render, once each.
func (b *builder) warnRune(r rune) {
	if b.missing == nil {
		b.missing = map[rune]bool{}
	}
	if b.missing[r] {
		return
	}
	b.missing[r] = true
	b.warn = append(b.warn, fmt.Sprintf("no glyph for %q (U+%04X) in any configured font", r, r))
}

// imageRun builds an inline-image run scaled so the image's height matches the
// cap height of the surrounding text, keeping its aspect ratio. The resolution
// is chosen to yield that target height; the run keeps the text face so canvas
// has a baseline to centre the image against.
func (b *builder) imageRun(s style, img image.Image) Run {
	f := b.face(s)
	if s.link != "" {
		b.links[f] = s.link
	}
	target := f.Metrics().CapHeight // mm
	if target <= 0 {
		target = pt(s.size) * 0.7
	}
	px := img.Bounds().Dy()
	if px <= 0 {
		px = 1
	}
	res := canvas.Resolution(float64(px) / target) // px per mm
	return Run{Face: f, Img: img, ImgRes: res, Link: s.link}
}

// svgRun builds an inline SVG run scaled so the graphic's height matches the
// cap height of the surrounding text, preserving aspect ratio.
func (b *builder) svgRun(s style, c *canvas.Canvas) Run {
	f := b.face(s)
	if s.link != "" {
		b.links[f] = s.link
	}
	target := f.Metrics().CapHeight // mm
	if target <= 0 {
		target = pt(s.size) * 0.7
	}
	return Run{Face: f, SVG: scaleCanvasToHeight(c, target), Link: s.link}
}

// scaleCanvasToHeight returns a new canvas of the source scaled uniformly so
// its height is h mm (aspect preserved). A degenerate source is returned as-is.
func scaleCanvasToHeight(src *canvas.Canvas, h float64) *canvas.Canvas {
	sw, sh := src.Size()
	if sw <= 0 || sh <= 0 || h <= 0 {
		return src
	}
	scale := h / sh
	dst := canvas.New(sw*scale, sh*scale)
	src.RenderViewTo(dst, canvas.Identity.Scale(scale, scale))
	return dst
}

func (b *builder) inline(n ast.Node, s style, out *[]Run) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			txt := decodeText(v.Segment.Value(b.src))
			if v.SoftLineBreak() {
				txt += " "
			}
			if v.HardLineBreak() {
				txt += "\n"
			}
			b.emit(s, txt, out)
		case *ast.String:
			b.emit(s, string(v.Value), out)
		case *ast.CodeSpan:
			cs := s
			cs.code = true
			var buf bytes.Buffer
			for cc := v.FirstChild(); cc != nil; cc = cc.NextSibling() {
				if t, ok := cc.(*ast.Text); ok {
					buf.Write(t.Segment.Value(b.src))
				}
			}
			b.emit(cs, buf.String(), out)
		case *ast.Emphasis:
			es := s
			if v.Level >= 2 {
				es.weight = canvas.FontBold
			} else {
				es.italic = true
			}
			b.inline(v, es, out)
		case *xast.Strikethrough:
			ss := s
			ss.strike = true
			b.inline(v, ss, out)
		case *ast.Link:
			ls := s
			ls.link = string(v.Destination)
			if strings.HasPrefix(ls.link, "#") {
				// In-document link: canvas turns "#name" into a GoTo to the
				// anchor of that name. Match the decoded form of the slug.
				if u, err := url.PathUnescape(ls.link); err == nil {
					ls.link = u
				}
				b.targets = append(b.targets, ls.link[1:])
			}
			b.inline(v, ls, out)
		case *ast.AutoLink:
			ls := s
			u := string(v.URL(b.src))
			ls.link = u
			b.emit(ls, string(v.Label(b.src)), out)
		case *ast.Image:
			// An inline image renders at the surrounding text's cap height,
			// vertically centred on the line. If the file is missing or fails to
			// decode, fall back to the alt text.
			dest := string(v.Destination)
			if isSVG(dest) {
				if c, err := b.loadSVG(dest); err == nil {
					*out = append(*out, b.svgRun(s, c))
				} else {
					b.inline(v, s, out)
				}
			} else if img, err := b.loadImage(dest); err == nil {
				*out = append(*out, b.imageRun(s, img))
			} else {
				b.inline(v, s, out)
			}
		case *xast.FootnoteLink:
			if b.t.Footnotes == "none" {
				continue
			}
			f := b.t.Text.Face(s.size, b.t.Link, canvas.FontSuperscript)
			b.notes[f] = v.Index
			*out = append(*out, Run{Face: f, Text: strconv.Itoa(v.Index)})
		case *ast.RawHTML:
			// A tag, not text. goldmark emits the text between tags as
			// ast.Text, so "strip" simply means: ignore the tags.
			raw := stripComments(rawHTML(v.Segments, b.src))
			if brRe.MatchString(raw) {
				*out = append(*out, b.run(s, "\n"))
			}
			b.warnHTML(raw)
		default:
			b.inline(c, s, out)
		}
	}
}

// decodeText resolves backslash escapes and entity/numeric character
// references in a text segment. goldmark leaves both to the renderer, so the
// raw segment still reads "\*x\*" or "&copy;". Running it through goldmark's own
// HTML text writer gives exactly CommonMark's semantics (an escaped "\&copy;"
// stays literal); html.UnescapeString then undoes only the writer's HTML
// escaping. Code spans and code blocks never come through here.
func decodeText(seg []byte) string {
	if bytes.IndexByte(seg, '\\') < 0 && bytes.IndexByte(seg, '&') < 0 {
		return string(seg) // fast path: nothing to decode
	}
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	gmhtml.DefaultWriter.Write(w, seg)
	w.Flush()
	return stdhtml.UnescapeString(buf.String())
}

func (b *builder) runs(n ast.Node, s style) []Run {
	var out []Run
	b.inline(n, s, &out)
	return out
}

// ---------------------------------------------------------------------------
// blocks
// ---------------------------------------------------------------------------

func (b *builder) push(blk Block, indent float64, bars []Bar, m *Marker) {
	b.items = append(b.items, Item{B: blk, Indent: indent, Bars: bars, Marker: m})
}

// depth is the list-nesting level (0 at the document root); it selects the
// unordered-list bullet glyph independently of the millimetre inset, so a
// custom spacing.indent doesn't disturb the bullet cycle.
func (b *builder) blocks(n ast.Node, indent float64, bars []Bar, marker *Marker, depth int) {
	first := true
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		m := marker
		if !first {
			m = nil
		}
		b.block(c, indent, bars, m, depth)
		first = false
	}
}

func (b *builder) block(n ast.Node, indent float64, bars []Bar, marker *Marker, depth int) {
	t := b.t
	body := style{size: t.BodySize}

	switch v := n.(type) {
	case *ast.Heading:
		lvl := v.Level
		if lvl > 6 {
			lvl = 6
		}
		hs := style{size: t.HeadSize[lvl-1], weight: canvas.FontBold}
		runs := b.runs(v, hs)
		// Footnote ref markers are superscript digits in the runs; they must
		// not leak into the title, running header, outline or TOC.
		plain := plainText(withoutRefs(runs, b.notes))
		if lvl == 1 && b.title == "" {
			b.title = plain
		}
		h := &Heading{
			base:  base{before: t.HeadSpaceHi[lvl-1], after: t.HeadSpaceLo[lvl-1], keep: true},
			Level: lvl, Runs: runs, Plain: plain, ID: b.anchorID(plain),
			Opts: t.textOpts(), Rule: lvl <= t.HeadingRules, T: t,
		}
		b.push(h, indent, bars, marker)

	case *ast.Paragraph, *ast.TextBlock:
		// A paragraph whose only content is an image becomes a figure.
		if p, ok := v.(*ast.Paragraph); ok && p.ChildCount() == 1 {
			if img, ok := p.FirstChild().(*ast.Image); ok {
				dest := string(img.Destination)
				cap := b.runs(img, style{size: t.SmallSize, color: colMuted})
				if isSVG(dest) {
					if c, err := b.loadSVG(dest); err == nil {
						b.push(&Image{
							base:    base{before: t.BlockSpace, after: t.BlockSpace},
							SVG:     c,
							Caption: cap,
							T:       t,
						}, indent, bars, marker)
						return
					}
					// fall through to alt text if the SVG is missing/invalid
				} else if m, err := b.loadImage(dest); err == nil {
					b.push(&Image{
						base:    base{before: t.BlockSpace, after: t.BlockSpace},
						Img:     m,
						Caption: cap,
						T:       t,
					}, indent, bars, marker)
					return
				}
				// fall through to alt text if the file is missing
			}
		}
		runs := b.runs(v, body)
		if len(runs) == 0 {
			return
		}
		sp := t.ParaSpace
		if _, ok := v.(*ast.TextBlock); ok {
			sp = 0.7 // tight list items
		}
		p := &Para{
			base: base{before: 0, after: sp},
			Runs: runs, Align: t.bodyAlign(), Opts: t.textOpts(),
			Orphan: t.Orphans, Widow: t.Widows,
		}
		b.push(p, indent, bars, marker)

	case *ast.FencedCodeBlock:
		lang := string(v.Language(b.src))
		b.push(b.code(v.Lines(), lang), indent, bars, marker)

	case *ast.CodeBlock:
		b.push(b.code(v.Lines(), ""), indent, bars, marker)

	case *ast.Blockquote:
		// Each quote level adds a bar at its own inset. Copy rather than
		// append in place, so sibling quotes don't share a backing array.
		b.quotes++
		inner := append(slices.Clone(bars), Bar{ID: b.quotes, Inset: indent + t.IndentStep})
		b.blocks(v, indent+t.IndentStep, inner, marker, depth)

	case *ast.List:
		b.list(v, indent, bars, depth+1)

	case *ast.ListItem:
		b.blocks(v, indent, bars, marker, depth)

	case *ast.ThematicBreak:
		b.push(&Rule{base: base{before: t.BlockSpace, after: t.BlockSpace}, T: t}, indent, bars, marker)

	case *xast.Table:
		b.table(v, indent, bars, marker)

	case *xast.DefinitionList:
		b.blocks(v, indent, bars, marker, depth)

	case *xast.DefinitionTerm:
		// The term is inline content; render it bold and keep it with the
		// description that follows.
		runs := b.runs(v, style{size: t.BodySize, weight: canvas.FontBold})
		if len(runs) == 0 {
			return
		}
		b.push(&Para{
			base: base{after: 0.7, keep: true},
			Runs: runs, Align: t.bodyAlign(), Opts: t.textOpts(),
			Orphan: t.Orphans, Widow: t.Widows,
		}, indent, bars, marker)

	case *xast.DefinitionDescription:
		// The description is block content, inset one step under its term.
		b.blocks(v, indent+t.IndentStep, bars, nil, depth)

	case *xast.FootnoteList:
		if t.Footnotes == "none" {
			return
		}
		for fn := n.FirstChild(); fn != nil; fn = fn.NextSibling() {
			f, ok := fn.(*xast.Footnote)
			if !ok {
				continue
			}
			var runs []Run
			for c := f.FirstChild(); c != nil; c = c.NextSibling() {
				if _, isBack := c.(*xast.FootnoteBacklink); isBack {
					continue
				}
				b.inline(c, style{size: t.SmallSize}, &runs)
			}
			b.body[f.Index] = Note{Num: f.Index, Runs: runs}
		}
		// Footnotes are not part of the flow: the paginator places them.

	case *xast.FootnoteBacklink:
		// never rendered

	case *ast.HTMLBlock:
		raw := rawHTML(v.Lines(), b.src)
		if v.HasClosure() {
			// goldmark keeps the closing line of a comment, <script>, etc.
			// outside Lines(); without it a multi-line comment loses its "-->"
			// and its contents leak out as text.
			raw += string(v.ClosureLine.Value(b.src))
		}
		raw = stripComments(raw)
		b.warnHTML(raw)
		if t.HTML == "drop" {
			return
		}
		txt := strings.TrimSpace(tagRe.ReplaceAllString(raw, " "))
		txt = wsRe.ReplaceAllString(txt, " ")
		if txt == "" {
			return
		}
		b.push(&Para{
			base:  base{after: t.ParaSpace},
			Runs:  []Run{b.run(body, txt)},
			Align: t.bodyAlign(), Opts: t.textOpts(),
			Orphan: t.Orphans, Widow: t.Widows,
		}, indent, bars, marker)

	default:
		b.blocks(n, indent, bars, marker, depth)
	}
}

// anchorID returns the GitHub-style slug for a heading, so links written for
// GitHub's renderer ([x](#getting-started)) resolve in the PDF too: lower-case,
// punctuation dropped, spaces to hyphens, and -1, -2, ... on repeats.
func (b *builder) anchorID(plain string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(plain)) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			sb.WriteRune(r)
		case r == ' ':
			sb.WriteRune('-')
		}
	}
	base := sb.String()
	if b.ids == nil {
		b.ids = map[string]bool{}
	}
	id := base
	for n := 1; b.ids[id]; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	b.ids[id] = true
	return id
}

// checkTargets warns, once per name, about "#name" links with no heading of
// that name: in the PDF they would silently go nowhere.
func (b *builder) checkTargets() {
	seen := map[string]bool{}
	for _, name := range b.targets {
		if b.ids[name] || seen[name] {
			continue
		}
		seen[name] = true
		b.warn = append(b.warn, fmt.Sprintf("link to #%s: no heading has that anchor", name))
	}
}

// commentRe matches the parts of HTML that are never content: comments
// (including one left unclosed at the end of a block), processing
// instructions, and <!DOCTYPE>/<![CDATA[ declarations. They are dropped
// silently, never rendered and never warned about.
var commentRe = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)|<\?.*?(?:\?>|$)|<![A-Za-z\[][^>]*>`)

func stripComments(raw string) string { return commentRe.ReplaceAllString(raw, " ") }

var (
	tagRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe  = regexp.MustCompile(`\s+`)
	brRe  = regexp.MustCompile(`(?i)^<br\s*/?>$`)
)

func rawHTML(segs *text.Segments, src []byte) string {
	var sb strings.Builder
	for i := 0; i < segs.Len(); i++ {
		seg := segs.At(i)
		sb.Write(seg.Value(src))
	}
	return sb.String()
}

// warnHTML records dropped markup once per distinct tag name, so a document
// full of <br> produces one line of warning, not four hundred.
func (b *builder) warnHTML(raw string) {
	for _, m := range tagRe.FindAllString(raw, -1) {
		name := strings.Trim(m, "</> \t\n")
		if i := strings.IndexAny(name, " \t\n"); i > 0 {
			name = name[:i]
		}
		if name == "" || name == "br" {
			continue
		}
		w := "dropped HTML tag <" + name + ">"
		if !slices.Contains(b.warn, w) {
			b.warn = append(b.warn, w)
		}
	}
}

func (b *builder) list(v *ast.List, indent float64, bars []Bar, depth int) {
	t := b.t
	i := v.Start // goldmark sets this for every ordered list; 0 is a real start
	for li := v.FirstChild(); li != nil; li = li.NextSibling() {
		var label string
		if v.IsOrdered() {
			label = strconv.Itoa(i) + "."
			i++
		} else {
			label = markerGlyph(depth)
		}
		ms := style{size: t.BodySize, color: colFG}
		mk := &Marker{Text: label, Face: b.face(ms)}
		if label == "\u25e6" {
			mk.Shape = ShapeRing
		}

		// A task-list checkbox replaces the bullet.
		if item, ok := li.(*ast.ListItem); ok {
			if tb := item.FirstChild(); tb != nil {
				if cb, ok := tb.FirstChild().(*xast.TaskCheckBox); ok {
					if cb.IsChecked {
						mk.Text, mk.Shape = "\u2611", ShapeBoxChecked
					} else {
						mk.Text, mk.Shape = "\u2610", ShapeBox
					}
					tb.RemoveChild(tb, cb)
				}
			}
		}
		if mk.Shape == ShapeText {
			mk.Face = b.markerFace(ms, mk.Face, mk.Text)
		}
		b.blocks(li, indent+t.IndentStep, bars, mk, depth)
	}
}

// markerFace returns a face that can render every rune of a text marker: the
// primary face if it covers them all, else the first fallback family that
// does. Uncovered runes are reported, so a custom font without "\u2022" is noticed
// rather than silently drawing .notdef boxes.
func (b *builder) markerFace(s style, primary *canvas.FontFace, txt string) *canvas.FontFace {
	all := func(f *canvas.FontFace) bool {
		for _, r := range txt {
			if !covers(f, r) {
				return false
			}
		}
		return true
	}
	if all(primary) {
		return primary
	}
	size, args := b.faceArgs(s)
	for _, fam := range b.t.Fallback {
		if ff := fam.Face(size, args...); all(ff) {
			return ff
		}
	}
	for _, r := range txt {
		if !covers(primary, r) {
			b.warnRune(r)
		}
	}
	return primary
}

// markerGlyph cycles the unordered-list bullet by nesting depth: \u2022 at the top
// level, \u25e6 one level in, \u2013 deeper, then repeating. depth is 1-based here (the
// outermost list is depth 1).
func markerGlyph(depth int) string {
	switch (depth - 1) % 3 {
	case 0:
		return "\u2022"
	case 1:
		return "\u25e6"
	default:
		return "\u2013"
	}
}

func (b *builder) table(v *xast.Table, indent float64, bars []Bar, marker *Marker) {
	t := b.t
	tb := &Table{
		base: base{before: t.BlockSpace, after: t.BlockSpace},
		T:    t, PadX: pt(5), PadY: pt(4),
	}
	align := func(a xast.Alignment) canvas.TextAlign {
		switch a {
		case xast.AlignCenter:
			return canvas.Center
		case xast.AlignRight:
			return canvas.Right
		default:
			return canvas.Left
		}
	}
	for row := v.FirstChild(); row != nil; row = row.NextSibling() {
		hdr := false
		if _, ok := row.(*xast.TableHeader); ok {
			hdr = true
		}
		var cells []Cell
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			tc, ok := c.(*xast.TableCell)
			if !ok {
				continue
			}
			s := style{size: t.BodySize}
			if hdr {
				s.weight = canvas.FontBold
			}
			cells = append(cells, Cell{Runs: b.runs(tc, s), Align: align(tc.Alignment)})
		}
		if hdr {
			tb.Head = cells
		} else {
			tb.Rows = append(tb.Rows, cells)
		}
	}
	b.push(tb, indent, bars, marker)
}

// ---------------------------------------------------------------------------
// code + syntax highlighting
// ---------------------------------------------------------------------------

func (b *builder) code(segs *text.Segments, lang string) Block {
	t := b.t
	var buf bytes.Buffer
	for i := 0; i < segs.Len(); i++ {
		seg := segs.At(i)
		buf.Write(seg.Value(b.src))
	}
	src := strings.TrimRight(buf.String(), "\n")

	plain := t.Mono.Face(t.CodeSize, t.FG, canvas.FontRegular)
	lines := b.highlight(src, lang, plain)

	return &Code{
		base:  base{before: t.BlockSpace, after: t.BlockSpace},
		Lines: lines, T: t,
		First: 1,
		nums:  t.Mono.Face(t.CodeSize, t.Muted, canvas.FontRegular),
		adv:   plain.TextWidth("M"),
		lineH: plain.Metrics().LineHeight * 1.12,
	}
}

func (b *builder) highlight(src, lang string, plain *canvas.FontFace) [][]Run {
	t := b.t
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		return b.plainLines(src, plain)
	}
	st := styles.Get(t.ChromaStyle)
	if st == nil {
		st = styles.Fallback
	}
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return b.plainLines(src, plain)
	}

	cache := map[chroma.StyleEntry]*canvas.FontFace{}
	faceFor := func(tok chroma.TokenType) *canvas.FontFace {
		e := st.Get(tok)
		if f, ok := cache[e]; ok {
			return f
		}
		style := canvas.FontRegular
		if e.Bold == chroma.Yes {
			style = canvas.FontBold
		}
		if e.Italic == chroma.Yes {
			style |= canvas.FontItalic
		}
		col := t.FG
		if e.Colour.IsSet() {
			col.R, col.G, col.B = e.Colour.Red(), e.Colour.Green(), e.Colour.Blue()
		}
		f := t.Mono.Face(t.CodeSize, col, style)
		cache[e] = f
		return f
	}

	var lines [][]Run
	cur := []Run{}
	for _, tok := range it.Tokens() {
		f := faceFor(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				lines = append(lines, cur)
				cur = []Run{}
			}
			if p != "" {
				cur = append(cur, b.monoRuns(f, b.expandTabs(p))...)
			}
		}
	}
	lines = append(lines, cur)
	return lines
}

func (b *builder) plainLines(src string, f *canvas.FontFace) [][]Run {
	var out [][]Run
	for _, ln := range strings.Split(src, "\n") {
		if ln == "" {
			out = append(out, nil)
			continue
		}
		out = append(out, b.monoRuns(f, b.expandTabs(ln)))
	}
	return out
}

// monoRuns splits code text into runs by glyph coverage, like emit does for
// prose: characters the mono face lacks switch to the first fallback family
// that has them (same size, colour and style), and a character no font covers
// is reported once. Code blocks used to bypass both.
func (b *builder) monoRuns(primary *canvas.FontFace, txt string) []Run {
	var out []Run
	var cur strings.Builder
	curFace := primary
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, Run{Face: curFace, Text: cur.String()})
			cur.Reset()
		}
	}
	for _, r := range txt {
		f := primary
		if !covers(primary, r) {
			f = b.monoFallback(primary, r)
		}
		if f != curFace {
			flush()
			curFace = f
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// monoFallback finds (and caches per primary face) the fallback face covering
// r, or warns and returns primary.
func (b *builder) monoFallback(primary *canvas.FontFace, r rune) *canvas.FontFace {
	if b.monoFB == nil {
		b.monoFB = map[*canvas.FontFace][]*canvas.FontFace{}
	}
	faces, ok := b.monoFB[primary]
	if !ok {
		for _, fam := range b.t.Fallback {
			faces = append(faces, fam.Face(b.t.CodeSize, primary.Fill.Color, primary.Style))
		}
		b.monoFB[primary] = faces
	}
	for _, f := range faces {
		if covers(f, r) {
			return f
		}
	}
	b.warnRune(r)
	return primary
}

func (b *builder) expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", b.t.TabWidth))
}

// loadImage is used by the (optional) image path; kept simple on purpose.
func (b *builder) loadImage(rel string) (image.Image, error) {
	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.base, rel)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", rel, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// isSVG reports whether a destination looks like an SVG, by extension.
func isSVG(rel string) bool {
	return strings.EqualFold(filepath.Ext(rel), ".svg")
}

// loadSVG parses an SVG file into a vector canvas that embeds losslessly into
// the PDF. Returns an error (→ alt-text fallback) if the file is missing or the
// parser rejects it.
func (b *builder) loadSVG(rel string) (*canvas.Canvas, error) {
	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.base, rel)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", rel, err)
	}
	defer f.Close()
	c, err := canvas.ParseSVG(f)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", rel, err)
	}
	return c, nil
}
