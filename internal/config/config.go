// Package config loads ~/.inkcap/config.toml and folds it into a Theme.
//
// Every knob has a default; the file is entirely optional. `inkcap init`
// writes an annotated copy of the defaults so the file doubles as the
// reference documentation for itself.
package config

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Dir is the application's dot directory.
const Dir = ".inkcap"

type Config struct {
	Page     Page       `toml:"page"`
	Fonts    Fonts      `toml:"fonts"`
	Type     Typography `toml:"typography"`
	Spacing  Spacing    `toml:"spacing"`
	Colors   Colors     `toml:"colors"`
	Code     Code       `toml:"code"`
	Document Document   `toml:"document"`
}

type Page struct {
	Size   string  `toml:"size"`   // a4 | letter | legal | a5, or "" with explicit width/height
	Width  float64 `toml:"width"`  // mm, overrides size
	Height float64 `toml:"height"` // mm, overrides size
	Top    float64 `toml:"margin_top"`
	Bottom float64 `toml:"margin_bottom"`
	Left   float64 `toml:"margin_left"`
	Right  float64 `toml:"margin_right"`
}

// Face lists the four files of a family. Empty fields fall back to the
// embedded IBM Plex faces.
type Face struct {
	Regular    string `toml:"regular"`
	Bold       string `toml:"bold"`
	Italic     string `toml:"italic"`
	BoldItalic string `toml:"bold_italic"`
}

type Fonts struct {
	Text Face `toml:"text"`
	Mono Face `toml:"mono"`
}

type Typography struct {
	BodySize     float64    `toml:"body_size"`     // points
	CodeSize     float64    `toml:"code_size"`     // points
	SmallSize    float64    `toml:"small_size"`    // points
	HeadingSizes [6]float64 `toml:"heading_sizes"` // points, H1..H6
	LineHeight   float64    `toml:"line_height"`   // extra leading, 0.42 = +42%
	Justify      bool       `toml:"justify"`
	Orphans      int        `toml:"orphans"`
	Widows       int        `toml:"widows"`
	HeadingRules int        `toml:"heading_rules"` // hairline under H1..Hn; 0 = none
}

type Spacing struct {
	Paragraph    float64    `toml:"paragraph"` // mm
	Block        float64    `toml:"block"`
	Indent       float64    `toml:"indent"`
	CodePadding  float64    `toml:"code_padding"`
	HeadingAbove [6]float64 `toml:"heading_above"`
	HeadingBelow [6]float64 `toml:"heading_below"`
}

type Colors struct {
	FG          string `toml:"fg"`
	Muted       string `toml:"muted"`
	Link        string `toml:"link"`
	Rule        string `toml:"rule"`
	CodeBG      string `toml:"code_bg"`
	QuoteBar    string `toml:"quote_bar"`
	TableHeadBG string `toml:"table_head_bg"`
	TableRule   string `toml:"table_rule"`
}

type Code struct {
	Style       string `toml:"style"` // any chroma style name
	LineNumbers bool   `toml:"line_numbers"`
	TabWidth    int    `toml:"tab_width"`
}

type Document struct {
	TOC           bool   `toml:"toc"`
	TOCDepth      int    `toml:"toc_depth"`
	TOCTitle      string `toml:"toc_title"`
	Footnotes     string `toml:"footnotes"` // page | end | none
	PageNumbers   bool   `toml:"page_numbers"`
	PageTotal     bool   `toml:"page_total"` // render "3 / 12"
	RunningHeader bool   `toml:"running_header"`
	HTML          string `toml:"html"` // strip | drop
}

// Default is the built-in configuration. Everything else is a delta on this.
func Default() Config {
	return Config{
		Page: Page{
			Size: "a4",
			Top:  25, Bottom: 22, Left: 28, Right: 28,
		},
		Type: Typography{
			BodySize: 10.5, CodeSize: 9, SmallSize: 8.5,
			HeadingSizes: [6]float64{20, 15.5, 13, 11.5, 10.5, 10},
			LineHeight:   0.42,
			Justify:      true,
			Orphans:      2,
			Widows:       2,
			HeadingRules: 2,
		},
		Spacing: Spacing{
			Paragraph: 2.5, Block: 2.8, Indent: 7.0, CodePadding: 2.5,
			HeadingAbove: [6]float64{0, 5.6, 4.6, 3.9, 3.2, 3.2},
			HeadingBelow: [6]float64{2.8, 2.1, 1.8, 1.4, 1.1, 1.1},
		},
		Colors: Colors{
			FG: "#1a1a1e", Muted: "#6b6f76", Link: "#1a549e", Rule: "#d8dbe0",
			CodeBG: "#f6f7f9", QuoteBar: "#c7ccd4",
			TableHeadBG: "#f2f4f7", TableRule: "#c9ced6",
		},
		Code: Code{Style: "github", TabWidth: 4},
		Document: Document{
			TOCDepth: 3, TOCTitle: "Contents",
			Footnotes: "page", PageNumbers: true, RunningHeader: true,
			HTML: "strip",
		},
	}
}

