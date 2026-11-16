package pdfdoc

import (
	"bytes"
	"image/color"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/canvas"
	"github.com/scttfrdmn/inkcap/internal/config"
)

// markers walks the built items and returns the bullet glyph of every
// unordered-list marker, in document order.
func markers(d *Doc) []string {
	var out []string
	for _, it := range d.Items {
		if it.Marker == nil {
			continue
		}
		switch it.Marker.Text {
		case "•", "◦", "–": // • ◦ –
			out = append(out, it.Marker.Text)
		}
	}
	return out
}

// The unordered-list bullet must cycle by nesting depth (• ◦ –), and that
// choice must not depend on the configured indent width. The old code derived
// the glyph from the accumulated millimetre inset divided by the default step,
// so a non-default spacing.indent silently corrupted the cycle.
func TestListMarkerCyclesByDepth(t *testing.T) {
	src := "- a\n" +
		"  - b\n" +
		"    - c\n" +
		"      - d\n" // fourth level wraps back to •

	want := []string{"•", "◦", "–", "•"}

	for _, indent := range []float64{7.0, 3.0, 12.5} {
		th := theme(t, func(c *config.Config) { c.Spacing.Indent = indent })
		d := parse(t, src, th)
		got := markers(d)
		if len(got) != len(want) {
			t.Fatalf("indent=%v: got %d markers %v, want %d %v", indent, len(got), got, len(want), want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("indent=%v: marker %d = %q, want %q", indent, i, got[i], want[i])
			}
		}
	}
}

// A definition list must render its term in bold and inset its description one
// step, rather than collapsing both into flush body paragraphs. goldmark's
// DefinitionList extension is enabled, so this exercises the DefinitionTerm /
// DefinitionDescription block cases.
func TestDefinitionListStyled(t *testing.T) {
	th := theme(t, nil)
	src := "Apple\n:   A pomaceous fruit.\n\nBanana\n:   An elongated berry.\n"
	d := parse(t, src, th)

	var terms, descs int
	for _, it := range d.Items {
		p, ok := it.B.(*Para)
		if !ok {
			continue
		}
		txt := plainText(p.Runs)
		switch txt {
		case "Apple", "Banana":
			terms++
			// The term must be bold and kept with its description.
			if len(p.Runs) == 0 || p.Runs[0].Face.Style&canvas.FontBold == 0 {
				t.Errorf("term %q is not bold", txt)
			}
			if !p.KeepWithNext() {
				t.Errorf("term %q is not kept with its description", txt)
			}
			if it.Indent != 0 {
				t.Errorf("term %q indented %.1fmm, want 0", txt, it.Indent)
			}
		case "A pomaceous fruit.", "An elongated berry.":
			descs++
			// The description must be inset one step under its term.
			if it.Indent != th.IndentStep {
				t.Errorf("description %q indented %.1fmm, want %.1f", txt, it.Indent, th.IndentStep)
			}
		}
	}
	if terms != 2 {
		t.Errorf("found %d bold terms, want 2", terms)
	}
	if descs != 2 {
		t.Errorf("found %d descriptions, want 2", descs)
	}
}

// Every default list marker must be renderable: text markers only with glyphs
// their face actually has, and the glyphs the embedded faces lack (◦ ☐ ☑)
// drawn as vector shapes. Before, nested and task lists drew .notdef boxes.
func TestListMarkersRenderable(t *testing.T) {
	src := "- a\n  - b\n    - c\n\n" +
		"1. one\n2. two\n\n" +
		"- [ ] todo\n- [x] done\n"
	th := theme(t, nil)
	d := parse(t, src, th)

	shapes := map[string]MarkerShape{}
	for _, it := range d.Items {
		m := it.Marker
		if m == nil {
			continue
		}
		shapes[m.Text] = m.Shape
		if m.Shape != ShapeText {
			continue
		}
		for _, r := range m.Text {
			if !covers(m.Face, r) {
				t.Errorf("marker %q: face has no glyph for %q (U+%04X)", m.Text, r, r)
			}
		}
	}
	want := map[string]MarkerShape{
		"•": ShapeText, "◦": ShapeRing, "–": ShapeText,
		"1.": ShapeText, "2.": ShapeText,
		"☐": ShapeBox, "☑": ShapeBoxChecked,
	}
	for txt, sh := range want {
		got, ok := shapes[txt]
		if !ok {
			t.Errorf("no %q marker built", txt)
		} else if got != sh {
			t.Errorf("marker %q: shape %d, want %d", txt, got, sh)
		}
	}
	if len(d.Warn) != 0 {
		t.Errorf("unexpected warnings: %v", d.Warn)
	}
	// Drawing the shapes must not fail.
	var buf bytes.Buffer
	if err := d.Render(&buf, ""); err != nil {
		t.Fatal(err)
	}
}

