# Changelog

All notable changes to inkcap. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/) on the 0.x line.

## [Unreleased]

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

[Unreleased]: https://github.com/scttfrdmn/inkcap/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/scttfrdmn/inkcap/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/scttfrdmn/inkcap/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/scttfrdmn/inkcap/releases/tag/v0.1.0
