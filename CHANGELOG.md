# Changelog

All notable changes to inkcap. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/) on the 0.x line.

## [Unreleased]

### Fixed
- Footnotes that can't fit at the foot of their page continue on the next
  page instead of being drawn over the body (#70).
- List markers: `1)` keeps its delimiter, wide numbers like `100.` widen the
  list instead of sticking into the margin, a marker before a code block, table
  or heading sits on its first line, empty items keep their marker, and `- - x`
  shows both bullets (#73).
- Email autolinks link to `mailto:` (#74); tabs in code expand to the next tab
  stop (#72); an input with no content warns instead of silently producing a
  blank page (#76).
- A missing image warns (`image x.png: not found`), and remote images warn that
  they aren't fetched, instead of silently showing the alt text (#67).
- A linked image on its own line (`[![alt](fig.png)](url)`) is a clickable
  figure rather than an inline image (#68).
- Tight lists and definition lists are followed by block spacing instead of
  running into the next paragraph (#66).
- Blockquote bars run unbroken through a quote, each nesting level gets its
  own bar, lists inside quotes keep it, and adjacent quotes stay separate (#65).
- Code blocks use the fallback fonts and missing-glyph warning like prose, and
  wide (CJK) characters count as two columns when wrapping (#64).
- A chain of headings (H2, H3, H4 in a row) is no longer stranded at the foot
  of a page (#63).
- HTML comments, `<!DOCTYPE>` and `<?…?>` are dropped silently; a multi-line
  comment no longer prints its contents or warns as a dropped tag (#62).
- YAML/TOML front matter is stripped instead of rendering as a rule and a
  heading; its title, author, subject and keywords fill the PDF properties and
  running header (#61).
- Backslash escapes (`\*`, `\|`) and HTML entities (`&copy;`, `&#8212;`) are
  resolved instead of printing literally; code stays literal (#59).

## [0.5.0] — 2026-11-16

### Changed
- macOS binaries are Developer ID signed and notarized by Apple. The Homebrew
  cask no longer strips the quarantine flag; Gatekeeper accepts the binary as
  downloaded (#56).
- Releases are created as drafts and published only after the uploaded assets
  verify against `checksums.txt` and both darwin binaries pass a signed,
  notarized and runs-while-quarantined check on a macOS runner (#56).

## [0.4.0] — 2026-11-15

### Fixed
- Paragraphs whose lines end in hard breaks (poetry, addresses, `<br>`) now
  split across pages instead of running past the bottom margin (#31).
- The second-level list bullet and task-list checkboxes are drawn as vector
  shapes; the embedded font has no glyphs for them, so they rendered as
  `.notdef` boxes. Text markers now use fallback fonts and warn on missing
  glyphs (#32).
- Link click areas sit on the link text; they were shifted up, off the page
  for links a few lines into a paragraph (#45).
- `typography.heading_rules` is honoured for body headings (#33).
- Ordered lists starting at `0.` keep their numbering (#35).
- Footnote numbers no longer leak into the document title, running header,
  outline or table of contents (#34).
- Figures taller than the page are scaled down to fit, with their caption (#36).
- `.deb`/`.rpm` assets have version-less names, so the README's
  `releases/latest/download/…` links keep working (#40).

- Config mistakes are no longer silent: unknown keys are warned about, and bad
  colours, unknown paper sizes and out-of-range values are errors naming the
  key. `-margin 0` now sets zero margins (#38).

### Added
- In-document links: headings get GitHub-style anchors, `[x](#anchor)` jumps to
  the heading, table-of-contents entries are clickable, and links to a missing
  anchor are reported (#37).
- `-` reads Markdown from stdin and, with no `-o`, writes the PDF to stdout
  (`-o -` also writes stdout) (#39).

### Changed
- Flags may follow the file name (`inkcap doc.md -toc`); `--` ends flags (#39).
- The PDF is written to a temp file and renamed into place, so a failed
  render leaves no truncated file; inkcap refuses to overwrite its input (#39).
- `config.Load` also returns warnings (internal API) (#38).
- Homebrew cask uses `install_steps`; install with
  `brew tap scttfrdmn/tap && brew trust scttfrdmn/tap` first (Homebrew 7) (#41).

## [0.3.0] — 2026-07-15

### Added
- SVG images, embedded as vector (#9).
- Branding images (#29).

## [0.2.0] — 2026-07-15

### Added
- Font fallback for glyphs missing from the primary face: `[fonts] fallback` (#7).
- Inline images render in the line instead of falling back to alt text (#8).
- Configurable figure resolution: `[document] image_dpi` (#10).
- Distribution: `.deb`/`.rpm` packages, GHCR Docker image, Homebrew, Scoop,
  install script (#22).

## [0.1.0] — 2026-07-15

First release.

### Added
- `-version` flag and `version` subcommand (#1).
- Tagged release binaries via GoReleaser (#2).

### Fixed
- List bullet glyph follows nesting depth, not the millimetre inset (#5).
- Definition lists render instead of flattening into paragraphs (#6).

[Unreleased]: https://github.com/scttfrdmn/inkcap/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/scttfrdmn/inkcap/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/scttfrdmn/inkcap/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/scttfrdmn/inkcap/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/scttfrdmn/inkcap/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/scttfrdmn/inkcap/releases/tag/v0.1.0