// A text marker with a rune no configured font covers is reported, rather
// than silently drawn as .notdef.
func TestMarkerMissingGlyphWarns(t *testing.T) {
	th := theme(t, nil)
	b := &builder{t: th, links: map[*canvas.FontFace]string{}}
	s := style{size: th.BodySize}
	b.markerFace(s, b.face(s), "中.")
	if len(b.warn) != 1 || !strings.Contains(b.warn[0], "U+4E2D") {
		t.Errorf("warnings = %v, want one for U+4E2D", b.warn)
	}
}

// typography.heading_rules = n draws the hairline under H1..Hn, and 0 disables
// it. Body headings used to hardcode H1/H2 and ignore the setting.
func TestHeadingRulesSetting(t *testing.T) {
	src := "# h1\n\n## h2\n\n### h3\n\n#### h4\n"
	for _, n := range []int{0, 1, 2, 3} {
		th := theme(t, func(c *config.Config) { c.Type.HeadingRules = n })
		d := parse(t, src, th)
		for _, it := range d.Items {
			h, ok := it.B.(*Heading)
			if !ok {
				continue
			}
			if want := h.Level <= n; h.Rule != want {
				t.Errorf("heading_rules=%d: H%d Rule=%v, want %v", n, h.Level, h.Rule, want)
			}
		}
	}
}

// An ordered list numbers from its literal start, including 0.
func TestOrderedListStart(t *testing.T) {
	th := theme(t, nil)
	for src, want := range map[string][]string{
		"0. a\n1. b\n": {"0.", "1."},
		"1. a\n2. b\n": {"1.", "2."},
		"7. a\n8. b\n": {"7.", "8."},
		"3) a\n4) b\n": {"3)", "4)"}, // the ")" delimiter is kept (#73)
	} {
		var got []string
		for _, it := range parse(t, src, th).Items {
			if it.Marker != nil {
				got = append(got, it.Marker.Text)
			}
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%q: markers %v, want %v", src, got, want)
		}
	}
}

// A footnote ref in a heading must not leak its number into the document
// title (PDF metadata, running header), the outline entry, or the TOC line.
func TestFootnoteRefNotInHeadingText(t *testing.T) {
	th := theme(t, func(c *config.Config) { c.Document.TOC = true })
	src := "# Intro[^1]\n\nLead.\n\n## Setup[^2] steps\n\nBody.\n\n[^1]: one\n[^2]: two\n"
	d := parse(t, src, th)
	if d.Title != "Intro" {
		t.Errorf("Title = %q, want %q", d.Title, "Intro")
	}
	for _, it := range d.Items {
		if h, ok := it.B.(*Heading); ok && strings.ContainsAny(h.Plain, "12") {
			t.Errorf("H%d Plain = %q contains a footnote number", h.Level, h.Plain)
		}
	}
	for _, it := range d.buildTOC(d.paginate(d.Items)) {
		if l, ok := it.B.(*TocLine); ok && strings.ContainsAny(plainText(l.runs()), "12") {
			t.Errorf("TOC line %q contains a footnote number", plainText(l.runs()))
		}
	}
}

