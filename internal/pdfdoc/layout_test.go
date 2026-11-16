package pdfdoc

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/scttfrdmn/canvas"
	"github.com/scttfrdmn/inkcap/internal/config"
)

func theme(t *testing.T, tweak func(*config.Config)) *Theme {
	t.Helper()
	c := config.Default()
	if tweak != nil {
		tweak(&c)
	}
	th, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func parse(t *testing.T, src string, th *Theme) *Doc {
	t.Helper()
	d, err := Parse([]byte(src), ".", th)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A heading must never be the last thing on a page.
func TestHeadingNeverStranded(t *testing.T) {
	th := theme(t, nil)
	var sb strings.Builder
	sb.WriteString("# Doc\n\n")
	// Sweep the body length so a heading lands near the foot of a page in at
	// least one of these; a single fixture would be luck, not a test.
	for i := 0; i < 40; i++ {
		sb.WriteString(strings.Repeat("Filler sentence for vertical space. ", 3) + "\n\n")
		sb.WriteString("## Section\n\nBody under the section.\n\n")
	}
	d := parse(t, sb.String(), th)
	for i, p := range d.paginate(d.Items) {
		if len(p.Items) == 0 {
			continue
		}
		last := p.Items[len(p.Items)-1]
		if h, ok := last.It.B.(*Heading); ok {
			t.Errorf("page %d ends with heading %q", i+1, h.Plain)
		}
	}
}

// A chain of headings (H2, H3, H4 with nothing between them) must travel
// together with the start of its body: no page may end with any of them. The
// keep-with-next check used to look only one item ahead.
func TestHeadingChainNeverStranded(t *testing.T) {
	th := theme(t, nil)
	hits := 0
	for n := 10; n <= 45; n++ {
		var sb strings.Builder
		sb.WriteString("# Doc\n\n")
		for i := 0; i < n; i++ {
			sb.WriteString("Paragraph filler text to take up vertical space on the page.\n\n")
		}
		sb.WriteString("## Section Two\n\n### Sub Three\n\n#### Deep Four\n\nFinal paragraph.\n")
		d := parse(t, sb.String(), th)
		pages := d.paginate(d.Items)
		if len(pages) > 1 {
			hits++
		}
		for i, p := range pages {
			if len(p.Items) == 0 {
				continue
			}
			if h, ok := p.Items[len(p.Items)-1].It.B.(*Heading); ok {
				t.Errorf("n=%d: page %d ends with heading %q", n, i+1, h.Plain)
			}
		}
	}
	if hits == 0 {
		t.Fatal("no case crossed a page; test is vacuous")
	}
}

// Paragraph fragments must respect the orphan and widow thresholds.
func TestOrphansAndWidows(t *testing.T) {
	th := theme(t, nil)
	body := strings.Repeat("The perishable rectangle is destroyed value. ", 40)
	var sb strings.Builder
	for i := 0; i < 12; i++ {
		sb.WriteString(body + "\n\n")
	}
	d := parse(t, sb.String(), th)
	pages := d.paginate(d.Items)

	w := th.ContentW()
	frags := 0
	for _, p := range pages {
		for _, pl := range p.Items {
			para, ok := pl.It.B.(*Para)
			if !ok {
				continue
			}
			n := len(extractLines(buildText(para.Runs, w, para.Align, para.Opts)))
			if n == 0 {
				continue
			}
			// A fragment shorter than the threshold is only legal if it's the
			// whole (short) paragraph — which these aren't; they're all long.
			if n < th.Orphans {
				t.Errorf("paragraph fragment of %d line(s), want >= %d", n, th.Orphans)
			}
			frags++
		}
	}
	if frags <= 12 {
		t.Fatalf("nothing split; test is vacuous (%d fragments for 12 paragraphs)", frags)
	}
}

// Nothing may be laid out below the bottom margin, footnotes included.
func TestNothingOverflowsTheMargin(t *testing.T) {
	th := theme(t, nil)
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString("Paragraph with a note[^" + itoa(i) + "] in it. " +
			strings.Repeat("More text to push things down the page. ", 6) + "\n\n")
	}
	for i := 1; i <= 30; i++ {
		sb.WriteString("[^" + itoa(i) + "]: The content of footnote " + itoa(i) + ".\n\n")
	}
	d := parse(t, sb.String(), th)

	bottom := th.PageH - th.MarginBottom
	for i, p := range d.paginate(d.Items) {
		notesH := d.notesHeight(p.Notes)
		for _, pl := range p.Items {
			if end := pl.Y + pl.H; end > bottom-notesH+0.01 {
				t.Errorf("page %d: block ends at %.1fmm, past the text area (%.1fmm)",
					i+1, end, bottom-notesH)
			}
		}
	}
}

// A footnote must land on the page that references it.
func TestFootnotesLandWithTheirRefs(t *testing.T) {
	th := theme(t, nil)
	var sb strings.Builder
	for i := 1; i <= 25; i++ {
		sb.WriteString("Body[^" + itoa(i) + "]. " +
			strings.Repeat("Filler to consume vertical space. ", 8) + "\n\n")
	}
	for i := 1; i <= 25; i++ {
		sb.WriteString("[^" + itoa(i) + "]: Note " + itoa(i) + ".\n\n")
	}
	d := parse(t, sb.String(), th)
	pages := d.paginate(d.Items)
	if len(pages) < 2 {
		t.Fatal("test needs multiple pages to be meaningful")
	}
	for i, p := range pages {
		for _, pl := range p.Items {
			for _, n := range pl.It.B.Refs(d.Notes) {
				if !contains(p.Notes, n) {
					t.Errorf("page %d references note %d but doesn't carry it", i+1, n)
				}
			}
		}
	}
}

// A table taller than a page must still terminate, and repeat its header.
func TestOversizedTableRowTerminates(t *testing.T) {
	th := theme(t, nil)
	huge := strings.Repeat("This cell is far taller than one page. ", 200)
	src := "| A | B |\n|---|---|\n| x | " + huge + " |\n| y | short |\n"
	d := parse(t, src, th)

	pages := d.paginate(d.Items) // infinite-loop guard: this returning at all is the test
	if len(pages) < 2 {
		t.Fatalf("expected the row to split across pages, got %d page(s)", len(pages))
	}
	for i, p := range pages {
		for _, pl := range p.Items {
			tb, ok := pl.It.B.(*Table)
			if !ok {
				continue
			}
			if len(tb.Head) == 0 {
				t.Errorf("page %d: table fragment lost its header", i+1)
			}
		}
	}
}

// A long table must repeat its header on every continuation fragment.
func TestTableHeaderRepeats(t *testing.T) {
	th := theme(t, nil)
	var sb strings.Builder
	sb.WriteString("| Node | Role | Notes |\n|---|---|---|\n")
	for i := 0; i < 60; i++ {
		sb.WriteString("| node-" + itoa(i) + " | worker | some notes here |\n")
	}
	d := parse(t, sb.String(), th)
	pages := d.paginate(d.Items)
	if len(pages) < 2 {
		t.Fatalf("expected the table to span pages, got %d", len(pages))
	}
	frags := 0
	for i, p := range pages {
		for _, pl := range p.Items {
			tb, ok := pl.It.B.(*Table)
			if !ok {
				continue
			}
			frags++
			if len(tb.Head) == 0 {
				t.Errorf("page %d: table fragment lost its header", i+1)
			}
			if len(tb.Rows) == 0 {
				t.Errorf("page %d: table fragment has a header but no rows", i+1)
			}
		}
	}
	if frags < 2 {
		t.Fatalf("table never split; test is vacuous (%d fragment)", frags)
	}
}

// The table of contents must reach a fixed point rather than oscillating.
func TestTOCConverges(t *testing.T) {
	th := theme(t, func(c *config.Config) { c.Document.TOC = true })
	var sb strings.Builder
	sb.WriteString("# Title\n\n")
	for i := 0; i < 25; i++ {
		sb.WriteString("## Section " + itoa(i) + "\n\n" +
			strings.Repeat("Body text. ", 40) + "\n\n")
	}
	d := parse(t, sb.String(), th)

	pages := d.paginate(d.Items)
	var toc []Item
	iters := 0
	for i := 0; i < 4; i++ {
		iters++
		next := d.buildTOC(pages)
		if tocEqual(toc, next) {
			break
		}
		toc = next
		pages = d.paginate(spliceTOC(d.Items, toc))
	}
	if iters >= 4 {
		t.Error("TOC page numbers never settled")
	}
	if len(toc) == 0 {
		t.Fatal("no TOC produced")
	}
	// Every entry's page number must match where that heading actually landed.
	got := map[string]int{}
	for i, p := range pages {
		for _, pl := range p.Items {
			if h, ok := pl.It.B.(*Heading); ok && !h.fromTOC {
				if _, seen := got[h.Plain]; !seen {
					got[h.Plain] = i + 1
				}
			}
		}
	}
	for _, it := range toc {
		l, ok := it.B.(*TocLine)
		if !ok {
			continue
		}
		name := plainText(l.Runs)
		if want := got[name]; want != l.Page {
			t.Errorf("TOC says %q is on page %d; it is on page %d", name, l.Page, want)
		}
	}
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// A paragraph made of hard line breaks (poetry, addresses) must split across
// pages like any other, without losing, duplicating, or re-indenting lines.
func TestHardBrokenParagraphSplits(t *testing.T) {
	th := theme(t, nil)
	const n = 150 // roughly three pages of single lines
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString("verse line " + itoa(i) + "  \n")
	}
	d := parse(t, sb.String(), th)
	pages := d.paginate(d.Items)

	bottom := th.PageH - th.MarginBottom
	var got []string
	frags := 0
	for i, p := range pages {
		for _, pl := range p.Items {
			if end := pl.Y + pl.H; end > bottom+0.01 {
				t.Errorf("page %d: block ends at %.1fmm, past the bottom margin (%.1fmm)", i+1, end, bottom)
			}
			para, ok := pl.It.B.(*Para)
			if !ok {
				continue
			}
			frags++
			for _, ln := range strings.Split(plainText(para.Runs), "\n") {
				got = append(got, ln)
			}
		}
	}
	if frags < 3 {
		t.Fatalf("paragraph split into %d fragment(s), want >= 3", frags)
	}
	if len(got) != n {
		t.Fatalf("got %d lines across fragments, want %d", len(got), n)
	}
	for i, ln := range got {
		if want := "verse line " + itoa(i); ln != want {
			t.Fatalf("line %d = %q, want %q", i, ln, want)
		}
	}
}

// Blockquote bars: one unbroken bar per quote level (spanning the gaps between
// paragraphs), one bar per nesting level, lists inside quotes keep the bar,
// and two separate quotes get separate bars.
func TestQuoteBars(t *testing.T) {
	th := theme(t, nil)
	bars := func(src string) []barSeg {
		d := parse(t, src, th)
		pages := d.paginate(d.Items)
		if len(pages) != 1 {
			t.Fatalf("want 1 page")
		}
		return quoteBars(pages[0].Items, th)
	}
	itemsIn := func(src string) []Item { return parse(t, src, th).Items }

	// Two paragraphs in one quote: a single segment covering both.
	src := "> first paragraph\n>\n> second paragraph\n"
	segs := bars(src)
	if len(segs) != 1 {
		t.Fatalf("two-paragraph quote: %d segments, want 1 continuous bar", len(segs))
	}
	d := parse(t, src, th)
	pl := d.paginate(d.Items)[0].Items
	if segs[0].Y0 > pl[0].Y+0.01 || segs[0].Y1 < pl[1].Y+pl[1].H-0.01 {
		t.Errorf("bar %.1f-%.1f doesn't span both paragraphs (%.1f-%.1f)", segs[0].Y0, segs[0].Y1, pl[0].Y, pl[1].Y+pl[1].H)
	}

	// Nested quote: the inner item carries both levels, at different x.
	segs = bars("> outer\n>\n> > inner\n")
	if len(segs) != 2 || segs[0].X == segs[1].X {
		t.Errorf("nested quote: segments %+v, want 2 at different x", segs)
	}

	// A list inside a quote keeps the bar.
	for _, it := range itemsIn("> - one\n> - two\n") {
		if len(it.Bars) != 1 {
			t.Errorf("list item inside a quote has %d bars, want 1", len(it.Bars))
		}
	}

	// Two separate quotes: two bars, not one merged across the gap.
	if segs := bars("> one\n\n> two\n"); len(segs) != 2 {
		t.Errorf("two separate quotes: %d segments, want 2", len(segs))
	}
}

// When a page's footnotes can't all fit (here: one paragraph with 80 long
// notes), the excess continues on following pages instead of being drawn over
// the body. Every note appears exactly once, in order, never before the page
// that references it, and no page's notes overlap its text.
func TestFootnoteOverflowContinues(t *testing.T) {
	th := theme(t, nil)
	var sb strings.Builder
	sb.WriteString("# Doc\n\nA paragraph with many notes")
	for i := 1; i <= 80; i++ {
		sb.WriteString("[^" + itoa(i) + "]")
	}
	sb.WriteString(".\n\nAnother paragraph.\n\n")
	long := strings.Repeat("A long footnote sentence that takes up real space. ", 3)
	for i := 1; i <= 80; i++ {
		sb.WriteString("[^" + itoa(i) + "]: " + long + "\n\n")
	}
	d := parse(t, sb.String(), th)
	pages := d.paginate(d.Items)

	bottom := th.PageH - th.MarginBottom
	refPage := map[int]int{}
	for i, p := range pages {
		for _, pl := range p.Items {
			for _, n := range pl.It.B.Refs(d.Notes) {
				if _, ok := refPage[n]; !ok {
					refPage[n] = i
				}
			}
		}
	}
	var seen []int
	for i, p := range pages {
		nh := d.notesHeight(p.Notes)
		if nh > th.ContentH()+0.01 {
			t.Errorf("page %d: notes %.0fmm taller than the text area", i+1, nh)
		}
		for _, pl := range p.Items {
			if end := pl.Y + pl.H; end > bottom-nh+0.01 {
				t.Errorf("page %d: body ends at %.1fmm, under notes starting at %.1fmm", i+1, end, bottom-nh)
			}
		}
		for _, n := range p.Notes {
			if i < refPage[n] {
				t.Errorf("note %d on page %d, before its reference on page %d", n, i+1, refPage[n]+1)
			}
			seen = append(seen, n)
		}
	}
	if len(seen) != 80 {
		t.Fatalf("%d notes placed, want 80", len(seen))
	}
	for i, n := range seen {
		if n != i+1 {
			t.Fatalf("notes out of order or duplicated at %d: %v", i, seen)
		}
	}
	if len(pages) < 3 {
		t.Fatalf("only %d pages; the notes didn't need to continue (vacuous)", len(pages))
	}
}

// line_height is a multiple of the font size, like CSS: at the default 1.5,
// baselines of body text are 1.5 x 10.5pt apart. It used to mean extra leading
// over the font's natural spacing, which set lines about 2x apart.
func TestLineHeightIsMultipleOfSize(t *testing.T) {
	for _, lh := range []float64{1.5, 1.2} {
		th := theme(t, func(c *config.Config) { c.Type.LineHeight = lh })
		f := th.Text.Face(th.BodySize, th.FG)
		one := buildText([]Run{{Face: f, Text: "x"}}, 0, canvas.Left, th.textOpts()).Height
		two := buildText([]Run{{Face: f, Text: "x\nx"}}, 0, canvas.Left, th.textOpts()).Height
		want := pt(th.BodySize) * lh
		if pitch := two - one; pitch < want*0.98 || pitch > want*1.02 {
			t.Errorf("line_height %v: pitch %.2fmm, want %.2fmm (%.2fx size)", lh, pitch, want, pitch/pt(th.BodySize))
		}
		if bl := th.bodyLine(); bl < want*0.98 || bl > want*1.02 {
			t.Errorf("line_height %v: bodyLine %.2fmm, want %.2fmm", lh, bl, want)
		}
	}
}

// Page breaks: an HTML comment or \newpage starts a new page; a break at the
// top of a page (or two in a row) never makes a blank page; h1_new_page
// breaks before every H1 after the first; nothing warns.
func TestPageBreaks(t *testing.T) {
	firstTexts := func(d *Doc, pages []Page) []string {
		var out []string
		for _, p := range pages {
			if len(p.Items) == 0 {
				out = append(out, "")
				continue
			}
			switch v := p.Items[0].It.B.(type) {
			case *Para:
				out = append(out, plainText(v.Runs))
			case *Heading:
				out = append(out, v.Plain)
			default:
				out = append(out, "?")
			}
		}
		return out
	}
	th := theme(t, nil)
	d := parse(t, "One.\n\n<!-- pagebreak -->\n\nTwo.\n\n\\newpage\n\n<!-- PAGE-BREAK -->\n\nThree.\n", th)
	if len(d.Warn) != 0 {
		t.Errorf("warnings: %v", d.Warn)
	}
	if got := strings.Join(firstTexts(d, d.paginate(d.Items)), "|"); got != "One.|Two.|Three." {
		t.Errorf("pages start with %q, want One.|Two.|Three.", got)
	}

	th = theme(t, func(c *config.Config) { c.Document.H1NewPage = true; c.Document.TOC = true })
	d = parse(t, "# First\n\nLead.\n\n## Sub\n\nx\n\n# Second\n\ny\n\n# Third\n\nz\n", th)
	pages := d.paginate(spliceTOC(d.Items, d.buildTOC(d.paginate(d.Items))))
	got := firstTexts(d, pages)
	if strings.Join(got, "|") != "First|Second|Third" {
		t.Errorf("h1_new_page: pages start with %q, want First|Second|Third", got)
	}
	// the TOC stays on the title page
	foundTOC := false
	for _, pl := range pages[0].Items {
		if _, ok := pl.It.B.(*TocLine); ok {
			foundTOC = true
		}
	}
	if !foundTOC {
		t.Error("TOC is not on the first page")
	}
}

// Footnotes hold blocks: several paragraphs, a list and a code block survive
// (they used to be flattened into one line, with the code dropped), the
// number leads the first paragraph, and endnotes mode carries the same blocks.
func TestBlockFootnotes(t *testing.T) {
	src := "Body text[^a] and more[^b].\n\n" +
		"[^a]: First paragraph.\n\n    Second paragraph.\n\n    - one\n    - two\n\n" +
		"[^b]: Code:\n\n    ```go\n    x := 1\n    ```\n"
	for _, mode := range []string{"page", "end"} {
		th := theme(t, func(c *config.Config) { c.Document.Footnotes = mode })
		d := parse(t, src, th)
		var kinds []string
		var texts []string
		items := d.noteItems(1)
		items = append(items, d.noteItems(2)...)
		for _, it := range items {
			switch v := it.B.(type) {
			case *Para:
				kinds = append(kinds, "para")
				texts = append(texts, plainText(v.Runs))
			case *Code:
				kinds = append(kinds, "code")
				texts = append(texts, plainText(v.Lines[0]))
			default:
				kinds = append(kinds, "?")
			}
		}
		want := "para para para para para code"
		if got := strings.Join(kinds, " "); got != want {
			t.Errorf("%s: note blocks = %q, want %q", mode, got, want)
		}
		if len(texts) < 4 || texts[0] != "1 First paragraph." || texts[1] != "Second paragraph." || texts[2] != "one" {
			t.Errorf("%s: note texts = %q", mode, texts)
		}
		if mode == "end" {
			// the blocks are in the flow after the Notes heading
			sawCode := false
			for _, it := range d.endNotes(d.Items) {
				if _, ok := it.B.(*Code); ok {
					sawCode = true
				}
			}
			if !sawCode {
				t.Error("end mode: the footnote's code block is not in the flow")
			}
			continue
		}
		// page mode: the reserved height matches what is drawn, and it renders
		_, end := d.noteFlow(d.noteItems(1), 10)
		if got := d.noteHeight(1); math.Abs((end-10)-got) > 1e-9 {
			t.Errorf("noteHeight %.2f != drawn height %.2f", got, end-10)
		}
		var buf bytes.Buffer
		if err := d.Render(&buf, ""); err != nil {
			t.Fatal(err)
		}
	}
}

// title_page puts a cover built from front matter on page 1 alone; the TOC
// follows the cover and its numbers count from the page after it.
func TestTitlePage(t *testing.T) {
	th := theme(t, func(c *config.Config) { c.Document.TitlePage = true; c.Document.TOC = true })
	src := "---\ntitle: The Report\nsubtitle: A subtitle\nauthor: Jane Doe\ndate: 2026-11-16\n---\n\n# Intro\n\nText.\n\n## Part\n\nMore.\n"
	d := parse(t, src, th)
	tp, ok := d.Items[0].B.(*TitlePage)
	if !ok {
		t.Fatalf("first item is %T, want *TitlePage", d.Items[0].B)
	}
	if tp.Title != "The Report" || tp.Subtitle != "A subtitle" || tp.Author != "Jane Doe" || tp.Date != "2026-11-16" {
		t.Errorf("cover = %+v", tp)
	}
	items := spliceTOC(d.Items, d.buildTOC(d.paginate(d.Items)))
	pages := d.paginate(items)
	if len(pages[0].Items) != 1 {
		t.Errorf("cover page holds %d items, want only the cover", len(pages[0].Items))
	}
	if d.coverPages(pages) != 1 {
		t.Error("coverPages != 1")
	}
	// The TOC opens the page after the cover, and "Intro" is numbered from
	// that page as 1.
	if h, ok := pages[1].Items[0].It.B.(*Heading); !ok || !h.fromTOC {
		t.Errorf("page 2 starts with %T, want the Contents heading", pages[1].Items[0].It.B)
	}
	for _, it := range d.buildTOC(pages) {
		if l, ok := it.B.(*TocLine); ok && plainText(l.runs()) == "Intro" && l.Page != 1 {
			t.Errorf("TOC lists Intro on page %d, want 1 (cover unnumbered)", l.Page)
		}
	}
	var buf bytes.Buffer
	if err := d.Render(&buf, ""); err != nil {
		t.Fatal(err)
	}
	// Without a title anywhere there is no cover, and it says so.
	d = parse(t, "Just text.\n", th)
	if _, ok := d.Items[0].B.(*TitlePage); ok || len(d.Warn) == 0 {
		t.Errorf("no title: got a cover or no warning (%v)", d.Warn)
	}
	// Off by default.
	if _, ok := parse(t, src, theme(t, nil)).Items[0].B.(*TitlePage); ok {
		t.Error("cover added with title_page off")
	}
}
