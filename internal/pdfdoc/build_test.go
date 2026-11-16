package pdfdoc

import (
	"bytes"
	"strings"
	"testing"

	"github.com/scttfrdmn/inkcap/internal/config"
	"github.com/tdewolff/canvas"
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
		"3) a\n4) b\n": {"3.", "4."},
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
