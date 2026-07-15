package pdfdoc

import (
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
