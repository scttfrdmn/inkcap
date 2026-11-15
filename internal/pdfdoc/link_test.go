package pdfdoc

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var rectRe = regexp.MustCompile(`/Rect\s*\[\s*([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s*\]`)

// linkBands renders src and returns each link annotation's vertical extent in
// y-down millimetres from the top of the page, plus the single placed block.
func linkBands(t *testing.T, src string) ([][2]float64, Placed) {
	t.Helper()
	th := theme(t, nil)
	d := parse(t, src, th)
	pages := d.paginate(d.Items)
	if len(pages) != 1 || len(pages[0].Items) != 1 {
		t.Fatalf("want one page with one block, got %d pages", len(pages))
	}
	var buf bytes.Buffer
	if err := d.Render(&buf, ""); err != nil {
		t.Fatal(err)
	}
	const mmPerPt = 25.4 / 72
	var bands [][2]float64
	for _, m := range rectRe.FindAllSubmatch(buf.Bytes(), -1) {
		y0, _ := strconv.ParseFloat(string(m[2]), 64)
		y1, _ := strconv.ParseFloat(string(m[4]), 64)
		// PDF is y-up from the page bottom; convert to y-down from the top.
		bands = append(bands, [2]float64{th.PageH - y1*mmPerPt, th.PageH - y0*mmPerPt})
	}
	if len(bands) == 0 {
		t.Fatal("no link annotations in the PDF")
	}
	return bands, pages[0].Items[0]
}

// A link's click area must cover its text: it lies inside the block that
// holds the link, and on the line the link is on.
func TestLinkRectCoversText(t *testing.T) {
	const tol = 0.5 // mm

	bands, pl := linkBands(t, "Some text then a [link here](https://example.com) and more.\n")
	for _, b := range bands {
		if b[0] < pl.Y-tol || b[1] > pl.Y+pl.H+tol {
			t.Errorf("one-line paragraph: link spans %.1f–%.1fmm, block is %.1f–%.1fmm", b[0], b[1], pl.Y, pl.Y+pl.H)
		}
	}

	// A link only on the last line of a long paragraph must be in its lower half.
	long := strings.Repeat("Plain words without any link at all here. ", 12) + "[tail link](https://example.com)\n"
	bands, pl = linkBands(t, long)
	for _, b := range bands {
		if mid := (b[0] + b[1]) / 2; mid < pl.Y+pl.H/2 || b[1] > pl.Y+pl.H+tol {
			t.Errorf("last-line link spans %.1f–%.1fmm, block is %.1f–%.1fmm", b[0], b[1], pl.Y, pl.Y+pl.H)
		}
	}

	// Links inside table cells go through the same drawRuns path.
	bands, pl = linkBands(t, "| a | b |\n|---|---|\n| x | [cell link](https://example.com) |\n")
	for _, b := range bands {
		// The link is in the body row, below the header: the table's lower half.
		if mid := (b[0] + b[1]) / 2; mid < pl.Y+pl.H/2 || b[1] > pl.Y+pl.H+tol {
			t.Errorf("table: link spans %.1f–%.1fmm, table is %.1f–%.1fmm", b[0], b[1], pl.Y, pl.Y+pl.H)
		}
	}
}