// Backslash escapes and entity/numeric references are resolved in text, as
// CommonMark specifies, but code spans stay literal. They used to print raw.
func TestEscapesAndEntities(t *testing.T) {
	th := theme(t, nil)
	text := func(src string) string {
		var sb strings.Builder
		for _, it := range parse(t, src, th).Items {
			switch v := it.B.(type) {
			case *Para:
				sb.WriteString(plainText(v.Runs))
			case *Table:
				for _, r := range v.Rows {
					for _, c := range r {
						sb.WriteString(plainText(c.Runs) + "|")
					}
				}
			}
		}
		return sb.String()
	}
	for src, want := range map[string]string{
		`\*not em\* and \# and \\`:                   `*not em* and # and \`,
		`&copy; &amp; &lt;tag&gt; &nbsp;x`:           "© & <tag>  x",
		`&#8212; &#x2014; &#169;`:                    "— — ©",
		`\&copy; stays literal`:                      "&copy; stays literal",
		"`&copy; \\* in code`":                       `&copy; \* in code`,
		"[a \\[link\\]](https://x.y)":                "a [link]",
		"| a | b |\n|---|---|\n| x \\| y | &lt; |\n": "x | y|<|",
	} {
		if got := text(src); got != want {
			t.Errorf("%q -> %q, want %q", src, got, want)
		}
	}
}

// HTML comments (and <!DOCTYPE>, <?pi?>) are never content: they must not
// render as text and must not warn. A multi-line comment block used to lose its
// closing "-->" line and print its contents.
func TestHTMLCommentsAreSilent(t *testing.T) {
	th := theme(t, nil)
	src := "Before.\n\n" +
		"<!--\nmulti-line SECRET1\ncomment\n-->\n\n" +
		"<!-- one-line SECRET2 -->\n\n" +
		"<!-- has a > inside SECRET3 -->\n\n" +
		"Inline <!-- SECRET4 --> text stays.\n\n" +
		"<!DOCTYPE html>\n\n" +
		"<?xml version=\"1.0\"?>\n\n" +
		"<div>kept text</div>\n\n" +
		"After.\n\n" +
		"<!-- unclosed SECRET5\n"
	d := parse(t, src, th)
	var sb strings.Builder
	for _, it := range d.Items {
		if p, ok := it.B.(*Para); ok {
			sb.WriteString(plainText(p.Runs) + "\n")
		}
	}
	got := sb.String()
	for _, secret := range []string{"SECRET", "<!--", "-->", "DOCTYPE", "xml"} {
		if strings.Contains(got, secret) {
			t.Errorf("comment leaked %q into the text:\n%s", secret, got)
		}
	}
	for _, want := range []string{"Before.", "Inline", "text stays.", "kept text", "After."} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	// Only the real tag is reported.
	if len(d.Warn) != 1 || d.Warn[0] != "dropped HTML tag <div>" {
		t.Errorf("warnings = %q, want only the <div> one", d.Warn)
	}
}

// A tight list or a definition list is followed by at least BlockSpace, not
// the 0.7 mm between its own items; a nested list inside an item stays tight.
func TestSpaceAfterLists(t *testing.T) {
	th := theme(t, nil)
	for name, src := range map[string]string{
		"tight list": "- a\n- b\n\nNext.\n",
		"ordered":    "1. a\n2. b\n\nNext.\n",
		"deflist":    "Term\n: Definition\n\nNext.\n",
	} {
		items := parse(t, src, th).Items
		last := items[len(items)-2] // the list's last item, before "Next."
		if got := last.B.SpaceAfter(); got < th.BlockSpace {
			t.Errorf("%s: space after the last item = %.1fmm, want >= %.1fmm", name, got, th.BlockSpace)
		}
	}
	items := parse(t, "- outer\n  - inner one\n  - inner two\n- outer two\n", th).Items
	if got := items[2].B.SpaceAfter(); got >= th.BlockSpace {
		t.Errorf("nested list end got %.1fmm after it; a nested list should stay tight", got)
	}
}

// Email autolinks get a mailto: URL; goldmark hands back the bare address.
func TestEmailAutolinkMailto(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "Mail <me@example.com>, me2@example.com, <mailto:me3@example.com>, <https://x.y>.\n", th)
	got := map[string]bool{}
	for _, u := range d.Links {
		got[u] = true
	}
	for _, want := range []string{"mailto:me@example.com", "mailto:me2@example.com", "mailto:me3@example.com", "https://x.y"} {
		if !got[want] {
			t.Errorf("no link %q; have %v", want, got)
		}
	}
}

