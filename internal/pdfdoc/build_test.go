package pdfdoc

import (
	"testing"

	"github.com/scttfrdmn/inkcap/internal/config"
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
