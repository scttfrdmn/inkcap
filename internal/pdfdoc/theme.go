package pdfdoc

import (
	"embed"
	"fmt"
	"image/color"

	"github.com/scttfrdmn/inkcap/internal/config"
	"github.com/tdewolff/canvas"
)

//go:embed fonts/*.ttf
var fontFS embed.FS

// Theme is the resolved, ready-to-use styling. Font sizes are in points (what
// canvas' Face() wants); every other length is in millimetres.
type Theme struct {
	PageW, PageH            float64
	MarginTop, MarginBottom float64
	MarginLeft, MarginRight float64
	FooterGap               float64

	Text *canvas.FontFamily
	Mono *canvas.FontFamily

	BodySize   float64
	CodeSize   float64
	SmallSize  float64
	HeadSize   [6]float64
	LineHeight float64

	ParaSpace   float64
	HeadSpaceHi [6]float64
	HeadSpaceLo [6]float64
	CodePad     float64
	BlockSpace  float64
	IndentStep  float64

	FG          color.RGBA
	Muted       color.RGBA
	Link        color.RGBA
	Rule        color.RGBA
	CodeBG      color.RGBA
	QuoteBar    color.RGBA
	TableHeadBG color.RGBA
	TableRuleC  color.RGBA

	Orphans      int
	Widows       int
	Justify      bool
	HeadingRules int

	ChromaStyle string
	LineNumbers bool
	TabWidth    int

	TOC           bool
	TOCDepth      int
	TOCTitle      string
	Footnotes     string
	PageNumbers   bool
	PageTotal     bool
	RunningHeader bool
	HTML          string
}

func pt(x float64) float64 { return x * 25.4 / 72.0 }

// loadFamily loads a family from explicit paths, falling back to the embedded
// face for any style left blank.
func loadFamily(name string, embedded, override map[canvas.FontStyle]string) (*canvas.FontFamily, error) {
	fam := canvas.NewFontFamily(name)
	for style, def := range embedded {
		if p := override[style]; p != "" {
			if err := fam.LoadFontFile(p, style); err != nil {
				return nil, fmt.Errorf("font %s: %w", p, err)
			}
			continue
		}
		b, err := fontFS.ReadFile(def)
		if err != nil {
			return nil, fmt.Errorf("font %s: %w", def, err)
		}
		if err := fam.LoadFont(b, 0, style); err != nil {
			return nil, fmt.Errorf("font %s: %w", def, err)
		}
	}
	return fam, nil
}

func faceMap(f config.Face) map[canvas.FontStyle]string {
	return map[canvas.FontStyle]string{
		canvas.FontRegular:                  f.Regular,
		canvas.FontBold:                     f.Bold,
		canvas.FontItalic:                   f.Italic,
		canvas.FontBold | canvas.FontItalic: f.BoldItalic,
	}
}

// New resolves a Config into a Theme.
func New(c config.Config) (*Theme, error) {
	text, err := loadFamily("text", map[canvas.FontStyle]string{
		canvas.FontRegular:                  "fonts/IBMPlexSerif-Regular.ttf",
		canvas.FontBold:                     "fonts/IBMPlexSerif-Bold.ttf",
		canvas.FontItalic:                   "fonts/IBMPlexSerif-Italic.ttf",
		canvas.FontBold | canvas.FontItalic: "fonts/IBMPlexSerif-BoldItalic.ttf",
	}, faceMap(c.Fonts.Text))
	if err != nil {
		return nil, err
	}
	mono, err := loadFamily("mono", map[canvas.FontStyle]string{
		canvas.FontRegular: "fonts/IBMPlexMono-Regular.ttf",
		canvas.FontBold:    "fonts/IBMPlexMono-Bold.ttf",
		canvas.FontItalic:  "fonts/IBMPlexMono-Italic.ttf",
	}, faceMap(c.Fonts.Mono))
	if err != nil {
		return nil, err
	}

	col := func(s string) color.RGBA {
		v, err := config.Hex(s)
		if err != nil {
			return color.RGBA{A: 0xff}
		}
		return v
	}

	w, h := c.PageSize()
	t := &Theme{
		PageW: w, PageH: h,
		MarginTop: c.Page.Top, MarginBottom: c.Page.Bottom,
		MarginLeft: c.Page.Left, MarginRight: c.Page.Right,
		FooterGap: 10,

		Text: text, Mono: mono,

		BodySize:   c.Type.BodySize,
		CodeSize:   c.Type.CodeSize,
		SmallSize:  c.Type.SmallSize,
		HeadSize:   c.Type.HeadingSizes,
		LineHeight: c.Type.LineHeight,

		ParaSpace:   c.Spacing.Paragraph,
		HeadSpaceHi: c.Spacing.HeadingAbove,
		HeadSpaceLo: c.Spacing.HeadingBelow,
		CodePad:     c.Spacing.CodePadding,
		BlockSpace:  c.Spacing.Block,
		IndentStep:  c.Spacing.Indent,

		FG:          col(c.Colors.FG),
		Muted:       col(c.Colors.Muted),
		Link:        col(c.Colors.Link),
		Rule:        col(c.Colors.Rule),
		CodeBG:      col(c.Colors.CodeBG),
		QuoteBar:    col(c.Colors.QuoteBar),
		TableHeadBG: col(c.Colors.TableHeadBG),
		TableRuleC:  col(c.Colors.TableRule),

		Orphans:      c.Type.Orphans,
		Widows:       c.Type.Widows,
		Justify:      c.Type.Justify,
		HeadingRules: c.Type.HeadingRules,

		ChromaStyle: c.Code.Style,
		LineNumbers: c.Code.LineNumbers,
		TabWidth:    c.Code.TabWidth,

		TOC:           c.Document.TOC,
		TOCDepth:      c.Document.TOCDepth,
		TOCTitle:      c.Document.TOCTitle,
		Footnotes:     c.Document.Footnotes,
		PageNumbers:   c.Document.PageNumbers,
		PageTotal:     c.Document.PageTotal,
		RunningHeader: c.Document.RunningHeader,
		HTML:          c.Document.HTML,
	}
	if t.TabWidth <= 0 {
		t.TabWidth = 4
	}
	if t.Footnotes == "" {
		t.Footnotes = "page"
	}
	if t.HTML == "" {
		t.HTML = "strip"
	}
	return t, nil
}

func (t *Theme) ContentW() float64 { return t.PageW - t.MarginLeft - t.MarginRight }
func (t *Theme) ContentH() float64 { return t.PageH - t.MarginTop - t.MarginBottom }

func (t *Theme) bodyAlign() canvas.TextAlign {
	if t.Justify {
		return canvas.Justify
	}
	return canvas.Left
}

func (t *Theme) textOpts() *canvas.TextOptions {
	return &canvas.TextOptions{
		LineStretch: t.LineHeight,
		Linebreaker: canvas.KnuthLinebreaker{},
	}
}

func (t *Theme) monoOpts() *canvas.TextOptions {
	return &canvas.TextOptions{
		LineStretch: 0.30,
		Linebreaker: canvas.GreedyLinebreaker{},
	}
}

// bodyLine is the height of one line of body text.
func (t *Theme) bodyLine() float64 {
	return t.Text.Face(t.BodySize, t.FG).Metrics().LineHeight * (1 + t.LineHeight)
}
