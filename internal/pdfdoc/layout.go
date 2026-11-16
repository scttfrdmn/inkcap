package pdfdoc

import (
	"fmt"
	"image/color"
	"io"
	"slices"
	"strconv"

	"github.com/scttfrdmn/canvas"
	"github.com/scttfrdmn/canvas/renderers/pdf"
)

// Layout is split into two passes for a reason: a table of contents needs page
// numbers that only exist after pagination, and footnotes need to shrink the
// available height of the page they land on. Neither is expressible in a single
// draw-as-you-go loop.
//
//	items ──paginate──▶ []Page ──draw──▶ PDF
//
// paginate is pure. The TOC is produced by running it twice: once to learn the
// page numbers, once with the TOC spliced in. The TOC's own height doesn't
// depend on those numbers, so the second pass is a fixed point.

// Placed is a block with its resolved position on a page.
type Placed struct {
	It         Item
	X, Y, W, H float64
}

// Page is one output page: its blocks, plus the footnotes that landed on it.
type Page struct {
	Items []Placed
	Notes []int
}

// ---------------------------------------------------------------------------
// footnotes
// ---------------------------------------------------------------------------

const noteSep = 22.0 // length of the rule above the footnote area, mm

// noteRuns renders footnote n as a superscript number followed by its text.
func (d *Doc) noteRuns(n int) []Run {
	t := d.T
	note, ok := d.Body[n]
	if !ok {
		return nil
	}
	sup := t.Text.Face(t.SmallSize, t.Muted, canvas.FontSuperscript)
	return append([]Run{{Face: sup, Text: strconv.Itoa(n) + " "}}, note.Runs...)
}

func (d *Doc) noteHeight(n int) float64 {
	t := buildText(d.noteRuns(n), d.T.ContentW(), canvas.Left, d.T.textOpts())
	if t == nil {
		return 0
	}
	return t.Height
}

// notesHeight is the total vertical space a set of footnotes will consume,
// including the separator rule and its gaps.
func (d *Doc) notesHeight(ids []int) float64 {
	if d.T.Footnotes != "page" || len(ids) == 0 {
		return 0
	}
	h := d.T.BlockSpace + 2.0 // gap above the rule, gap below it
	for _, n := range ids {
		h += d.noteHeight(n) + 0.8
	}
	return h
}

