package pdfdoc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100" viewBox="0 0 200 100">
  <rect width="200" height="100" fill="#3498db"/>
  <circle cx="50" cy="50" r="30" fill="#ffffff"/>
</svg>`

// writeSVG writes an SVG to a temp file and returns its path.
func writeSVG(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "img.svg")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// An .svg on its own line becomes a vector figure (Image with SVG set), not
// alt text, and its dimensions carry the SVG's aspect ratio.
func TestSVGFigureIsVector(t *testing.T) {
	th := theme(t, nil)
	p := writeSVG(t, sampleSVG)
	d := parse(t, "![a figure]("+p+")\n", th)

	var figs int
	for _, it := range d.Items {
		im, ok := it.B.(*Image)
		if !ok {
			continue
		}
		figs++
		if im.SVG == nil {
			t.Error("figure is not a vector SVG (SVG field nil)")
		}
		if im.Img != nil {
			t.Error("SVG figure should not also carry a raster image")
		}
		// 200x100 viewBox → 2:1 aspect.
		iw, ih := im.dims(1000)
		if !approx(iw/ih, 2.0) {
			t.Errorf("aspect ratio = %.3f, want 2.0 (%.2fx%.2f)", iw/ih, iw, ih)
		}
	}
	if figs != 1 {
		t.Fatalf("got %d figures, want 1", figs)
	}
}

// An inline .svg becomes an SVG run (not a figure, not alt text).
func TestSVGInlineIsRun(t *testing.T) {
	th := theme(t, nil)
	p := writeSVG(t, sampleSVG)
	d := parse(t, "Before ![alt]("+p+") after.\n", th)

	var paras, svgRuns, figs int
	for _, it := range d.Items {
		switch b := it.B.(type) {
		case *Para:
			paras++
			for _, r := range b.Runs {
				if r.SVG != nil {
					svgRuns++
				}
			}
		case *Image:
			figs++
		}
	}
	if figs != 0 {
		t.Errorf("inline SVG became a figure (%d); should stay inline", figs)
	}
	if paras != 1 {
		t.Fatalf("got %d paragraphs, want 1", paras)
	}
	if svgRuns != 1 {
		t.Errorf("got %d inline SVG runs, want 1", svgRuns)
	}
}

// A paragraph carrying an inline SVG must not be line-split (the SVG object
// would be dropped by the text-only split machinery); it moves whole.
func TestSVGParaDoesNotSplit(t *testing.T) {
	th := theme(t, nil)
	p := writeSVG(t, sampleSVG)
	d := parse(t, "Icon ![alt]("+p+") "+strings.Repeat("word ", 100)+"\n", th)

	var para *Para
	for _, it := range d.Items {
		if pp, ok := it.B.(*Para); ok {
			para = pp
			break
		}
	}
	if para == nil {
		t.Fatal("no paragraph built")
	}
	if !para.hasImage() {
		t.Fatal("paragraph with an inline SVG should report hasImage")
	}
	w := th.ContentW()
	head, tail, ok := para.Split(w, 20)
	if !ok {
		t.Fatal("Split returned ok=false")
	}
	if head != nil {
		t.Error("SVG paragraph was split; head should be nil (move whole)")
	}
	if tail == nil {
		t.Error("SVG paragraph Split lost the block; tail should be the whole para")
	}
}

// A malformed SVG falls back to alt text without panicking.
func TestSVGMalformedFallsBackToAlt(t *testing.T) {
	th := theme(t, nil)
	p := writeSVG(t, "this is not <<< svg")
	d := parse(t, "![the alt text]("+p+")\n", th)

	var text string
	for _, it := range d.Items {
		switch b := it.B.(type) {
		case *Para:
			text += plainText(b.Runs)
		case *Image:
			t.Error("malformed SVG produced a figure; want alt-text paragraph")
		}
	}
	if !strings.Contains(text, "the alt text") {
		t.Errorf("alt text not rendered on malformed SVG; got %q", text)
	}
}

// A missing .svg file falls back to alt text.
func TestSVGMissingFallsBackToAlt(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "![missing alt](/no/such/file.svg)\n", th)
	var text string
	for _, it := range d.Items {
		if p, ok := it.B.(*Para); ok {
			text += plainText(p.Runs)
		}
	}
	if !strings.Contains(text, "missing alt") {
		t.Errorf("alt text not rendered on missing SVG; got %q", text)
	}
}

// isSVG detects by extension, case-insensitively.
func TestIsSVG(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"a.svg", true}, {"a.SVG", true}, {"path/to/b.Svg", true},
		{"a.png", false}, {"a.svgz", false}, {"noext", false},
	} {
		if got := isSVG(tc.in); got != tc.want {
			t.Errorf("isSVG(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
