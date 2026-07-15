package pdfdoc

import (
	"strings"

	"github.com/tdewolff/canvas"
)

type Cell struct {
	Runs  []Run
	Align canvas.TextAlign
}

type Table struct {
	base
	Head []Cell
	Rows [][]Cell
	T    *Theme

	PadX, PadY float64
}

// contentWidths returns the minimum (longest unbreakable word) and maximum
// (everything on one line) width of a cell, excluding padding.
func cellWidths(runs []Run) (min, max float64) {
	for _, r := range runs {
		max += r.Face.TextWidth(r.Text)
		for _, word := range strings.Fields(r.Text) {
			if w := r.Face.TextWidth(word); w > min {
				min = w
			}
		}
	}
	return
}

// solve implements the CSS "auto" table layout algorithm: grow every column
// from its minimum toward its maximum in proportion to its available slack.
// If even the minimums don't fit, distribute proportionally and let it overflow.
func (tb *Table) solve(w float64) []float64 {
	n := tb.cols()
	mins := make([]float64, n)
	maxs := make([]float64, n)

	consider := func(row []Cell) {
		for i, c := range row {
			if i >= n {
				break
			}
			mn, mx := cellWidths(c.Runs)
			mn += 2 * tb.PadX
			mx += 2 * tb.PadX
			if mn > mins[i] {
				mins[i] = mn
			}
			if mx > maxs[i] {
				maxs[i] = mx
			}
		}
	}
	consider(tb.Head)
	for _, r := range tb.Rows {
		consider(r)
	}

	var sumMin, sumMax float64
	for i := range mins {
		if maxs[i] < mins[i] {
			maxs[i] = mins[i]
		}
		sumMin += mins[i]
		sumMax += maxs[i]
	}

	out := make([]float64, n)
	switch {
	case sumMax <= w:
		// Everything fits unwrapped: hand out the surplus evenly so the table
		// spans the full measure rather than hugging the left margin.
		extra := (w - sumMax) / float64(n)
		for i := range out {
			out[i] = maxs[i] + extra
		}
	case sumMin <= w:
		slack := sumMax - sumMin
		grow := w - sumMin
		for i := range out {
			share := 0.0
			if slack > 0 {
				share = (maxs[i] - mins[i]) / slack * grow
			}
			out[i] = mins[i] + share
		}
	default:
		for i := range out {
			out[i] = mins[i] / sumMin * w
		}
	}
	return out
}

func (tb *Table) cols() int {
	n := len(tb.Head)
	for _, r := range tb.Rows {
		if len(r) > n {
			n = len(r)
		}
	}
	return n
}

func (tb *Table) rowHeight(row []Cell, widths []float64) float64 {
	h := 0.0
	for i, c := range row {
		if i >= len(widths) {
			break
		}
		t := buildText(c.Runs, widths[i]-2*tb.PadX, c.Align, tb.T.textOpts())
		if t != nil && t.Height > h {
			h = t.Height
		}
	}
	return h + 2*tb.PadY
}

func (tb *Table) Measure(w float64) float64 {
	widths := tb.solve(w)
	h := 0.0
	if len(tb.Head) > 0 {
		h += tb.rowHeight(tb.Head, widths)
	}
	for _, r := range tb.Rows {
		h += tb.rowHeight(r, widths)
	}
	return h
}

// splitRow breaks a single row across a page by breaking each of its cells at
// a line boundary. This is the escape hatch for a row taller than the page:
// without it, a 40-line cell would simply bleed off the bottom.
func (tb *Table) splitRow(row []Cell, widths []float64, avail float64) (head, tail []Cell, ok bool) {
	budget := avail - 2*tb.PadY
	if budget <= 0 {
		return nil, nil, false
	}
	// Lay every cell out, then find the largest common line count that fits.
	lines := make([][][]Run, len(row))
	maxN := 0
	for i, c := range row {
		if i >= len(widths) {
			break
		}
		cw := widths[i] - 2*tb.PadX
		lines[i] = extractLines(buildText(c.Runs, cw, c.Align, tb.T.textOpts()))
		if n := len(lines[i]); n > maxN {
			maxN = n
		}
	}
	if maxN < 2 {
		return nil, nil, false
	}
	fits := func(k int) bool {
		h := 0.0
		for i := range row {
			if i >= len(widths) || k > len(lines[i]) {
				continue
			}
			t := buildText(joinLines(lines[i][:k]), widths[i]-2*tb.PadX, row[i].Align, tb.T.textOpts())
			if t != nil && t.Height > h {
				h = t.Height
			}
		}
		return h <= budget
	}
	lo, hi := 0, maxN-1 // never take every line: the tail must be non-empty
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo < 1 {
		return nil, nil, false
	}
	head = make([]Cell, len(row))
	tail = make([]Cell, len(row))
	for i, c := range row {
		head[i] = Cell{Align: c.Align}
		tail[i] = Cell{Align: c.Align}
		if i >= len(widths) {
			continue
		}
		if lo < len(lines[i]) {
			head[i].Runs = joinLines(lines[i][:lo])
			tail[i].Runs = joinLines(lines[i][lo:])
		} else {
			head[i].Runs = c.Runs
		}
	}
	return head, tail, true
}

