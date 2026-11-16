package pdfdoc

import (
	"strings"
	"testing"
)

func TestBreakable(t *testing.T) {
	z := string(zwsp)
	for in, want := range map[string]string{
		"short words stay":                  "short words stay",
		"https://example.com/a/b?x=1&y=2":   "https://" + z + "example.com/" + z + "a/" + z + "b?" + z + "x=1&" + z + "y=2",
		"monoFallbackForCode":               "mono" + z + "Fallback" + z + "For" + z + "Code",
		"abcdefghijklmnopqrstuvwxyz":        "abcdefghijklmnopqrst" + z + "uvwxyz",
		"a-very-long-hyphenated-identifier": "a-very-long-hyphenated-identifier",
	} {
		if got := breakable(in, 12); got != want {
			t.Errorf("breakable(%q)\n got %q\nwant %q", in, got, want)
		}
		if got := strings.ReplaceAll(breakable(in, 12), z, ""); got != in {
			t.Errorf("breakable(%q) changed the visible text to %q", in, got)
		}
	}
	if got := breakable("https://example.com/path", 40); strings.ContainsRune(got, zwsp) {
		t.Errorf("token shorter than minLen got break points: %q", got)
	}
}

// Long URLs and identifiers wrap within the measure, in body text and in
// table cells; they used to run past the right margin and overlap columns.
func TestLongTokensStayInMeasure(t *testing.T) {
	th := theme(t, nil)
	src := "See https://github.com/scttfrdmn/inkcap/blob/main/internal/pdfdoc/build.go?plain=1#L120-L180 and " +
		"`internal/pdfdoc.(*builder).monoFallbackForCodeBlocksWithAVeryLongName` here.\n\n" +
		"| one | two | three |\n|---|---|---|\n" +
		"| `internal/pdfdoc/blocks.go/extractLinesFromLaidOutText` | https://example.com/a/really/long/path/segment/that/keeps/going | `anotherVeryLongIdentifierNameThatHasNoSpaces` |\n"
	d := parse(t, src, th)
	w := th.ContentW()
	for _, it := range d.Items {
		switch v := it.B.(type) {
		case *Para:
			if txt := buildText(v.Runs, w, v.Align, v.Opts); txt.OverflowsX {
				t.Errorf("paragraph overflows the %.0fmm measure (%.0fmm)", w, txt.Width)
			}
		case *Table:
			widths := v.solve(w)
			for _, row := range append([][]Cell{v.Head}, v.Rows...) {
				for i, c := range row {
					cw := widths[i] - 2*v.PadX
					if txt := buildText(c.Runs, cw, c.Align, th.textOpts()); txt != nil && txt.OverflowsX {
						t.Errorf("cell %d (%q) overflows its %.1fmm column", i, plainText(c.Runs), cw)
					}
				}
			}
		}
	}
}

// Splitting a paragraph between any two lines must lose and insert nothing:
// the original text is exactly head + separator + tail, where the separator is
// what the line broke at (a space, a zero-width space, or a soft hyphen shown
// as "-" at the end of the head). Rebuilding from laid-out spans used to
// insert a space at every zero-width-space break.
func TestSplitPreservesText(t *testing.T) {
	th := theme(t, nil)
	words := "Words and https://example.com/a/really/long/path/segment/that/keeps/going/on/and/on " +
		"plus `veryLongIdentifierNameForTesting` and some soft­hyph­en­at­ed text "
	src := strings.Repeat(words, 6)
	p := parse(t, src+"\n", th).Items[0].B.(*Para)
	w := th.ContentW() * 0.5
	full := buildText(p.Runs, w, p.Align, p.Opts)
	lines := extractLines(full)
	cuts := lineCuts(p.Runs, lines)
	if cuts == nil {
		t.Fatal("lineCuts could not align the laid-out lines with the text")
	}
	orig := plainText(p.Runs)
	for k := 1; k < len(lines); k++ {
		head, tail := splitAt(p.Runs, lines, cuts, k)
		h, tl := plainText(head), plainText(tail)
		ok := false
		for _, sep := range []string{" ", string(zwsp), ""} {
			if orig == h+sep+tl {
				ok = true
			}
		}
		// a soft-hyphen break: the head ends in "-" standing for the U+00AD
		if strings.HasSuffix(h, "-") && orig == strings.TrimSuffix(h, "-")+string(shy)+tl {
			ok = true
		}
		if !ok {
			t.Fatalf("split after line %d changed the text:\nhead %q\ntail %q", k, h, tl)
		}
	}
}
