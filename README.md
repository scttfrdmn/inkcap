# inkcap

[![CI](https://github.com/scttfrdmn/inkcap/actions/workflows/ci.yml/badge.svg)](https://github.com/scttfrdmn/inkcap/actions/workflows/ci.yml)

Markdown → typeset PDF. One static Go binary. No headless browser, no LaTeX,
no CSS engine.

*Coprinopsis atramentaria*, the inkcap: a mushroom that deliquesces into black ink.

```
go install github.com/scttfrdmn/inkcap/cmd/inkcap@latest
# or grab a prebuilt binary from the Releases page:
#   https://github.com/scttfrdmn/inkcap/releases
# or, from a clone:
go build -o inkcap ./cmd/inkcap

inkcap notes.md                    # -> notes.pdf
inkcap init                        # write ~/.inkcap/config.toml
inkcap -toc -style nord doc.md
inkcap version                     # print version, Go version, platform
```

## Configuration

`~/.inkcap/config.toml`, created by `inkcap init` with every default written
out and annotated. Override the location with `-config` or `$INKCAP_CONFIG`.
Flags beat the file; the file beats the built-in defaults. A missing file is
not an error.

Sections: `[page]`, `[typography]`, `[spacing]`, `[colors]`, `[code]`,
`[document]`, `[fonts.text]`, `[fonts.mono]`. Font sizes are in points (canvas'
`Face()` wants points); every other length is in millimetres. Fonts default to
embedded IBM Plex Serif/Mono; point `[fonts.*]` at .ttf files to override, per
style, with a fallback to the embedded face for anything left blank.

## Why it isn't just `AST → gofpdf`

The usual pure-Go markdown-to-PDF tools walk the AST and emit boxes with a
running `y` cursor. That's a *renderer*, not a *layout engine*, and it's why
they produce tables that don't fit and headings stranded at the foot of a page.

```
markdown ──goldmark──▶ AST
                       │
                       ├─ build.go   AST → flat block flow  (nesting becomes
                       │              indent + decoration, not recursion)
                       ▼
                    []Item  { Block, Indent, Bar, Marker }
                       │
                       ├─ layout.go  paginate  → []Page   (pure)
                       │              draw      → PDF
                       ▼
              tdewolff/canvas ──▶ PDF   (shaping, kerning, subsetting,
                                         links, bookmarks)
```

Two decisions carry the whole design:

**The document model is a single column with no floats.** Blockquotes and list
items are not recursive containers; they are a left inset plus a bar or a
marker. The paginator therefore operates on a flat slice.

**Pagination is a separate pass from drawing.** A table of contents needs page
numbers that only exist after pagination, and footnotes shrink the height of
the page they land on. Neither is expressible in a draw-as-you-go loop.
`paginate` is pure and returns `[]Page`; `draw` consumes it.

## The Block contract

```go
type Block interface {
    Measure(w float64) float64
    Split(w, avail float64) (head, tail Block, ok bool)
    Draw(r *Rctx, x, y, w float64)

    SpaceBefore() float64
    SpaceAfter() float64
    KeepWithNext() bool
    Refs(reg map[*canvas.FontFace]int) []int   // footnotes carried
}
```

`Split` is where the typography lives. A block that doesn't want to be broken
says so by returning `(nil, self, true)` — "put me on the next page".

| Block | Splits at | Policy |
|---|---|---|
| `Para` | line boundaries | orphans ≥ 2, widows ≥ 2 (configurable), else move whole |
| `Code` | source-line boundaries | never mid-wrap, so numbering stays honest; ≥ 3 kept, ≥ 2 carried |
| `Table` | body-row boundaries | header repeats; a row taller than a page is broken *inside*, cell by cell |
| `Heading` | never | `KeepWithNext`: won't be left alone at the foot of a page |
| `TocLine`, `Rule`, `Image` | never | move whole |

Because `Split` also splits a block's `Refs`, footnote placement follows
paragraph splitting for free: the fragment that carries the marker is the
fragment whose page reserves the space.

Paragraph splitting is the non-obvious trick. `canvas` does Knuth-Plass line
breaking internally but doesn't expose the broken lines as a mutable structure —
so `extractLines` reads them back out through `Text.WalkLines`, `joinLines`
re-flattens any suffix into a run list, and `fitLines` binary-searches the
largest prefix that fits (O(log n) relayouts, not O(n)). `Table.splitRow` reuses
exactly this machinery to break an oversized row.

## Table of contents

`-toc`. Built by a fixed-point iteration: paginate, read the heading pages,
splice the TOC in, paginate again. The TOC's *height* doesn't depend on the
numbers it contains, so the second pass is stable; `TestTOCConverges` asserts it
settles and that every entry's number matches where the heading actually landed.

## Footnotes

`footnotes = "page" | "end" | "none"`. Page mode reserves the footnote area
before deciding whether a block fits, so a paragraph is broken *earlier* on a
page that has to carry three notes. Notes always land on the page that
references them.

## Table column widths

CSS "auto" table layout: compute each column's minimum (longest unbreakable
word) and maximum (all on one line) content width, then grow every column from
its min toward its max in proportion to its slack. If even the minimums don't
fit, distribute proportionally and overflow.

## Code blocks

Chroma highlighting (`style`, any of its ~70 themes), optional line numbers in a
gutter (`line_numbers`), configurable `tab_width`. Monospace means wrapping is
by exact character count with a two-column hanging indent, so a long line breaks
without reflowing away its indentation. Token faces are cached by
`chroma.StyleEntry`, so a 2,000-line block allocates a handful of font faces.

## HTML

`html = "strip"` (default) keeps the text and drops the tags; `<br>` becomes a
line break. `html = "drop"` discards HTML blocks entirely. Either way, each
distinct dropped tag is reported once on stderr — no more silent mangling.

## Tests

`go test ./...` asserts the invariants that matter, and they are
mutation-checked (break the code, watch them fail):

- a heading is never the last thing on a page
- no paragraph fragment is shorter than the orphan/widow thresholds
- nothing is laid out past the bottom margin, footnote area included
- every footnote lands on the page that references it
- a table repeats its header on every continuation
- a row taller than a page terminates rather than looping
- the TOC reaches a fixed point and its numbers are correct

## Not supported, on purpose

Floats, multi-column, nested tables. This is a tool for developer docs and
single-flow notes, not a replacement for a browser.
