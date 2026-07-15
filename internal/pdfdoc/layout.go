package pdfdoc

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/pdf"
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

func (d *Doc) paginate(items []Item) []Page {
	t := d.T
	contentW := t.ContentW()
	top := t.MarginTop
	bottom := t.PageH - t.MarginBottom
	keepMin := 2 * t.bodyLine()

	var pages []Page
	cur := Page{}
	y := top

	flush := func() {
		pages = append(pages, cur)
		cur = Page{}
		y = top
	}

	queue := slices.Clone(items)
	for len(queue) > 0 {
		it := queue[0]
		x := t.MarginLeft + it.Indent
		w := contentW - it.Indent

		sb := it.B.SpaceBefore()
		if y == top {
			sb = 0 // no leading space at the head of a page
		}

		// Reserve room for any footnotes this block would drag onto the page.
		// A split fragment can only carry a subset of them, so reserving for
		// the whole block is conservative and always safe.
		refs := it.B.Refs(d.Notes)
		notes := mergeNotes(cur.Notes, refs)
		avail := bottom - y - sb - d.notesHeight(notes)

		h := it.B.Measure(w)
		need := h
		if it.B.KeepWithNext() && len(queue) > 1 {
			need += it.B.SpaceAfter() + min(keepMin, queue[1].height(contentW))
		}

		if need <= avail {
			y += sb
			cur.Items = append(cur.Items, Placed{It: it, X: x, Y: y, W: w, H: h})
			cur.Notes = notes
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
				cur.Notes = notes
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
		cur.Notes = mergeNotes(cur.Notes, head.B.Refs(d.Notes))
		if tail == nil {
			y += hh + head.B.SpaceAfter()
			queue = queue[1:]
			continue
		}
		queue[0] = *tail
		flush()
	}
	if len(cur.Items) > 0 || len(pages) == 0 {
		pages = append(pages, cur)
	}
	return pages
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
					Runs:  h.Runs,
					Page:  i + 1,
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
			// keep any lead paragraph with the title
			for at < len(items) {
				if _, isHead := items[at].B.(*Heading); isHead {
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
	p.SetInfo(title, "", "", "", "inkcap")
	ctx := canvas.NewContext(p)
	ctx.SetCoordSystem(canvas.CartesianIV) // origin top-left, y grows downward
	r := &Rctx{C: ctx, P: p, T: t, Links: d.Links}

	for i, page := range pages {
		if i > 0 {
			p.NewPage(t.PageW, t.PageH)
		}
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

// paint draws one placed item plus its decoration.
func (d *Doc) paint(r *Rctx, pl Placed) {
	t := d.T
	it := pl.It
	if it.Bar {
		bx := pl.X - t.IndentStep*0.55
		r.C.SetStrokeColor(t.QuoteBar)
		r.C.SetStrokeWidth(1.1)
		r.C.MoveTo(bx, pl.Y)
		r.C.LineTo(bx, pl.Y+pl.H)
		r.C.Stroke()
	}
	if it.Marker != nil {
		gutter := t.IndentStep - 1.6
		mt := buildText([]Run{{Face: it.Marker.Face, Text: it.Marker.Text}},
			gutter, canvas.Right, t.textOpts())
		r.C.DrawText(pl.X-t.IndentStep, pl.Y, mt)
	}
	it.B.Draw(r, pl.X, pl.Y, pl.W)
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
