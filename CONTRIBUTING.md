# Contributing to inkcap

Thanks for your interest. inkcap is a small, focused tool — Markdown to a
typeset PDF, one static Go binary — and contributions that keep it that way are
very welcome.

## Getting started

You need Go 1.25 or newer. There are no other build dependencies; the fonts are
embedded and everything else is a Go module.

```
git clone https://github.com/scttfrdmn/inkcap
cd inkcap
go build -o inkcap ./cmd/inkcap
./inkcap testdata/gaps.md        # -> testdata/gaps.pdf
```

## Before you open a pull request

CI runs the following on every push and PR; run them locally first so there are
no surprises:

```
gofmt -l .        # must print nothing — run `gofmt -w .` to fix
go vet ./...
go build ./...
go test -race ./...
```

- **Formatting is enforced.** `gofmt -l .` printing any file fails CI.
- **Tests must pass with `-race`.**

## Tests are invariant-based

inkcap's tests assert the properties that matter rather than pinning exact
output, and they are written to be *mutation-checked* — break the code and the
test should fail. Examples already in the suite:

- a heading is never the last thing on a page;
- no paragraph fragment is shorter than the orphan/widow thresholds;
- nothing is laid out past the bottom margin, footnote area included;
- every footnote lands on the page that references it;
- a table repeats its header on every continuation;
- the table of contents reaches a fixed point and its numbers are correct.

If you add or change layout behaviour, add a test in the same spirit. A good
check: temporarily revert your fix and confirm the new test fails, then restore
it. Tests that guard against being vacuous (e.g. "nothing split; test is
vacuous") are encouraged.

## Scope

inkcap is deliberately **not** a browser or a LaTeX replacement. The document
model is a single column with no floats; blockquotes and lists are an inset plus
decoration, not recursive containers. Features that require a fundamentally
different layout model — floats, multi-column, nested tables, arbitrary HTML/CSS
rendering — are out of scope by design (see the open "Limitation:" issues).

If you're unsure whether an idea fits, open an issue to discuss it before
writing code.

## Architecture in one breath

```
markdown ──goldmark──▶ AST ──build.go──▶ []Item ──layout.go──▶ []Page ──▶ PDF
                                          (flat flow)   (paginate, pure)  (canvas)
```

`internal/pdfdoc` holds the engine: `build.go` (AST → flat block flow),
`layout.go` (pagination + rendering), `blocks.go`/`table.go` (the `Block`
implementations), `theme.go` (resolved styling). `internal/config` loads the
TOML config. `cmd/inkcap` is the CLI. The README's "Why it isn't just
`AST → gofpdf`" section is the best starting point.

## Commit and PR conventions

- Keep commits focused; reference the issue they close (`Closes #123`).
- One logical change per PR.
- Describe what you verified, not just what you changed.

## License

By contributing, you agree that your contributions are licensed under the
Apache License 2.0, the same license as the project.
