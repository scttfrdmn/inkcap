package pdfdoc

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/tdewolff/canvas"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	xast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Doc is a paginate-ready flow plus the side tables the renderer needs.
type Doc struct {
	Items []Item
	Links map[*canvas.FontFace]string // face -> URL
	Notes map[*canvas.FontFace]int    // face -> footnote number (the ref markers)
	Body  map[int]Note                // footnote number -> content
	Title string
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
	missing map[rune]bool // runes already warned about, deduped
}

// Parse converts markdown source into a block flow.
func Parse(src []byte, baseDir string, t *Theme) (*Doc, error) {
	md := goldmark.New(goldmark.WithExtensions(
		extension.GFM, // tables, strikethrough, autolinks, task lists
		extension.Footnote,
		extension.DefinitionList,
	))
	root := md.Parser().Parse(text.NewReader(src))

	b := &builder{
		t: t, src: src, base: baseDir,
		links: map[*canvas.FontFace]string{},
		notes: map[*canvas.FontFace]int{},
		body:  map[int]Note{},
	}
	b.blocks(root, 0, false, nil, 0)
	return &Doc{
		Items: b.items, Links: b.links, Notes: b.notes, Body: b.body,
		Title: b.title, Warn: b.warn, T: t,
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

func (b *builder) inline(n ast.Node, s style, out *[]Run) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			txt := string(v.Segment.Value(b.src))
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
			b.inline(v, ls, out)
		case *ast.AutoLink:
			ls := s
			u := string(v.URL(b.src))
			ls.link = u
			b.emit(ls, string(v.Label(b.src)), out)
		case *ast.Image:
			// inline images are rare in dev docs; render alt text
			b.inline(v, s, out)
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
			raw := rawHTML(v.Segments, b.src)
			if brRe.MatchString(raw) {
				*out = append(*out, b.run(s, "\n"))
			}
			b.warnHTML(raw)
		default:
			b.inline(c, s, out)
		}
	}
}

func (b *builder) runs(n ast.Node, s style) []Run {
	var out []Run
	b.inline(n, s, &out)
	return out
}

// ---------------------------------------------------------------------------
// blocks
// ---------------------------------------------------------------------------

func (b *builder) push(blk Block, indent float64, bar bool, m *Marker) {
	b.items = append(b.items, Item{B: blk, Indent: indent, Bar: bar, Marker: m})
}

// depth is the list-nesting level (0 at the document root); it selects the
// unordered-list bullet glyph independently of the millimetre inset, so a
// custom spacing.indent doesn't disturb the bullet cycle.
func (b *builder) blocks(n ast.Node, indent float64, bar bool, marker *Marker, depth int) {
	first := true
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		m := marker
		if !first {
			m = nil
		}
		b.block(c, indent, bar, m, depth)
		first = false
	}
}

func (b *builder) block(n ast.Node, indent float64, bar bool, marker *Marker, depth int) {
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
		if lvl == 1 && b.title == "" {
			b.title = plainText(runs)
		}
		h := &Heading{
			base:  base{before: t.HeadSpaceHi[lvl-1], after: t.HeadSpaceLo[lvl-1], keep: true},
			Level: lvl, Runs: runs, Plain: plainText(runs),
			Opts: t.textOpts(), Rule: lvl <= 2, T: t,
		}
		b.push(h, indent, bar, marker)

	case *ast.Paragraph, *ast.TextBlock:
		// A paragraph whose only content is an image becomes a figure.
		if p, ok := v.(*ast.Paragraph); ok && p.ChildCount() == 1 {
			if img, ok := p.FirstChild().(*ast.Image); ok {
				if m, err := b.loadImage(string(img.Destination)); err == nil {
					cap := b.runs(img, style{size: t.SmallSize, color: colMuted})
					b.push(&Image{
						base:    base{before: t.BlockSpace, after: t.BlockSpace},
						Img:     m,
						Caption: cap,
						T:       t,
					}, indent, bar, marker)
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
		b.push(p, indent, bar, marker)

	case *ast.FencedCodeBlock:
		lang := string(v.Language(b.src))
		b.push(b.code(v.Lines(), lang), indent, bar, marker)

	case *ast.CodeBlock:
		b.push(b.code(v.Lines(), ""), indent, bar, marker)

	case *ast.Blockquote:
		b.blocks(v, indent+t.IndentStep, true, marker, depth)

	case *ast.List:
		b.list(v, indent, depth+1)

	case *ast.ListItem:
		b.blocks(v, indent, bar, marker, depth)

	case *ast.ThematicBreak:
		b.push(&Rule{base: base{before: t.BlockSpace, after: t.BlockSpace}, T: t}, indent, bar, marker)

	case *xast.Table:
		b.table(v, indent, bar, marker)

	case *xast.DefinitionList:
		b.blocks(v, indent, bar, marker, depth)

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
		}, indent, bar, marker)

	case *xast.DefinitionDescription:
		// The description is block content, inset one step under its term.
		b.blocks(v, indent+t.IndentStep, bar, nil, depth)

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
		}, indent, bar, marker)

	default:
		b.blocks(n, indent, bar, marker, depth)
	}
}

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

func (b *builder) list(v *ast.List, indent float64, depth int) {
	t := b.t
	i := v.Start
	if i == 0 {
		i = 1
	}
	for li := v.FirstChild(); li != nil; li = li.NextSibling() {
		var label string
		if v.IsOrdered() {
			label = strconv.Itoa(i) + "."
			i++
		} else {
			label = markerGlyph(depth)
		}
		mk := &Marker{Text: label, Face: b.face(style{size: t.BodySize, color: colFG})}

		// A task-list checkbox replaces the bullet.
		if item, ok := li.(*ast.ListItem); ok {
			if tb := item.FirstChild(); tb != nil {
				if cb, ok := tb.FirstChild().(*xast.TaskCheckBox); ok {
					if cb.IsChecked {
						mk.Text = "\u2611"
					} else {
						mk.Text = "\u2610"
					}
					tb.RemoveChild(tb, cb)
				}
			}
		}
		b.blocks(li, indent+t.IndentStep, false, mk, depth)
	}
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

func (b *builder) table(v *xast.Table, indent float64, bar bool, marker *Marker) {
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
	b.push(tb, indent, bar, marker)
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
				cur = append(cur, Run{Face: f, Text: b.expandTabs(p)})
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
		out = append(out, []Run{{Face: f, Text: b.expandTabs(ln)}})
	}
	return out
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