// Tabs expand to the next tab stop, so tab-separated columns line up.
func TestTabStops(t *testing.T) {
	th := theme(t, func(c *config.Config) { c.Code.TabWidth = 4 })
	d := parse(t, "```\na\tb\nabc\tb\nabcde\tb\n\tx\n```\n", th)
	code := d.Items[0].B.(*Code)
	want := []string{"a   b", "abc b", "abcde   b", "    x"}
	for i, ln := range code.Lines {
		if got := plainText(ln); got != want[i] {
			t.Errorf("line %d = %q, want %q", i, got, want[i])
		}
	}
}

// An input with nothing to render says so instead of silently producing a
// blank page; front matter or comments alone count as nothing.
func TestEmptyInputWarns(t *testing.T) {
	th := theme(t, nil)
	for name, src := range map[string]string{
		"empty": "", "blank lines": "\n\n  \n", "front matter only": "---\ntitle: T\n---\n", "comment only": "<!-- todo -->\n",
	} {
		d := parse(t, src, th)
		if len(d.Warn) == 0 || !strings.Contains(d.Warn[len(d.Warn)-1], "no content") {
			t.Errorf("%s: warnings %v, want a 'no content' warning", name, d.Warn)
		}
	}
	if d := parse(t, "Text.\n", th); len(d.Warn) != 0 {
		t.Errorf("non-empty input warned: %v", d.Warn)
	}
}

// List marker edge cases (#73).
func TestListMarkerEdgeCases(t *testing.T) {
	th := theme(t, nil)
	markersOf := func(src string) []*Marker {
		var out []*Marker
		for _, it := range parse(t, src, th).Items {
			if it.Marker != nil {
				out = append(out, it.Marker)
			}
		}
		return out
	}

	// A wide ordered label widens the list's step so it fits its gutter.
	var sb strings.Builder
	for i := 98; i <= 102; i++ {
		sb.WriteString(strconv.Itoa(i) + ". item\n")
	}
	ms := markersOf("98. a\n99. b\n100. c\n")
	f := ms[0].Face
	if w := f.TextWidth("100.") + 1.6; ms[2].Step < w {
		t.Errorf("step %.1fmm too narrow for \"100.\" (%.1fmm incl. gap)", ms[2].Step, w)
	}
	if ms := markersOf("- a\n- b\n"); ms[0].Step != th.IndentStep {
		t.Errorf("bullet list step = %.1f, want IndentStep %.1f", ms[0].Step, th.IndentStep)
	}

	// Empty items keep their markers, including a bare task box.
	if got := len(markersOf("- a\n-\n- c\n")); got != 3 {
		t.Errorf("empty item: %d markers, want 3", got)
	}
	if ms := markersOf("- [ ]\n- [x] done\n"); len(ms) != 2 || ms[0].Shape != ShapeBox {
		t.Errorf("bare task box lost its marker: %d markers", len(ms))
	}

	// "- - x": the inner item carries the outer bullet too.
	ms = markersOf("- - x\n")
	if len(ms) != 1 || ms[0].Outer == nil || ms[0].Outer.Text != "\u2022" || ms[0].Text != "\u25e6" {
		t.Fatalf("\"- - x\": want inner ◦ with outer •, got %+v", ms)
	}
}

// A marker before a code block, table or heading sits on that block's first
// baseline, not at its top edge.
func TestMarkerOnFirstBaseline(t *testing.T) {
	th := theme(t, nil)
	for name, src := range map[string]string{
		"code":    "- ```\n  code line\n  ```\n",
		"table":   "- | a | b |\n  |---|---|\n  | 1 | 2 |\n",
		"heading": "- ## Heading in item\n",
	} {
		it := parse(t, src, th).Items[0]
		fb, ok := it.B.(firstBaseliner)
		if !ok {
			t.Fatalf("%s: %T has no firstBaseline", name, it.B)
		}
		if top := baselineOf(it.Marker.Face, th.textOpts()); fb.firstBaseline(th.ContentW()) <= top+0.1 {
			t.Errorf("%s: first baseline %.2fmm is not below a body line's %.2fmm", name, fb.firstBaseline(th.ContentW()), top)
		}
	}
}

