package pdfdoc

import (
	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/pdf"
)

// Run is a styled inline fragment. Face identity doubles as the link key:
// every linked run gets its own FontFace instance, so a pointer lookup
// recovers the URL when we walk the laid-out spans.
type Run struct {
	Face *canvas.FontFace
	Text string
	Link string
}

// Rctx is threaded through Draw so blocks can register PDF-level features
// (links, outline entries) that live outside the vector canvas.
type Rctx struct {
	C     *canvas.Context
	P     *pdf.PDF
	T     *Theme
	Links map[*canvas.FontFace]string
}

// Link registers a hyperlink rect given in y-down page coordinates.
func (r *Rctx) Link(uri string, x, yTop, w, h float64) {
	if r.P == nil || uri == "" {
		return
	}
	y := r.T.PageH - yTop - h
	r.P.AddLink(uri, canvas.Rect{X0: x, Y0: y, X1: x + w, Y1: y + h})
}

// Outline registers a PDF bookmark at a y-down page coordinate.
func (r *Rctx) Outline(name string, level int, yTop float64) {
	if r.P == nil {
		return
	}
	r.P.AddOutline(name, level, r.T.PageH-yTop)
}

// Block is one item in the vertical flow. The contract:
//
//   - Measure(w) is pure and cheap enough to call repeatedly.
//   - Split(w, avail) returns (head, tail, true) if the block can be broken so
//     that head fits within avail. head may be nil, meaning "push it all down".
//   - Draw paints with (x, y) as the top-left corner, y increasing downward.
type Block interface {
	Measure(w float64) float64
	Split(w, avail float64) (head, tail Block, ok bool)
	Draw(r *Rctx, x, y, w float64)

	SpaceBefore() float64
	SpaceAfter() float64
	KeepWithNext() bool

	// Refs reports the footnotes this block (or fragment of a block) carries,
	// so the paginator can reserve room for them on the page that will hold it.
	// Splitting a block therefore splits its footnotes too, for free.
	Refs(reg map[*canvas.FontFace]int) []int
}

// Note is a footnote: its number and its (already styled) content.
type Note struct {
	Num  int
	Runs []Run
}

// Item wraps a Block with its position in the (flattened) nesting structure.
// Blockquotes and lists are not recursive containers in the flow — they are
// left insets plus decoration. This keeps the paginator flat and total.
type Item struct {
	B      Block
	Indent float64 // left inset in mm
	Bar    bool    // draw a blockquote rule in the gutter
	Marker *Marker // list bullet/number, drawn once on the first fragment
}

// Marker is a list bullet or number, right-aligned into the indent gutter.
type Marker struct {
	Text string
	Face *canvas.FontFace
}

// split forwards to the block and preserves decoration, dropping the marker
// on the continuation fragment.
func (it Item) split(w, avail float64) (head, tail *Item, ok bool) {
	h, t, ok := it.B.Split(w-it.Indent, avail)
	if !ok {
		return nil, nil, false
	}
	if h != nil {
		hi := it
		hi.B = h
		head = &hi
	}
	if t != nil {
		ti := it
		ti.B = t
		ti.Marker = nil
		tail = &ti
	}
	return head, tail, true
}

func (it Item) height(w float64) float64 { return it.B.Measure(w - it.Indent) }

// base carries the common spacing fields so concrete blocks stay small.
type base struct {
	before, after float64
	keep          bool
}

func (b base) SpaceBefore() float64 { return b.before }
func (b base) SpaceAfter() float64  { return b.after }
func (b base) KeepWithNext() bool   { return b.keep }

// Refs defaults to none; blocks that hold inline runs override it.
func (b base) Refs(map[*canvas.FontFace]int) []int { return nil }

// refsIn collects, in order, the footnote numbers carried by a run list.
func refsIn(runs []Run, reg map[*canvas.FontFace]int) []int {
	var out []int
	for _, r := range runs {
		if n, ok := reg[r.Face]; ok {
			out = append(out, n)
		}
	}
	return out
}

func atomicSplit(b Block, w, avail float64) (Block, Block, bool) {
	if b.Measure(w) <= avail {
		return b, nil, true
	}
	return nil, b, true // push whole block to the next page
}
