package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() is invalid: %v", err)
	}
}

// The annotated file written by `inkcap init` is the config's own reference
// documentation: every key in it must be one inkcap reads, and it must be valid.
func TestAnnotatedConfigIsKnownAndValid(t *testing.T) {
	c := Default()
	md, err := toml.Decode(annotated, &c)
	if err != nil {
		t.Fatal(err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		t.Errorf("annotated config has unknown keys: %v", u)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("annotated config is invalid: %v", err)
	}
}

// Bad values are rejected with a message naming the key, instead of being
// silently replaced (bad colour -> black, unknown paper -> A4).
func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name  string
		tweak func(*Config)
		key   string // must appear in the error
	}{
		{"bad colour", func(c *Config) { c.Colors.Link = "blue" }, "colors.link"},
		{"short colour", func(c *Config) { c.Colors.FG = "#fff" }, "colors.fg"},
		{"unknown paper", func(c *Config) { c.Page.Size = "tabloid" }, "page.size"},
		{"width only", func(c *Config) { c.Page.Width = 200 }, "width and height"},
		{"negative margin", func(c *Config) { c.Page.Left = -1 }, "page.margin_left"},
		{"margins too wide", func(c *Config) { c.Page.Left, c.Page.Right = 110, 110 }, "left + right"},
		{"margins too tall", func(c *Config) { c.Page.Top, c.Page.Bottom = 150, 150 }, "top + bottom"},
		{"zero body size", func(c *Config) { c.Type.BodySize = 0 }, "typography.body_size"},
		{"zero heading size", func(c *Config) { c.Type.HeadingSizes[2] = 0 }, "H3"},
		{"negative leading", func(c *Config) { c.Type.LineHeight = -0.1 }, "typography.line_height"},
		{"old-style leading", func(c *Config) { c.Type.LineHeight = 0.42 }, "multiple of the font size"},
		{"huge leading", func(c *Config) { c.Type.LineHeight = 5 }, "typography.line_height"},
		{"negative widows", func(c *Config) { c.Type.Widows = -1 }, "widows"},
		{"heading_rules 7", func(c *Config) { c.Type.HeadingRules = 7 }, "typography.heading_rules"},
		{"negative indent", func(c *Config) { c.Spacing.Indent = -2 }, "spacing.indent"},
		{"negative heading_below", func(c *Config) { c.Spacing.HeadingBelow[4] = -1 }, "spacing.heading_below[4]"},
		{"negative tab_width", func(c *Config) { c.Code.TabWidth = -4 }, "code.tab_width"},
		{"toc_depth 0", func(c *Config) { c.Document.TOCDepth = 0 }, "document.toc_depth"},
		{"toc_depth 7", func(c *Config) { c.Document.TOCDepth = 7 }, "document.toc_depth"},
		{"negative dpi", func(c *Config) { c.Document.ImageDPI = -1 }, "document.image_dpi"},
		{"number_from 0", func(c *Config) { c.Document.NumberFrom = 0 }, "document.number_from"},
		{"bad footnotes", func(c *Config) { c.Document.Footnotes = "side" }, "footnotes"},
		{"bad html", func(c *Config) { c.Document.HTML = "render" }, "html"},
	}
	for _, tc := range cases {
		c := Default()
		tc.tweak(&c)
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%s: error %q doesn't name %q", tc.name, err, tc.key)
		}
	}
}

func TestValidateAccepts(t *testing.T) {
	for name, tweak := range map[string]func(*Config){
		"explicit size":  func(c *Config) { c.Page.Width, c.Page.Height = 100, 150 },
		"zero margins":   func(c *Config) { c.Page.Top, c.Page.Bottom, c.Page.Left, c.Page.Right = 0, 0, 0, 0 },
		"empty size":     func(c *Config) { c.Page.Size = "" },
		"upper case":     func(c *Config) { c.Page.Size = "Letter" },
		"rgba colour":    func(c *Config) { c.Colors.CodeBG = "#f6f7f980" },
		"no rules":       func(c *Config) { c.Type.HeadingRules = 0 },
		"default dpi":    func(c *Config) { c.Document.ImageDPI = 0 },
		"no orphan rule": func(c *Config) { c.Type.Orphans, c.Type.Widows = 0, 0 },
		"tight leading":  func(c *Config) { c.Type.LineHeight = 1.2 },
	} {
		c := Default()
		tweak(&c)
		if err := c.Validate(); err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
	}
}

func TestPageSize(t *testing.T) {
	for size, want := range map[string][2]float64{
		"a4": {210, 297}, "A5": {148, 210}, "letter": {215.9, 279.4}, "legal": {215.9, 355.6}, "": {210, 297},
	} {
		c := Default()
		c.Page.Size = size
		if w, h := c.PageSize(); w != want[0] || h != want[1] {
			t.Errorf("%q: %v × %v, want %v × %v", size, w, h, want[0], want[1])
		}
	}
	c := Default()
	c.Page.Width, c.Page.Height = 100, 150
	if w, h := c.PageSize(); w != 100 || h != 150 {
		t.Errorf("explicit: %v × %v, want 100 × 150", w, h)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Unknown keys (typos, or a key in the wrong table) are reported as warnings
// rather than silently ignored; recognised keys still apply.
func TestLoadWarnsOnUnknownKeys(t *testing.T) {
	p := writeConfig(t, "[page]\nmargn_top = 10\nmargin_left = 20\n\n[typografy]\nbody_size = 12\n")
	c, warn, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Page.Left != 20 {
		t.Errorf("margin_left = %v, want 20", c.Page.Left)
	}
	joined := strings.Join(warn, "\n")
	for _, k := range []string{"page.margn_top", "typografy"} {
		if !strings.Contains(joined, k) {
			t.Errorf("no warning for %q; warnings: %v", k, warn)
		}
	}
	if strings.Contains(joined, "margin_left") {
		t.Errorf("warned about a known key: %v", warn)
	}
}

func TestLoadMissingFileIsDefaults(t *testing.T) {
	c, warn, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || len(warn) != 0 {
		t.Fatalf("missing file: err=%v warn=%v", err, warn)
	}
	if c.Page.Size != Default().Page.Size {
		t.Errorf("missing file didn't yield defaults")
	}
}

func TestLoadInvalidNamesFile(t *testing.T) {
	p := writeConfig(t, "[colors]\nlink = \"blue\"\n")
	if _, _, err := Load(p); err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "colors.link") {
		t.Errorf("err = %v, want it to name the file and colors.link", err)
	}
}
