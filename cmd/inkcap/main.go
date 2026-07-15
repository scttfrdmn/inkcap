// inkcap converts Markdown into a typeset PDF.
//
// No headless browser, no LaTeX: a goldmark parse, a block-flow layout engine,
// and tdewolff/canvas for shaping and PDF output. One static binary.
//
//	inkcap notes.md                 # -> notes.pdf
//	inkcap init                     # write ~/.inkcap/config.toml
//	inkcap -toc -style nord doc.md
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scttfrdmn/inkcap/internal/config"
	"github.com/scttfrdmn/inkcap/internal/pdfdoc"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		initCmd(os.Args[2:])
		return
	}

	var (
		out     = flag.String("o", "", "output file (default: input with .pdf)")
		title   = flag.String("title", "", "document title (default: first H1)")
		cfgPath = flag.String("config", "", "config file (default: ~/.inkcap/config.toml)")
		paper   = flag.String("paper", "", "override page size: a4 | letter | legal | a5")
		style   = flag.String("style", "", "override chroma syntax style")
		margin  = flag.Float64("margin", 0, "override all margins, in mm")
		toc     = flag.Bool("toc", false, "generate a table of contents")
		notes   = flag.String("footnotes", "", "override footnote placement: page | end | none")
		nums    = flag.Bool("linenumbers", false, "number lines in code blocks")
		quiet   = flag.Bool("q", false, "suppress warnings")
	)
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: inkcap [flags] file.md\n       inkcap init [-force]\n\n")
		fmt.Fprintf(os.Stderr, "config: %s\n\n", config.Path())
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	in := flag.Arg(0)

	cfg, err := config.Load(*cfgPath)
	check(err)

	// Flags win over the file.
	if *paper != "" {
		cfg.Page.Size = strings.ToLower(*paper)
		cfg.Page.Width, cfg.Page.Height = 0, 0
	}
	if *style != "" {
		cfg.Code.Style = *style
	}
	if *margin > 0 {
		cfg.Page.Top, cfg.Page.Bottom = *margin, *margin
		cfg.Page.Left, cfg.Page.Right = *margin, *margin
	}
	if *toc {
		cfg.Document.TOC = true
	}
	if *notes != "" {
		cfg.Document.Footnotes = *notes
	}
	if *nums {
		cfg.Code.LineNumbers = true
	}
	check(cfg.Validate())

	theme, err := pdfdoc.New(cfg)
	check(err)

	src, err := os.ReadFile(in)
	check(err)

	doc, err := pdfdoc.Parse(src, filepath.Dir(in), theme)
	check(err)

	dst := *out
	if dst == "" {
		dst = strings.TrimSuffix(in, filepath.Ext(in)) + ".pdf"
	}
	f, err := os.Create(dst)
	check(err)
	check(doc.Render(f, *title))
	check(f.Close())

	if !*quiet {
		for _, w := range doc.Warn {
			fmt.Fprintln(os.Stderr, "inkcap: warning:", w)
		}
	}
	fmt.Println(dst)
}

func initCmd(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite an existing config")
	_ = fs.Parse(args)

	p, err := config.Init(*force)
	check(err)
	fmt.Println(p)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "inkcap:", err)
		os.Exit(1)
	}
}