// Split breaks between body rows and repeats the header on the continuation.
// If not even one row fits, it breaks inside the first row.
func (tb *Table) Split(w, avail float64) (Block, Block, bool) {
	if tb.Measure(w) <= avail {
		return tb, nil, true
	}
	widths := tb.solve(w)
	headH := 0.0
	if len(tb.Head) > 0 {
		headH = tb.rowHeight(tb.Head, widths)
	}

	y := headH
	k := 0
	for _, r := range tb.Rows {
		rh := tb.rowHeight(r, widths)
		if y+rh > avail {
			break
		}
		y += rh
		k++
	}

	if k == 0 {
		// Not one body row fits under the header. If the page is otherwise
		// empty the row is taller than a page, so break inside it; otherwise
		// just move the table down.
		if len(tb.Rows) == 0 || avail < tb.T.ContentH()*0.9 {
			return nil, tb, true
		}
		h, t, ok := tb.splitRow(tb.Rows[0], widths, avail-headH)
		if !ok {
			return nil, tb, true
		}
		head := *tb
		head.Rows = [][]Cell{h}
		head.after = 0
		tail := *tb
		tail.Rows = append([][]Cell{t}, tb.Rows[1:]...)
		tail.before = 0
		return &head, &tail, true
	}
	if len(tb.Rows)-k < 1 {
		return nil, tb, true
	}
	head := *tb
	head.Rows = tb.Rows[:k]
	head.after = 0
	tail := *tb
	tail.Rows = tb.Rows[k:]
	tail.before = 0
	return &head, &tail, true
}

func (tb *Table) Refs(reg map[*canvas.FontFace]int) []int {
	var out []int
	for _, c := range tb.Head {
		out = append(out, refsIn(c.Runs, reg)...)
	}
	for _, r := range tb.Rows {
		for _, c := range r {
			out = append(out, refsIn(c.Runs, reg)...)
		}
	}
	return out
}

func (tb *Table) Draw(r *Rctx, x, y, w float64) {
	widths := tb.solve(w)
	cy := y

	drawRow := func(row []Cell, h float64, header bool) {
		if header {
			r.C.SetFillColor(tb.T.TableHeadBG)
			r.C.SetStrokeColor(canvas.Transparent)
			r.C.DrawPath(x, cy, canvas.Rectangle(w, h))
		}
		cx := x
		for i, c := range row {
			if i >= len(widths) {
				break
			}
			t := buildText(c.Runs, widths[i]-2*tb.PadX, c.Align, tb.T.textOpts())
			drawRuns(r, t, cx+tb.PadX, cy+tb.PadY)
			cx += widths[i]
		}
		cy += h
		// hairline under the row
		col := tb.T.Rule
		width := 0.15
		if header {
			col = tb.T.TableRuleC
			width = 0.4
		}
		r.C.SetStrokeColor(col)
		r.C.SetStrokeWidth(width)
		r.C.MoveTo(x, cy)
		r.C.LineTo(x+w, cy)
		r.C.Stroke()
	}

	// top rule
	r.C.SetStrokeColor(tb.T.TableRuleC)
	r.C.SetStrokeWidth(0.4)
	r.C.MoveTo(x, cy)
	r.C.LineTo(x+w, cy)
	r.C.Stroke()

	if len(tb.Head) > 0 {
		drawRow(tb.Head, tb.rowHeight(tb.Head, widths), true)
	}
	for _, row := range tb.Rows {
		drawRow(row, tb.rowHeight(row, widths), false)
	}
}