// Path returns the config file location: $INKCAP_CONFIG, else ~/.inkcap/config.toml.
func Path() string {
	if p := os.Getenv("INKCAP_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, Dir, "config.toml")
}

// Load reads path (or the default location) over the built-in defaults. A
// missing file is not an error.
func Load(path string) (Config, error) {
	c := Default()
	if path == "" {
		path = Path()
	}
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := toml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	switch c.Document.Footnotes {
	case "page", "end", "none", "":
	default:
		return fmt.Errorf("footnotes: want page|end|none, got %q", c.Document.Footnotes)
	}
	switch c.Document.HTML {
	case "strip", "drop", "":
	default:
		return fmt.Errorf("html: want strip|drop, got %q", c.Document.HTML)
	}
	return nil
}

// PageSize resolves the named paper size, or the explicit override.
func (c Config) PageSize() (w, h float64) {
	if c.Page.Width > 0 && c.Page.Height > 0 {
		return c.Page.Width, c.Page.Height
	}
	switch strings.ToLower(c.Page.Size) {
	case "letter":
		return 215.9, 279.4
	case "legal":
		return 215.9, 355.6
	case "a5":
		return 148, 210
	default:
		return 210, 297
	}
}

// Hex parses "#rrggbb" (or "#rrggbbaa").
func Hex(s string) (color.RGBA, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 && len(s) != 8 {
		return color.RGBA{}, fmt.Errorf("bad colour %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("bad colour %q: %w", s, err)
	}
	if len(s) == 6 {
		return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}, nil
	}
	return color.RGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

// Init writes an annotated default config, refusing to clobber an existing one.
func Init(force bool) (string, error) {
	p := Path()
	if p == "" {
		return "", fmt.Errorf("cannot locate home directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil && !force {
		return p, fmt.Errorf("%s already exists (use -force to overwrite)", p)
	}
	return p, os.WriteFile(p, []byte(annotated), 0o644)
}

const annotated = `# inkcap — markdown to typeset PDF
#
# Every value below is the built-in default; delete anything you don't want to
# override. Font sizes are in points. Every other length is in millimetres.

[page]
size          = "a4"    # a4 | letter | legal | a5
# width       = 210     # explicit mm, overrides size
# height      = 297
margin_top    = 25
margin_bottom = 22
margin_left   = 28
margin_right  = 28

[typography]
body_size     = 10.5
code_size     = 9
small_size    = 8.5
heading_sizes = [20, 15.5, 13, 11.5, 10.5, 10]   # H1..H6
line_height   = 0.42    # extra leading, as a fraction of the natural leading
justify       = true
orphans       = 2       # min lines left at the foot of a page
widows        = 2       # min lines carried to the next page
heading_rules = 2       # hairline under H1..Hn; 0 for none

[spacing]
paragraph      = 2.5
block          = 2.8    # around code, tables, rules, figures
indent         = 7.0    # per level of list / blockquote nesting
code_padding   = 2.5
heading_above  = [0, 5.6, 4.6, 3.9, 3.2, 3.2]
heading_below  = [2.8, 2.1, 1.8, 1.4, 1.1, 1.1]

[colors]
fg            = "#1a1a1e"
muted         = "#6b6f76"
link          = "#1a549e"
rule          = "#d8dbe0"
code_bg       = "#f6f7f9"
quote_bar     = "#c7ccd4"
table_head_bg = "#f2f4f7"
table_rule    = "#c9ced6"

[code]
style        = "github"   # any chroma style: monokai, dracula, nord, tango, ...
line_numbers = false
tab_width    = 4

[document]
toc            = false
toc_depth      = 3
toc_title      = "Contents"
footnotes      = "page"   # page | end | none
page_numbers   = true
page_total     = false    # render "3 / 12" instead of "3"
running_header = true
html           = "strip"  # strip: keep the text, drop the tags. drop: discard.

# Fonts default to the embedded IBM Plex Serif / Mono. Point these at .ttf or
# .otf files to override; any face you leave blank falls back to the embedded one.
# [fonts.text]
# regular     = "/usr/share/fonts/.../Charter-Regular.ttf"
# bold        = "/usr/share/fonts/.../Charter-Bold.ttf"
# italic      = "/usr/share/fonts/.../Charter-Italic.ttf"
# bold_italic = "/usr/share/fonts/.../Charter-BoldItalic.ttf"
#
# [fonts.mono]
# regular = "/usr/share/fonts/.../JetBrainsMono-Regular.ttf"
# bold    = "/usr/share/fonts/.../JetBrainsMono-Bold.ttf"
# italic  = "/usr/share/fonts/.../JetBrainsMono-Italic.ttf"
`
