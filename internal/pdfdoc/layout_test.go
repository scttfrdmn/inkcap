package pdfdoc

import (
	"strings"
	"testing"

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
