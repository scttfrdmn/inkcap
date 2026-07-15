package pdfdoc

import (
	"image"
	"strconv"
	"strings"

	"github.com/tdewolff/canvas"
)

// ---------------------------------------------------------------------------
// inline text plumbing
// ---------------------------------------------------------------------------

func buildText(runs []Run, w float64, halign canvas.TextAlign, opts *canvas.TextOptions) *canvas.Text {
	if len(runs) == 0 {
		return nil
	}
	rt := canvas.NewRichText(runs[0].Face)
	for _, r := range runs {
		if r.SVG != nil {
			rt.SetFace(r.Face)
			rt.WriteCanvas(r.SVG, canvas.FontMiddle)
			continue
		}
		if r.Img != nil {
			rt.SetFace(r.Face)
			rt.WriteImage(r.Img, r.ImgRes, canvas.FontMiddle)
			continue
		}
		rt.WriteFace(r.Face, r.Text)
	}
	return rt.ToText(w, 0, halign, canvas.Top, opts)
}

// extractLines recovers the broken lines from a laid-out Text as run lists.
// This is the hinge that makes paragraph splitting possible: canvas does the
// Knuth-Plass break, we read the result back out and can re-flow any suffix.
func extractLines(t *canvas.Text) [][]Run {
	var out [][]Run
	if t == nil {
		return out
	}
	t.WalkLines(func(_ float64, spans []canvas.TextSpan) {
		var rs []Run
		for _, s := range spans {
			if !s.IsText() || s.Text == "" {
				continue
			}
			rs = append(rs, Run{Face: s.Face, Text: s.Text})
		}
		out = append(out, rs)
	})
	return out
}

