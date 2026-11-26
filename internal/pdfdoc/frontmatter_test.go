package pdfdoc

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestResourcesFindsLocalImages(t *testing.T) {
	got, err := Resources([]byte("---\ntitle: Demo\nlogo: cover.svg\n---\n\n![alt](images/pic.png)\n\n<img src='diagram.jpg'>\n\n![remote](https://example.com/a.png)\n"), "docs")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs/cover.svg", "docs/diagram.jpg", "docs/images/pic.png"}
	if !slices.Equal(got, want) {
		t.Fatalf("Resources() = %#v, want %#v", got, want)
	}
}

func TestResourcesPreservesAbsoluteImagePaths(t *testing.T) {
	got, err := Resources([]byte("![absolute](/assets/figure.png)\n"), "docs")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/assets/figure.png"}
	if !slices.Equal(got, want) {
		t.Fatalf("Resources() = %#v, want %#v", got, want)
	}
}

func TestFrontMatterParsed(t *testing.T) {
	cases := []struct {
		name, src string
		want      Meta
	}{
		{"yaml scalars", "---\ntitle: My Doc\nauthor: \"Jane Q. Doe\"\ndescription: 'It''s a test'\ndate: 2026-11-16\n---\n# H\n",
			Meta{Title: "My Doc", Author: "Jane Q. Doe", Subject: "It's a test", Date: "2026-11-16"}},
		{"yaml lists", "---\ntitle: T # trailing comment\nauthor:\n  - Ann\n  - Bob\nkeywords: [go, pdf, \"mark down\"]\nextra:\n  nested: ignored\n...\nbody\n",
			Meta{Title: "T", Author: "Ann, Bob", Keywords: "go, pdf, mark down"}},
		{"toml", "+++\ntitle = \"Toml Doc\"\nauthors = [\"A\", \"B\"]\ntags = [\"x\"]\n[params]\nk = 1\n+++\nbody\n",
			Meta{Title: "Toml Doc", Author: "A, B", Keywords: "x"}},
		{"bom", "\ufeff---\ntitle: BOM\n---\nbody\n", Meta{Title: "BOM"}},
	}
	for _, tc := range cases {
		meta, body, err := splitFrontMatter([]byte(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if meta != tc.want {
			t.Errorf("%s: meta = %+v, want %+v", tc.name, meta, tc.want)
		}
		if bytes.Contains(body, []byte("title")) || bytes.Contains(body, []byte("+++")) {
			t.Errorf("%s: front matter left in body: %q", tc.name, body)
		}
		// Line numbers are preserved: the body has as many lines as the source.
		if strings.Count(string(body), "\n") != strings.Count(strings.TrimPrefix(tc.src, "\ufeff"), "\n") {
			t.Errorf("%s: body line count changed", tc.name)
		}
	}
}

// Documents that merely start with a thematic break, or never close the
// block, are not front matter and must be left untouched.
func TestNotFrontMatter(t *testing.T) {
	for name, src := range map[string]string{
		"rule then blank": "---\n\n# Heading\n\nText\n\n---\n",
		"rule then prose": "---\nJust a paragraph after a rule.\n---\n",
		"unclosed":        "---\ntitle: never closed\n\n# Body\n",
		"rule mid-doc":    "# Title\n\n---\ntitle: x\n---\n",
		"too short":       "---\n",
	} {
		meta, body, err := splitFrontMatter([]byte(src))
		if err != nil || meta != (Meta{}) || string(body) != src {
			t.Errorf("%s: treated as front matter (meta %+v, err %v)", name, meta, err)
		}
	}
}

func TestBadTOMLFrontMatterErrors(t *testing.T) {
	if _, _, err := splitFrontMatter([]byte("+++\ntitle = \n+++\nbody\n")); err == nil || !strings.Contains(err.Error(), "front matter") {
		t.Errorf("err = %v, want a front matter error", err)
	}
}

// End to end: the block is not rendered, the title comes from front matter
// (beating the first H1), and author/subject/keywords reach the PDF info.
func TestFrontMatterInDocument(t *testing.T) {
	th := theme(t, nil)
	d := parse(t, "---\ntitle: Front Title\nauthor: Jane\nsubject: Things\nkeywords: [a, b]\n---\n\n# Body Heading\n\nText.\n", th)
	if d.Title != "Front Title" {
		t.Errorf("Title = %q, want front matter title", d.Title)
	}
	for _, it := range d.Items {
		switch v := it.B.(type) {
		case *Rule:
			t.Error("front matter rendered as a thematic break")
		case *Heading:
			if strings.Contains(v.Plain, "title") {
				t.Errorf("front matter rendered as heading %q", v.Plain)
			}
		case *Para:
			if strings.Contains(plainText(v.Runs), "author") {
				t.Errorf("front matter rendered as text %q", plainText(v.Runs))
			}
		}
	}
	var buf bytes.Buffer
	if err := d.Render(&buf, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/Title(Front Title)", "/Author(Jane)", "/Subject(Things)", "/Keywords(a, b)"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("PDF info lacks %s", want)
		}
	}
	// -title still beats front matter.
	buf.Reset()
	if err := d.Render(&buf, "Flag Title"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("/Title(Flag Title)")) {
		t.Error("-title did not override the front matter title")
	}
}
