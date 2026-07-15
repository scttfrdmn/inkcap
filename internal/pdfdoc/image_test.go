package pdfdoc

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePNG writes a solid w×h PNG to a temp file and returns its path.
func writePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{0x34, 0x98, 0xdb, 0xff})
		}
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "dot.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

// An inline image (image inside a paragraph with other text) becomes an image
// run within the paragraph, not a standalone figure, and not alt text.
func TestInlineImageRendersInline(t *testing.T) {
	th := theme(t, nil)
	p := writePNG(t, 20, 20)
	src := "Text before ![alt](" + p + ") text after.\n"
	d := parse(t, src, th)

	var paras, imgRuns, imgBlocks int
	for _, it := range d.Items {
		switch b := it.B.(type) {
		case *Para:
			paras++
			for _, r := range b.Runs {
				if r.Img != nil {
					imgRuns++
				}
			}
		case *Image:
			imgBlocks++
		}
	}
	if paras != 1 {
		t.Fatalf("got %d paragraphs, want 1", paras)
	}
	if imgBlocks != 0 {
		t.Errorf("inline image became a figure block (%d); should stay inline", imgBlocks)
	}
	if imgRuns != 1 {
		t.Errorf("got %d inline image runs, want 1", imgRuns)
	}
}

// A missing inline image falls back to its alt text.
func TestInlineImageMissingFallsBackToAlt(t *testing.T) {
	th := theme(t, nil)
	src := "Before ![the alt text](/no/such/file.png) after.\n"
	d := parse(t, src, th)

	var text string
	for _, it := range d.Items {
		if p, ok := it.B.(*Para); ok {
			text += plainText(p.Runs)
			for _, r := range p.Runs {
				if r.Img != nil {
					t.Error("missing image produced an image run; want alt text")
				}
			}
		}
	}
	if !strings.Contains(text, "the alt text") {
		t.Errorf("alt text not rendered; got %q", text)
	}
}

// A paragraph carrying an inline image must not be line-split (which would drop
// the image); it moves whole instead.
func TestInlineImageParaDoesNotSplit(t *testing.T) {
	th := theme(t, nil)
	p := writePNG(t, 20, 20)
	runs := []Run{
		{Face: th.Text.Face(th.BodySize, th.FG)},
	}
	// Build a Para with an image run plus enough text to exceed a tiny avail.
	para := &Para{
		Runs: append(runs,
			Run{Face: th.Text.Face(th.BodySize, th.FG), Text: strings.Repeat("word ", 100)}),
		Align:  th.bodyAlign(),
		Opts:   th.textOpts(),
		Orphan: th.Orphans, Widow: th.Widows,
	}
	// mark the first run as an image
	img := loadTestImage(t, p)
	para.Runs[0].Img = img

	w := th.ContentW()
	head, tail, ok := para.Split(w, 20 /* mm, forces a split attempt */)
	if !ok {
		t.Fatal("Split returned ok=false")
	}
	if head != nil {
		t.Error("image paragraph was split; head should be nil (move whole)")
	}
	if tail == nil {
		t.Error("image paragraph Split lost the block; tail should be the whole para")
	}
}

func loadTestImage(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}
