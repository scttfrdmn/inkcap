package pdfdoc

import (
	"bytes"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/inkcap/internal/config"
)

// Heading anchors follow GitHub's slugs, so links written for GitHub resolve.
func TestAnchorIDs(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "# Getting Started!\n\n## `run()` & friends\n\n## Café 2.0\n\n## Getting Started!\n\n## Getting Started!\n", th)
	var got []string
	for _, it := range d.Items {
		if h, ok := it.B.(*Heading); ok {
			got = append(got, h.ID)
		}
	}
	want := []string{"getting-started", "run--friends", "café-20", "getting-started-1", "getting-started-2"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ids = %q, want %q", got, want)
	}
}

func TestUnknownAnchorWarnsOnce(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "# A\n\n[x](#a) [y](#missing) [z](#missing) [w](#caf%C3%A9)\n\n## Café\n", th)
	n := 0
	for _, w := range d.Warn {
		if strings.Contains(w, "#") {
			n++
			if !strings.Contains(w, "#missing") {
				t.Errorf("unexpected warning %q", w)
			}
		}
	}
	if n != 1 {
		t.Errorf("got %d anchor warnings, want 1: %v", n, d.Warn)
	}
}

var (
	destsRe = regexp.MustCompile(`/Dests<</Names\[([^\]]*)\]>>`)
	pairRe  = regexp.MustCompile(`\(([^)]*)\)\s*(\d+) 0 R`)
	kidsRe  = regexp.MustCompile(`/Kids\[([^\]]*)\]`)
	refRe   = regexp.MustCompile(`(\d+) 0 R`)
	gotoRe  = regexp.MustCompile(`/Dest\(([^)]*)\)`)
)

// An in-document link and every TOC entry must target a named destination,
// and each destination must point at the page and height where its heading
// was actually laid out. Before, "#x" links pointed at names that were never
// defined, and TOC lines weren't links at all.
func TestInternalLinksResolve(t *testing.T) {
	th := theme(t, func(c *config.Config) { c.Document.TOC = true })
	var sb strings.Builder
	sb.WriteString("# Doc\n\nJump to [the far section](#far-away).\n\n")
	for i := 0; i < 60; i++ {
		sb.WriteString(strings.Repeat("Filler sentence for vertical space. ", 4) + "\n\n")
		if i == 30 {
			sb.WriteString("## Middle\n\nBody.\n\n")
		}
	}
	sb.WriteString("## Far away\n\nThe end.\n")
	d := parse(t, sb.String(), th)

	var buf bytes.Buffer
	if err := d.Render(&buf, ""); err != nil {
		t.Fatal(err)
	}
	pdfb := buf.Bytes()

	// Page object numbers, in page order.
	var pageObj []string
	for _, k := range kidsRe.FindAllSubmatch(pdfb, -1) {
		for _, r := range refRe.FindAllSubmatch(k[1], -1) {
			pageObj = append(pageObj, string(r[1]))
		}
	}
	// Named destinations -> (page index, y in pt from the page bottom).
	type dest struct {
		page int
		y    float64
	}
	dests := map[string]dest{}
	m := destsRe.FindSubmatch(pdfb)
	if m == nil {
		t.Fatal("PDF has no named destinations")
	}
	for _, p := range pairRe.FindAllSubmatch(m[1], -1) {
		obj := regexp.MustCompile(`\n` + string(p[2]) + ` 0 obj\s*<</D\[(\d+) 0 R /XYZ [\d.]+ ([\d.]+) 0\]>>`).FindSubmatch(pdfb)
		if obj == nil {
			t.Fatalf("destination %s: object %s not found", p[1], p[2])
		}
		y, _ := strconv.ParseFloat(string(obj[2]), 64)
		pg := -1
		for i, o := range pageObj {
			if o == string(obj[1]) {
				pg = i
			}
		}
		dests[string(p[1])] = dest{pg, y}
	}

	// Where the paginator actually put each heading, with the TOC spliced in
	// exactly as Render does it.
	pages := d.paginate(spliceTOC(d.Items, d.buildTOC(d.paginate(d.Items))))
	const ptPerMm = 72 / 25.4
	checked := 0
	for pi, p := range pages {
		for _, pl := range p.Items {
			h, ok := pl.It.B.(*Heading)
			if !ok || h.ID == "" {
				continue
			}
			got, ok := dests[h.ID]
			if !ok {
				t.Errorf("heading %q (#%s) has no destination", h.Plain, h.ID)
				continue
			}
			if want := (th.PageH - pl.Y) * ptPerMm; got.page != pi || math.Abs(got.y-want) > 0.5 {
				t.Errorf("#%s -> page %d y %.1f, want page %d y %.1f", h.ID, got.page+1, got.y, pi+1, want)
			}
			checked++
		}
	}
	if checked < 3 || dests["far-away"].page < 1 {
		t.Fatalf("test is vacuous: %d headings checked, #far-away on page %d", checked, dests["far-away"].page+1)
	}

	// The body link and one TOC link per heading entry target those names.
	links := map[string]int{}
	for _, g := range gotoRe.FindAllSubmatch(pdfb, -1) {
		links[string(g[1])]++
	}
	if links["far-away"] != 2 { // body link + TOC entry
		t.Errorf("links to #far-away = %d, want 2 (body + TOC)", links["far-away"])
	}
	for _, id := range []string{"doc", "middle"} {
		if links[id] != 1 {
			t.Errorf("TOC links to #%s = %d, want 1", id, links[id])
		}
	}
}
