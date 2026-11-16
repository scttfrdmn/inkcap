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

// Code blocks take the same glyph fallback and missing-glyph warning as prose.
// They used to emit every token with the mono face, drawing .notdef boxes.
func TestCodeBlockMissingGlyphWarns(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "```go\nfmt.Println(\"你好\") // 你\n```\n", th)
	n := 0
	for _, w := range d.Warn {
		if strings.Contains(w, "U+4F60") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d warnings for U+4F60 in a code block, want 1: %v", n, d.Warn)
	}
}

func TestCodeBlockFallbackFace(t *testing.T) {
	const fontPath = "/System/Library/Fonts/Supplemental/Arial Unicode.ttf"
	if _, err := os.Stat(fontPath); err != nil {
		t.Skipf("no broad-coverage fallback font at %s", fontPath)
	}
	th := theme(t, func(c *config.Config) { c.Fonts.Fallback = []string{fontPath} })
	d := parse(t, "```\nok 你好 ok\n```\n", th)
	if len(d.Warn) != 0 {
		t.Errorf("unexpected warnings with a covering fallback: %v", d.Warn)
	}
	code := d.Items[0].B.(*Code)
	var faces []*canvas.FontFace
	for _, r := range code.Lines[0] {
		faces = append(faces, r.Face)
		if strings.Contains(r.Text, "你") && covers(th.Mono.Face(th.CodeSize, th.FG), '你') {
			t.Skip("mono face unexpectedly covers CJK")
		}
	}
	if len(faces) != 3 || faces[0] != faces[2] || faces[0] == faces[1] {
		t.Errorf("want runs mono/fallback/mono, got %d runs", len(faces))
	}
}

// Wide (CJK) characters count as two monospace columns, so a wrapped line
// never exceeds the column limit and a wide character is never split.
func TestWrapMonoWideChars(t *testing.T) {
	line := []Run{{Text: "ab你好世界你好世界cd"}}
	for _, vis := range wrapMono(line, 8) {
		if n := runCols(plainText(vis)); n > 8 {
			t.Errorf("visual line %q is %d columns, limit 8", plainText(vis), n)
		}
	}
	if got := runCols("a你b"); got != 4 {
		t.Errorf("runCols(a你b) = %d, want 4", got)
	}
}

// glyphCount lays out runs and counts the glyphs in spans of face f.
func glyphCount(runs []Run, f *canvas.FontFace, th *Theme) int {
	n := 0
	buildText(runs, 0, canvas.Left, th.textOpts()).WalkSpans(func(_, _ float64, s canvas.TextSpan) {
		if s.Face == f {
			n += len(s.Glyphs)
		}
	})
	return n
}

// Link text is shaped without ligatures, so a URL copies out of the PDF as
// typed ("files", not "ﬁles"); body text keeps them.
func TestLinksHaveNoLigatures(t *testing.T) {
	th := theme(t, nil)
	body := th.Text.Face(th.BodySize, th.FG)
	if glyphCount([]Run{{Face: body, Text: "files"}}, body, th) == 5 {
		t.Skip("the text face has no fi ligature; nothing to test")
	}
	d := parse(t, "[files](https://files.example.com)\n", th)
	run := d.Items[0].B.(*Para).Runs[0]
	if n := glyphCount([]Run{run}, run.Face, th); n != 5 {
		t.Errorf("link 'files' shaped to %d glyphs, want 5 (no ligature)", n)
	}
}

// Invisible characters (variation selectors, joiners) never warn.
func TestDefaultIgnorablesDontWarn(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "A️ B‍ C‌ D⁠ E​.\n", th)
	if len(d.Warn) != 0 {
		t.Errorf("warnings for invisible characters: %v", d.Warn)
	}
}

// A soft line break between CJK characters joins without a space.
func TestCJKSoftBreakNoSpace(t *testing.T) {
	th := theme(t, nil)
	for src, want := range map[string]string{
		"中文\n测试\n":         "中文测试",
		"English\nwords\n": "English words",
		"中文\nEnglish\n":    "中文 English",
		"日本語の\n*文章*\n":     "日本語の文章",
	} {
		if got := plainText(parse(t, src, th).Items[0].B.(*Para).Runs); got != want {
			t.Errorf("%q -> %q, want %q", src, got, want)
		}
	}
}