// typography.smart_punctuation turns straight quotes, -- / --- and ... into
// typographic characters in prose (not code), and is off by default.
func TestSmartPunctuation(t *testing.T) {
	src := "\"Quoted\" and 'single', it's -- en --- em... `\"code\" -- ...` a << b >> c\n"
	off := plainText(parse(t, src, theme(t, nil)).Items[0].B.(*Para).Runs)
	if off != "\"Quoted\" and 'single', it's -- en --- em... \"code\" -- ... a << b >> c" {
		t.Errorf("default changed punctuation: %q", off)
	}
	th := theme(t, func(c *config.Config) { c.Type.SmartPunct = true })
	on := plainText(parse(t, src, th).Items[0].B.(*Para).Runs)
	want := "“Quoted” and ‘single’, it’s – en — em… \"code\" -- ... a << b >> c"
	if on != want {
		t.Errorf("smart punctuation:\n got %q\nwant %q", on, want)
	}
}

// number_sections prefixes headings with hierarchical numbers; anchors and
// the document title stay unnumbered, and number_from leaves higher levels
// (an H1 title) alone. A skipped level counts as 0.
func TestSectionNumbers(t *testing.T) {
	src := "# Title\n\n## Intro\n\n### Detail\n\n### More\n\n## Design\n\n#### Deep\n\n## End\n"
	type h struct{ plain, id string }
	heads := func(th *Theme) ([]h, string) {
		d := parse(t, src, th)
		var out []h
		for _, it := range d.Items {
			if v, ok := it.B.(*Heading); ok {
				out = append(out, h{v.Plain, v.ID})
			}
		}
		return out, d.Title
	}
	th := theme(t, func(c *config.Config) { c.Document.NumberSects = true; c.Document.NumberFrom = 2 })
	for _, r := range numGap {
		if f := th.Text.Face(th.HeadSize[1], th.FG); !covers(f, r) {
			t.Fatalf("heading face lacks %U, used after section numbers", r)
		}
	}
	got, title := heads(th)
	want := []h{{"Title", "title"}, {"1" + numGap + "Intro", "intro"}, {"1.1" + numGap + "Detail", "detail"}, {"1.2" + numGap + "More", "more"},
		{"2" + numGap + "Design", "design"}, {"2.0.1" + numGap + "Deep", "deep"}, {"3" + numGap + "End", "end"}}
	if len(got) != len(want) {
		t.Fatalf("got %d headings, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("heading %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if title != "Title" {
		t.Errorf("title = %q, want it unnumbered", title)
	}
	// number_from = 1 numbers the H1 too; off by default.
	th = theme(t, func(c *config.Config) { c.Document.NumberSects = true })
	if got, _ := heads(th); got[0].plain != "1"+numGap+"Title" || got[1].plain != "1.1"+numGap+"Intro" {
		t.Errorf("number_from=1: %+v", got[:2])
	}
	if got, _ := heads(theme(t, nil)); got[1].plain != "Intro" {
		t.Errorf("numbering on by default: %+v", got[1])
	}
}

// GitHub alerts: the marker line becomes a coloured bold label kept with the
// body, the bar takes the alert colour, and "[!TIP] text" on one line stays an
// ordinary quote.
func TestGitHubAlerts(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "> [!NOTE]\n> Useful *info* here.\n\n> [!warning]\n> Careful.\n\n> [!TIP] same line\n", th)
	items := d.Items
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5 (label, body, label, body, plain quote)", len(items))
	}
	label := items[0].B.(*Para)
	if plainText(label.Runs) != "Note" || !label.KeepWithNext() {
		t.Errorf("note label = %q keep=%v", plainText(label.Runs), label.KeepWithNext())
	}
	if label.Runs[0].Face.Fill.Color != th.Alert["note"] {
		t.Error("note label is not in the note colour")
	}
	if body := plainText(items[1].B.(*Para).Runs); body != "Useful info here." {
		t.Errorf("note body = %q (marker not removed?)", body)
	}
	if c := items[1].Bars[0].Color; c != th.Alert["note"] {
		t.Errorf("note bar colour = %v", c)
	}
	if plainText(items[2].B.(*Para).Runs) != "Warning" || items[3].Bars[0].Color != th.Alert["warning"] {
		t.Error("lower-case [!warning] not recognised")
	}
	plain := items[4]
	if got := plainText(plain.B.(*Para).Runs); got != "[!TIP] same line" || plain.Bars[0].Color != (color.RGBA{}) {
		t.Errorf("one-line marker should be a plain quote, got %q", got)
	}
}