func mergeNotes(cur, add []int) []int {
	out := slices.Clone(cur)
	for _, n := range add {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// pagination
// ---------------------------------------------------------------------------

// noteCapFrac caps the footnote area at this fraction of the text height.
// Notes that would push past it continue on the next page, as in TeX, rather
// than being drawn up over the body (#70). Below the cap, every note still
// lands on the page that references it.
const noteCapFrac = 0.6

// fitNotes splits ids, in order, into the notes that fit within capH and the
// rest. A single note taller than the cap is kept anyway (it can't be split),
// so a page always makes progress.
func (d *Doc) fitNotes(ids []int, capH float64) (keep, rest []int) {
	for i, n := range ids {
		if i > 0 && d.notesHeight(append(slices.Clone(keep), n)) > capH {
			return keep, slices.Clone(ids[i:])
		}
		keep = append(keep, n)
	}
	return keep, nil
}

func (d *Doc) paginate(items []Item) []Page {
	t := d.T
	contentW := t.ContentW()
	top := t.MarginTop
	bottom := t.PageH - t.MarginBottom
	keepMin := 2 * t.bodyLine()
	noteCap := t.ContentH() * noteCapFrac

	var pages []Page
	cur := Page{}
	y := top
	var carry []int // notes deferred from earlier pages, placed before any new ones

	// pageNotes decides which notes this page shows if it also takes refs,
	// and which are deferred. Deferred notes keep their order, and new refs
	// never jump ahead of notes already waiting.
	pageNotes := func(refs []int) (keep, rest []int) {
		if len(carry) > 0 {
			return cur.Notes, mergeNotes(carry, refs)
		}
		return d.fitNotes(mergeNotes(cur.Notes, refs), noteCap)
	}
	flush := func() {
		pages = append(pages, cur)
		cur = Page{}
		y = top
		cur.Notes, carry = d.fitNotes(carry, noteCap)
	}

	queue := slices.Clone(items)
	for len(queue) > 0 {
		it := queue[0]
		if _, ok := it.B.(*PageBreak); ok {
			if y > top {
				flush()
			}
			queue = queue[1:]
			continue
		}
		x := t.MarginLeft + it.Indent
		w := contentW - it.Indent

		sb := it.B.SpaceBefore()
		if y == top {
			sb = 0 // no leading space at the head of a page
		}

		// Reserve room for any footnotes this block would drag onto the page.
		// A split fragment can only carry a subset of them, so reserving for
		// the whole block is conservative and always safe.
		notes, deferred := pageNotes(it.B.Refs(d.Notes))
		avail := bottom - y - sb - d.notesHeight(notes)

		h := it.B.Measure(w)
		need := h
		if it.B.KeepWithNext() {
			need += keepRun(queue, contentW, keepMin)
		}

		if need <= avail {
			y += sb
			cur.Items = append(cur.Items, Placed{It: it, X: x, Y: y, W: w, H: h})
			cur.Notes, carry = notes, deferred
			y += h + it.B.SpaceAfter()
			queue = queue[1:]
			continue
		}

		// A block that must stay with its successor is never broken and never
		// stranded: it just moves down.
		if it.B.KeepWithNext() && y > top {
			flush()
			continue
		}

		head, tail, ok := it.split(w, avail)
		if !ok || head == nil {
			if y == top {
				// Won't fit on an empty page either. Draw it and let it bleed
				// rather than loop forever.
				cur.Items = append(cur.Items, Placed{It: it, X: x, Y: y, W: w, H: h})
				cur.Notes, carry = notes, deferred
				y += h + it.B.SpaceAfter()
				queue = queue[1:]
				continue
			}
			flush()
			continue
		}

		y += sb
		hh := head.B.Measure(w)
		cur.Items = append(cur.Items, Placed{It: *head, X: x, Y: y, W: w, H: hh})
		cur.Notes, carry = pageNotes(head.B.Refs(d.Notes))
		if tail == nil {
			y += hh + head.B.SpaceAfter()
			queue = queue[1:]
			continue
		}
		queue[0] = *tail
		flush()
	}
	if len(cur.Items) > 0 || len(cur.Notes) > 0 || len(pages) == 0 {
		pages = append(pages, cur)
	}
	// Notes still waiting after the last block get pages of their own.
	for len(carry) > 0 {
		var p Page
		p.Notes, carry = d.fitNotes(carry, t.ContentH())
		pages = append(pages, p)
	}
	return pages
}

// keepRun is the extra height that queue[0], a keep-with-next block, drags
// onto its page: its space after, then every following keep-with-next block in
// full (a chain like "## A" / "### B" / "#### C" must travel together), then
// the first keepMin of the block the chain is kept with. Looking only one item
// ahead let a chain of headings be stranded at the foot of a page.
func keepRun(queue []Item, contentW, keepMin float64) float64 {
	extra := queue[0].B.SpaceAfter()
	for j := 1; j < len(queue); j++ {
		next := queue[j]
		if !next.B.KeepWithNext() {
			return extra + next.B.SpaceBefore() + min(keepMin, next.height(contentW))
		}
		extra += next.B.SpaceBefore() + next.height(contentW) + next.B.SpaceAfter()
	}
	return extra
}

// ---------------------------------------------------------------------------
// table of contents
// ---------------------------------------------------------------------------

// buildTOC reads the heading placements out of a paginated document.
func (d *Doc) buildTOC(pages []Page) []Item {
	t := d.T
	var lines []Item
	for i, p := range pages {
		for _, pl := range p.Items {
			h, ok := pl.It.B.(*Heading)
			if !ok || h.Level > t.TOCDepth || h.fromTOC {
				continue
			}
			lines = append(lines, Item{
				B: &TocLine{
					base:  base{after: 0.6},
					Level: h.Level,
					Runs:  withoutRefs(h.Runs, d.Notes),
					Page:  i + 1,
					ID:    h.ID,
					T:     t,
				},
				Indent: float64(h.Level-1) * (t.IndentStep * 0.7),
			})
		}
	}
	if len(lines) == 0 {
		return nil
	}
	title := t.TOCTitle
	if title == "" {
		title = "Contents"
	}
	head := &Heading{
		base:    base{before: t.HeadSpaceHi[1], after: t.HeadSpaceLo[1], keep: true},
		Level:   2,
		Runs:    []Run{{Face: t.Text.Face(t.HeadSize[1], t.FG, canvas.FontBold), Text: title}},
		Plain:   title,
		Opts:    t.textOpts(),
		Rule:    t.HeadingRules >= 2,
		T:       t,
		fromTOC: true,
	}
	out := append([]Item{{B: head}}, lines...)
	out[len(out)-1].B.(*TocLine).after = t.BlockSpace
	return out
}

// spliceTOC inserts the TOC after the document's leading H1, if it has one.
func spliceTOC(items, toc []Item) []Item {
	if len(toc) == 0 {
		return items
	}
	at := 0
	if len(items) > 0 {
		if h, ok := items[0].B.(*Heading); ok && h.Level == 1 {
			at = 1
			// keep any lead paragraph with the title (up to the next heading
			// or page break, so the TOC stays on the title's page)
			for at < len(items) {
				if _, isHead := items[at].B.(*Heading); isHead {
					break
				}
				if _, isBreak := items[at].B.(*PageBreak); isBreak {
					break
				}
				at++
			}
		}
	}
	out := make([]Item, 0, len(items)+len(toc))
	out = append(out, items[:at]...)
	out = append(out, toc...)
	out = append(out, items[at:]...)
	return out
}

// endNotes appends the footnotes as an endnote section.
func (d *Doc) endNotes(items []Item) []Item {
	t := d.T
	if t.Footnotes != "end" || len(d.Body) == 0 {
		return items
	}
	nums := make([]int, 0, len(d.Body))
	for n := range d.Body {
		nums = append(nums, n)
	}
	slices.Sort(nums)

	out := slices.Clone(items)
	out = append(out, Item{B: &Heading{
		base:  base{before: t.HeadSpaceHi[1], after: t.HeadSpaceLo[1], keep: true},
		Level: 2,
		Runs:  []Run{{Face: t.Text.Face(t.HeadSize[1], t.FG, canvas.FontBold), Text: "Notes"}},
		Plain: "Notes", Opts: t.textOpts(), Rule: t.HeadingRules >= 2, T: t,
	}})
	for _, n := range nums {
		out = append(out, Item{B: &Para{
			base:   base{after: 1.2},
			Runs:   d.noteRuns(n),
			Align:  canvas.Left,
			Opts:   t.textOpts(),
			Orphan: 1, Widow: 1,
		}})
	}
	return out
}

// ---------------------------------------------------------------------------
// render
// ---------------------------------------------------------------------------

// Render lays the document out and writes a PDF.
func (d *Doc) Render(w io.Writer, title string) error {
	t := d.T
	items := d.endNotes(d.Items)

	pages := d.paginate(items)
	if t.TOC {
		// Two fixed-point iterations: the first learns the page numbers, the
		// second lays out with the TOC present. The TOC's height doesn't depend
		// on the numbers it contains, so this converges. The loop guard is
		// paranoia, not necessity.
		var toc []Item
		for i := 0; i < 4; i++ {
			next := d.buildTOC(pages)
			if tocEqual(toc, next) {
				break
			}
			toc = next
			pages = d.paginate(spliceTOC(items, toc))
		}
		items = spliceTOC(items, toc)
	}

	if title == "" {
		title = d.Title
	}

	p := pdf.New(w, t.PageW, t.PageH, nil)
	p.SetInfo(title, d.Meta.Subject, d.Meta.Keywords, d.Meta.Author, "inkcap")
	ctx := canvas.NewContext(p)
	ctx.SetCoordSystem(canvas.CartesianIV) // origin top-left, y grows downward
	r := &Rctx{C: ctx, P: p, T: t, Links: d.Links}

	for i, page := range pages {
		if i > 0 {
			p.NewPage(t.PageW, t.PageH)
		}
		d.drawQuoteBars(r, quoteBars(page.Items, t))
		for _, pl := range page.Items {
			d.paint(r, pl)
		}
		d.drawNotes(r, page.Notes)
		d.decorate(r, i+1, len(pages), title)
	}
	return p.Close()
}

func tocEqual(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, ok1 := a[i].B.(*TocLine)
		y, ok2 := b[i].B.(*TocLine)
		if ok1 != ok2 {
			return false
		}
		if ok1 && (x.Page != y.Page || x.Level != y.Level) {
			return false
		}
	}
	return true
}

// barSeg is one vertical blockquote bar on a page, y-down.
type barSeg struct {
	X, Y0, Y1 float64
	Color     color.RGBA
}

// quoteBars computes the blockquote bars for one page. A bar for a given quote
// level runs unbroken from the top of the first consecutive item inside that
// quote to the bottom of the last, so it spans the gaps between paragraphs, and
// each nesting level gets its own bar at its own inset. Pure, so it can be
// tested without drawing.
func quoteBars(items []Placed, t *Theme) []barSeg {
	var out []barSeg
	open := map[int]int{} // quote ID -> index in out of its running segment
	for _, pl := range items {
		cur := map[int]bool{}
		for _, bar := range pl.It.Bars {
			cur[bar.ID] = true
			if j, ok := open[bar.ID]; ok {
				out[j].Y1 = pl.Y + pl.H // extend through the gap to this item
				continue
			}
			open[bar.ID] = len(out)
			col := bar.Color
			if col == (color.RGBA{}) {
				col = t.QuoteBar
			}
			out = append(out, barSeg{X: t.MarginLeft + bar.Inset - t.IndentStep*0.55, Y0: pl.Y, Y1: pl.Y + pl.H, Color: col})
		}
		for id := range open {
			if !cur[id] {
				delete(open, id) // the quote ended
			}
		}
	}
	return out
}

func (d *Doc) drawQuoteBars(r *Rctx, segs []barSeg) {
	r.C.SetStrokeWidth(1.1)
	for _, s := range segs {
		r.C.SetStrokeColor(s.Color)
		r.C.MoveTo(s.X, s.Y0)
		r.C.LineTo(s.X, s.Y1)
		r.C.Stroke()
	}
}

// paint draws one placed item plus its decoration.
func (d *Doc) paint(r *Rctx, pl Placed) {
	t := d.T
	it := pl.It
	if m := it.Marker; m != nil {
		base := pl.Y + baselineOf(m.Face, t.textOpts())
		if fb, ok := it.B.(firstBaseliner); ok {
			base = pl.Y + fb.firstBaseline(pl.W)
		}
		drawMarker(r, m, pl.X-1.6, base, t)
	}
	it.B.Draw(r, pl.X, pl.Y, pl.W)
}

// firstBaseliner is implemented by blocks whose first line of text doesn't sit
// at the top of the block (code boxes, tables, headings). A list marker is
// aligned with that baseline, not with the block's top edge.
type firstBaseliner interface {
	firstBaseline(w float64) float64
}

// drawMarker draws a list marker with its right edge at right and on baseline
// (y-down page coordinates), then any outer marker one list step further left.
func drawMarker(r *Rctx, m *Marker, right, baseline float64, t *Theme) {
	if m.Shape == ShapeText {
		mt := buildText([]Run{{Face: m.Face, Text: m.Text}}, 0, canvas.Left, t.textOpts())
		if mt != nil {
			r.C.DrawText(right-mt.Width, baseline-baselineOf(m.Face, t.textOpts()), mt)
		}
	} else {
		drawMarkerShape(r, m, right, baseline)
	}
	if m.Outer != nil {
		drawMarker(r, m.Outer, right-m.Step, baseline, t)
	}
}

// baselineOf is the distance from the top of a line set in face f with opts
// to its baseline, measured from a laid-out reference glyph so it tracks the
// same leading as real text.
func baselineOf(f *canvas.FontFace, opts *canvas.TextOptions) float64 {
	base := f.Metrics().Ascent
	ref := buildText([]Run{{Face: f, Text: "x"}}, 0, canvas.Left, opts)
	if ref != nil {
		// WalkSpans reports the baseline y-up (-line.y); flip it to y-down.
		ref.WalkSpans(func(_, sy float64, _ canvas.TextSpan) { base = -sy })
	}
	return base
}

// drawMarkerShape draws a vector list marker whose right edge is at right and
// which sits on baseline (both y-down page coordinates), sized from the face
// metrics so it scales with the body size.
func drawMarkerShape(r *Rctx, m *Marker, right, baseline float64) {
	fm := m.Face.Metrics()
	col := m.Face.Fill.Color
	r.C.SetFillColor(canvas.Transparent)
	r.C.SetStrokeColor(col)

	switch m.Shape {
	case ShapeRing:
		rad := fm.XHeight * 0.27
		sw := 0.2
		r.C.SetStrokeWidth(sw)
		r.C.DrawPath(right-rad-sw/2, baseline-fm.XHeight/2, canvas.Circle(rad))

	case ShapeBox, ShapeBoxChecked:
		s := fm.CapHeight * 0.95
		left, top := right-s, baseline-s
		r.C.SetStrokeWidth(0.22)
		r.C.DrawPath(left, top, canvas.Rectangle(s, s))
		if m.Shape == ShapeBoxChecked {
			r.C.SetStrokeWidth(0.32)
			r.C.SetStrokeCapper(canvas.RoundCap)
			r.C.SetStrokeJoiner(canvas.RoundJoin)
			r.C.MoveTo(left+0.2*s, top+0.52*s)
			r.C.LineTo(left+0.42*s, top+0.76*s)
			r.C.LineTo(left+0.82*s, top+0.24*s)
			r.C.Stroke()
			r.C.SetStrokeCapper(canvas.ButtCap)
			r.C.SetStrokeJoiner(canvas.MiterJoin)
		}
	}
}

// drawNotes paints the footnote area at the foot of the text block.
func (d *Doc) drawNotes(r *Rctx, ids []int) {
	t := d.T
	if t.Footnotes != "page" || len(ids) == 0 {
		return
	}
	h := d.notesHeight(ids)
	y := t.PageH - t.MarginBottom - h + t.BlockSpace

	r.C.SetStrokeColor(t.Rule)
	r.C.SetStrokeWidth(0.25)
	r.C.MoveTo(t.MarginLeft, y)
	r.C.LineTo(t.MarginLeft+noteSep, y)
	r.C.Stroke()
	y += 2.0

	for _, n := range ids {
		nt := buildText(d.noteRuns(n), t.ContentW(), canvas.Left, t.textOpts())
		drawRuns(r, nt, t.MarginLeft, y)
		y += d.noteHeight(n) + 0.8
	}
}

// decorate paints the running header and the folio.
func (d *Doc) decorate(r *Rctx, page, total int, title string) {
	t := d.T
	small := t.Text.Face(t.SmallSize, t.Muted)

	if t.RunningHeader && page > 1 && title != "" {
		ht := buildText([]Run{{Face: small, Text: title}}, t.ContentW(), canvas.Left, t.textOpts())
		r.C.DrawText(t.MarginLeft, t.MarginTop-t.FooterGap, ht)
		r.C.SetStrokeColor(t.Rule)
		r.C.SetStrokeWidth(0.2)
		r.C.MoveTo(t.MarginLeft, t.MarginTop-pt(9))
		r.C.LineTo(t.PageW-t.MarginRight, t.MarginTop-pt(9))
		r.C.Stroke()
	}

	if !t.PageNumbers {
		return
	}
	label := strconv.Itoa(page)
	if t.PageTotal {
		label = fmt.Sprintf("%d / %d", page, total)
	}
	ft := buildText([]Run{{Face: small, Text: label}}, t.ContentW(), canvas.Center, t.textOpts())
	r.C.DrawText(t.MarginLeft, t.PageH-t.MarginBottom+t.FooterGap, ft)
}
