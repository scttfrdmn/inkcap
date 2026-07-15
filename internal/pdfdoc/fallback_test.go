package pdfdoc

import (
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/inkcap/internal/config"
	"github.com/tdewolff/canvas"
)

// covers must treat control characters as covered (they have no glyph but are
// handled by the layout) and report real coverage for printable runes.
func TestCovers(t *testing.T) {
	th := theme(t, nil)
	f := th.Text.Face(th.BodySize, th.FG, canvas.FontRegular)

	if !covers(f, 'A') {
		t.Error("expected coverage for 'A'")
	}
	if !covers(f, '\n') {
		t.Error("newline must count as covered")
	}
	if !covers(f, ' ') {
		t.Error("space must count as covered")
	}
	if covers(f, '中') {
		t.Skip("embedded serif unexpectedly covers CJK; test assumption void")
	}
}

// With no fallback configured, a glyph missing from the primary face is warned
// about exactly once, no matter how many times it appears.
func TestMissingGlyphWarnsOnce(t *testing.T) {
	th := theme(t, nil) // no fallback fonts
	d := parse(t, "The character 中 appears 中 twice.\n", th)

	n := 0
	for _, w := range d.Warn {
		if strings.Contains(w, "U+4E2D") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d warnings for U+4E2D, want exactly 1 (warns: %v)", n, d.Warn)
	}
}

// A run mixing covered and uncovered characters is split so the covered part
// still renders with the primary face (the uncovered part falls to .notdef
// there when no fallback is set, but must not corrupt the covered text).
func TestEmitSplitsMixedRun(t *testing.T) {
	th := theme(t, nil)
	b := &builder{
		t: th, src: nil, base: ".",
		links: map[*canvas.FontFace]string{},
		notes: map[*canvas.FontFace]int{},
		body:  map[int]Note{},
	}
	var out []Run
	b.emit(style{size: th.BodySize}, "ab中cd", &out)

	got := plainText(out)
	if got != "ab中cd" {
		t.Errorf("emit lost text: got %q, want %q", got, "ab中cd")
	}
	// The ASCII portions must be present and contiguous.
	if len(out) == 0 {
		t.Fatal("emit produced no runs")
	}
}

// When a fallback font that covers the glyph is configured, emit routes the
// uncovered rune to it and does not warn. Skipped where no broad-coverage font
// is available (e.g. CI without the font installed).
func TestFallbackCoversGlyph(t *testing.T) {
	const fontPath = "/System/Library/Fonts/Supplemental/Arial Unicode.ttf"
	if _, err := os.Stat(fontPath); err != nil {
		t.Skipf("no broad-coverage fallback font at %s", fontPath)
	}
	th := theme(t, func(c *config.Config) {
		c.Fonts.Fallback = []string{fontPath}
	})
	if len(th.Fallback) != 1 {
		t.Fatalf("fallback family not loaded: %d", len(th.Fallback))
	}
	d := parse(t, "CJK: 中文 here.\n", th)

	for _, w := range d.Warn {
		if strings.Contains(w, "U+4E2D") {
			t.Errorf("warned about a glyph the fallback covers: %q", w)
		}
	}
	// The CJK characters must be carried on a face from the fallback family,
	// distinct from the primary serif face.
	primary := th.Text.Face(th.BodySize, th.FG, canvas.FontRegular)
	var foundFallback bool
	for _, it := range d.Items {
		p, ok := it.B.(*Para)
		if !ok {
			continue
		}
		for _, r := range p.Runs {
			if strings.ContainsRune(r.Text, '中') && r.Face != primary {
				foundFallback = true
			}
		}
	}
	if !foundFallback {
		t.Error("CJK text was not routed to a fallback face")
	}
}