// joinLines re-flattens a slice of lines back into a run list, restoring the
// inter-line space that the line breaker consumed and merging adjacent runs
// that share a face.
func joinLines(lines [][]Run) []Run {
	var out []Run
	for i, ln := range lines {
		if i > 0 && len(out) > 0 && len(ln) > 0 {
			out[len(out)-1].Text += " "
		}
		for _, r := range ln {
			if n := len(out); n > 0 && out[n-1].Face == r.Face {
				out[n-1].Text += r.Text
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

// fitLines finds the largest k such that lines[:k] is no taller than avail.
// Binary search: each probe is one relayout, so this is O(log n) layouts.
func fitLines(lines [][]Run, w, avail float64, halign canvas.TextAlign, opts *canvas.TextOptions) int {
	lo, hi := 0, len(lines)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		t := buildText(joinLines(lines[:mid]), w, halign, opts)
		if t != nil && t.Height <= avail && t.Lines() <= mid {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// drawRuns paints a laid-out text and registers link rects for any span whose
// face is a known link face.
func drawRuns(r *Rctx, t *canvas.Text, x, y float64) {
	if t == nil {
		return
	}
	r.C.DrawText(x, y, t)
	if len(r.Links) == 0 {
		return
	}
	t.WalkSpans(func(sx, sy float64, span canvas.TextSpan) {
		uri, ok := r.Links[span.Face]
		if !ok {
			return
		}
		m := span.Face.Metrics()
		r.Link(uri, x+sx, y+sy-m.Ascent, span.Width, m.Ascent+m.Descent)
	})
}

// ---------------------------------------------------------------------------
// Para
// ---------------------------------------------------------------------------

type Para struct {
	base
	Runs   []Run
	Align  canvas.TextAlign
	Opts   *canvas.TextOptions
	Orphan int // min lines to leave behind
	Widow  int // min lines to carry forward
}

func (p *Para) Measure(w float64) float64 {
	t := buildText(p.Runs, w, p.Align, p.Opts)
	if t == nil {
		return 0
	}
	return t.Height
}

func (p *Para) Split(w, avail float64) (Block, Block, bool) {
	t := buildText(p.Runs, w, p.Align, p.Opts)
	if t == nil {
		return nil, nil, true
	}
	if t.Height <= avail {
		return p, nil, true
	}
	// A paragraph carrying an inline image can't be line-split: extractLines /
	// joinLines rebuild runs from text spans only and would drop the image. Such
	// paragraphs are short by nature, so move the whole thing to the next page.
	if p.hasImage() {
		return nil, p, true
	}
	lines := extractLines(t)
	k := fitLines(lines, w, avail, p.Align, p.Opts)

	// Orphan/widow control: don't leave fewer than Orphan lines behind, and
	// don't carry fewer than Widow lines forward. If either rule is violated
	// and can't be satisfied, push the whole paragraph to the next page.
	if k < p.Orphan || len(lines)-k < p.Widow {
		if len(lines) >= p.Orphan+p.Widow {
			k = len(lines) - p.Widow
			if k < p.Orphan {
				return nil, p, true
			}
			// re-verify the adjusted head still fits
			ht := buildText(joinLines(lines[:k]), w, p.Align, p.Opts)
			if ht == nil || ht.Height > avail {
				return nil, p, true
			}
		} else {
			return nil, p, true
		}
	}
	if k == 0 {
		return nil, p, true
	}

	head := *p
	head.Runs = joinLines(lines[:k])
	head.after = 0
	tail := *p
	tail.Runs = joinLines(lines[k:])
	tail.before = 0
	return &head, &tail, true
}

func (p *Para) hasImage() bool {
	for _, r := range p.Runs {
		if r.Img != nil || r.SVG != nil {
			return true
		}
	}
	return false
}

func (p *Para) Refs(reg map[*canvas.FontFace]int) []int { return refsIn(p.Runs, reg) }

func (p *Para) Draw(r *Rctx, x, y, w float64) {
	drawRuns(r, buildText(p.Runs, w, p.Align, p.Opts), x, y)
}

// ---------------------------------------------------------------------------
// Heading
// ---------------------------------------------------------------------------

type Heading struct {
	base
	Level int
	Runs  []Run
	Plain string
	Opts  *canvas.TextOptions
	Rule  bool // hairline under the heading (H1/H2)
	T     *Theme

	fromTOC bool // synthesised for the contents page; never listed in itself
}

func (h *Heading) text(w float64) *canvas.Text {
	return buildText(h.Runs, w, canvas.Left, h.Opts)
}

func (h *Heading) Measure(w float64) float64 {
	t := h.text(w)
	if t == nil {
		return 0
	}
	ht := t.Height
	if h.Rule {
		ht += pt(5)
	}
	return ht
}

// Headings never split; if one doesn't fit it moves down whole.
func (h *Heading) Split(w, avail float64) (Block, Block, bool) { return atomicSplit(h, w, avail) }

func (h *Heading) Refs(reg map[*canvas.FontFace]int) []int { return refsIn(h.Runs, reg) }

func (h *Heading) Draw(r *Rctx, x, y, w float64) {
	t := h.text(w)
	drawRuns(r, t, x, y)
	r.Outline(h.Plain, h.Level-1, y)
	if h.Rule && t != nil {
		ry := y + t.Height + pt(3)
		r.C.SetStrokeColor(h.T.Rule)
		r.C.SetStrokeWidth(0.25)
		r.C.MoveTo(x, ry)
		r.C.LineTo(x+w, ry)
		r.C.Stroke()
	}
}

// ---------------------------------------------------------------------------
// Code
// ---------------------------------------------------------------------------

// Code is a fenced block. Because the font is monospaced we can wrap by
// character count exactly, which preserves indentation instead of reflowing it
// away — a hanging indent marks the continuation.
type Code struct {
	base
	Lines [][]Run // logical source lines, already syntax-highlighted
	T     *Theme
	adv   float64 // advance width of one mono character
	lineH float64

	First int // 1-based number of Lines[0]; survives a split
	nums  *canvas.FontFace
}

// gutter is the width reserved for line numbers (zero when disabled).
func (c *Code) gutter() float64 {
	if !c.T.LineNumbers {
		return 0
	}
	digits := len(strconv.Itoa(c.First + len(c.Lines)))
	if digits < 2 {
		digits = 2
	}
	return float64(digits)*c.adv + c.T.CodePad
}

// visual returns the wrapped lines, and for each one the source line number it
// belongs to (0 for a wrapped continuation, which gets no number).
func (c *Code) visual(w float64) ([][]Run, []int) {
	inner := w - 2*c.T.CodePad - c.gutter()
	maxCols := int(inner / c.adv)
	if maxCols < 8 {
		maxCols = 8
	}
	var out [][]Run
	var nums []int
	for i, ln := range c.Lines {
		frags := wrapMono(ln, maxCols)
		for j := range frags {
			out = append(out, frags[j])
			if j == 0 {
				nums = append(nums, c.First+i)
			} else {
				nums = append(nums, 0)
			}
		}
	}
	return out, nums
}

// wrapMono breaks one logical line into visual lines at maxCols characters,
// splitting runs as needed and indenting continuations by two columns.
func wrapMono(line []Run, maxCols int) [][]Run {
	total := 0
	for _, r := range line {
		total += len([]rune(r.Text))
	}
	if total <= maxCols {
		return [][]Run{line}
	}
	var out [][]Run
	var cur []Run
	col := 0
	limit := maxCols
	cont := false
	flush := func() {
		if cont {
			cur = append([]Run{{Face: line[0].Face, Text: "  "}}, cur...)
		}
		out = append(out, cur)
		cur = nil
		col = 0
		cont = true
		limit = maxCols - 2
	}
	for _, r := range line {
		rs := []rune(r.Text)
		for len(rs) > 0 {
			room := limit - col
			if room <= 0 {
				flush()
				continue
			}
			n := min(room, len(rs))
			cur = append(cur, Run{Face: r.Face, Text: string(rs[:n])})
			col += n
			rs = rs[n:]
			if col >= limit && len(rs) > 0 {
				flush()
			}
		}
	}
	if len(cur) > 0 {
		if cont {
			cur = append([]Run{{Face: line[0].Face, Text: "  "}}, cur...)
		}
		out = append(out, cur)
	}
	return out
}

func (c *Code) Measure(w float64) float64 {
	vis, _ := c.visual(w)
	return float64(len(vis))*c.lineH + 2*c.T.CodePad
}

// Split breaks at a source-line boundary (never mid-wrap), so a continuation
// never starts halfway through a wrapped line and the numbering stays honest.
func (c *Code) Split(w, avail float64) (Block, Block, bool) {
	if c.Measure(w) <= avail {
		return c, nil, true
	}
	inner := w - 2*c.T.CodePad - c.gutter()
	maxCols := int(inner / c.adv)
	if maxCols < 8 {
		maxCols = 8
	}
	budget := int((avail - 2*c.T.CodePad) / c.lineH)

	used, k := 0, 0
	for _, ln := range c.Lines {
		n := len(wrapMono(ln, maxCols))
		if used+n > budget {
			break
		}
		used += n
		k++
	}
	// Don't break for a sliver on either side.
	if used < 3 || k == 0 || len(c.Lines)-k < 2 {
		return nil, c, true
	}
	head := *c
	head.Lines = c.Lines[:k]
	head.after = 0
	tail := *c
	tail.Lines = c.Lines[k:]
	tail.First = c.First + k
	tail.before = 0
	return &head, &tail, true
}

func (c *Code) Draw(r *Rctx, x, y, w float64) {
	vis, nums := c.visual(w)
	h := float64(len(vis))*c.lineH + 2*c.T.CodePad
	g := c.gutter()

	r.C.SetFillColor(c.T.CodeBG)
	r.C.SetStrokeColor(c.T.Rule)
	r.C.SetStrokeWidth(0.2)
	r.C.DrawPath(x, y, canvas.RoundedRectangle(w, h, 1.0))

	ty := y + c.T.CodePad
	for i, ln := range vis {
		if g > 0 && nums[i] > 0 {
			nt := buildText([]Run{{Face: c.nums, Text: strconv.Itoa(nums[i])}},
				g-c.T.CodePad, canvas.Right, c.T.monoOpts())
			r.C.DrawText(x+c.T.CodePad, ty, nt)
		}
		if len(ln) > 0 {
			drawRuns(r, buildText(ln, 0, canvas.Left, c.T.monoOpts()), x+c.T.CodePad+g, ty)
		}
		ty += c.lineH
	}
}

// ---------------------------------------------------------------------------
// Rule, Image, Spacer
// ---------------------------------------------------------------------------

type Rule struct {
	base
	T *Theme
}

func (h *Rule) Measure(w float64) float64 { return pt(1) }
func (h *Rule) Split(w, avail float64) (Block, Block, bool) {
	return atomicSplit(h, w, avail)
}
func (h *Rule) Draw(r *Rctx, x, y, w float64) {
	r.C.SetStrokeColor(h.T.Rule)
	r.C.SetStrokeWidth(0.3)
	inset := w * 0.35
	r.C.MoveTo(x+inset, y)
	r.C.LineTo(x+w-inset, y)
	r.C.Stroke()
}

type Image struct {
	base
	Img     image.Image    // raster figure
	SVG     *canvas.Canvas // vector figure (mutually exclusive with Img)
	Caption []Run
	T       *Theme
}

func (im *Image) dims(w float64) (iw, ih float64) {
	if im.SVG != nil {
		// SVG carries its own physical size in mm; no DPI applies.
		iw, ih = im.SVG.Size()
	} else {
		b := im.Img.Bounds()
		dpi := im.T.ImageDPI
		if dpi <= 0 {
			dpi = 150
		}
		// Nominal DPI, scaled down to fit the measure.
		iw = float64(b.Dx()) * 25.4 / dpi
		ih = float64(b.Dy()) * 25.4 / dpi
	}
	if iw > w {
		ih *= w / iw
		iw = w
	}
	return
}

func (im *Image) Measure(w float64) float64 {
	_, ih := im.dims(w)
	if len(im.Caption) > 0 {
		if t := buildText(im.Caption, w, canvas.Center, im.T.textOpts()); t != nil {
			ih += pt(4) + t.Height
		}
	}
	return ih
}

func (im *Image) Split(w, avail float64) (Block, Block, bool) { return atomicSplit(im, w, avail) }

func (im *Image) Draw(r *Rctx, x, y, w float64) {
	iw, ih := im.dims(w)
	ix := x + (w-iw)/2
	if im.SVG != nil {
		im.drawSVG(r, ix, y, iw, ih)
	} else {
		res := canvas.Resolution(float64(im.Img.Bounds().Dx()) / iw)
		r.C.DrawImage(ix, y, im.Img, res)
	}
	if len(im.Caption) > 0 {
		drawRuns(r, buildText(im.Caption, w, canvas.Center, im.T.textOpts()), x, y+ih+pt(4))
	}
}

// drawSVG renders the vector canvas at (x, y) with size (iw, ih), where (x, y)
// is the top-left corner in the page's y-down coordinate system. The SVG canvas
// is authored y-up with its origin bottom-left, so we flip and translate into
// PDF space (also y-up, origin bottom-left) via the raw renderer.
func (im *Image) drawSVG(r *Rctx, x, y, iw, ih float64) {
	sw, sh := im.SVG.Size()
	if sw <= 0 || sh <= 0 {
		return
	}
	sx, sy := iw/sw, ih/sh
	// Page-bottom-left origin: the figure's bottom edge is at (PageH - (y+ih)).
	yBottom := im.T.PageH - (y + ih)
	view := canvas.Identity.Translate(x, yBottom).Scale(sx, sy)
	im.SVG.RenderViewTo(r.C, view)
}

func plainText(runs []Run) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// TocLine
// ---------------------------------------------------------------------------

// TocLine is one entry in the table of contents: the heading text, a dotted
// leader, and a page number flush to the right margin.
type TocLine struct {
	base
	Level int
	Runs  []Run
	Page  int
	T     *Theme
}

const tocNumW = 10.0 // width reserved for the page number, mm

func (l *TocLine) face() *canvas.FontFace {
	if l.Level == 1 {
		return l.T.Text.Face(l.T.BodySize, l.T.FG, canvas.FontBold)
	}
	return l.T.Text.Face(l.T.BodySize, l.T.FG)
}

// runs restyles the heading's runs at body size, so a bold word inside an H2
// doesn't come back at 15pt.
func (l *TocLine) runs() []Run {
	f := l.face()
	return []Run{{Face: f, Text: plainText(l.Runs)}}
}

func (l *TocLine) text(w float64) *canvas.Text {
	return buildText(l.runs(), w-tocNumW-3, canvas.Left, l.T.textOpts())
}

func (l *TocLine) Measure(w float64) float64 {
	t := l.text(w)
	if t == nil {
		return 0
	}
	return t.Height
}

func (l *TocLine) Split(w, avail float64) (Block, Block, bool) { return atomicSplit(l, w, avail) }

func (l *TocLine) Draw(r *Rctx, x, y, w float64) {
	t := l.text(w)
	if t == nil {
		return
	}
	r.C.DrawText(x, y, t)

	num := strconv.Itoa(l.Page)
	f := l.face()
	nt := buildText([]Run{{Face: f, Text: num}}, tocNumW, canvas.Right, l.T.textOpts())
	r.C.DrawText(x+w-tocNumW, y, nt)

	// Leaders only make sense when the entry is a single line.
	if t.Lines() != 1 {
		return
	}
	from := x + f.TextWidth(plainText(l.Runs)) + 1.5
	to := x + w - tocNumW - f.TextWidth(num) - 1.5
	if to-from < 4 {
		return
	}
	m := f.Metrics()
	ly := y + m.Ascent - m.XHeight*0.28
	r.C.SetStrokeColor(l.T.Muted)
	r.C.SetStrokeWidth(0.22)
	r.C.SetDashes(0, 0.35, 1.5)
	r.C.MoveTo(from, ly)
	r.C.LineTo(to, ly)
	r.C.Stroke()
	r.C.SetDashes(0)
}
