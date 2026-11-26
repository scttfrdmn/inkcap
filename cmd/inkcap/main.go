// inkcap converts Markdown into a typeset PDF.
//
// No headless browser, no LaTeX: a goldmark parse, a block-flow layout engine,
// and tdewolff/canvas for shaping and PDF output. One static binary.
//
//	inkcap notes.md                 # -> notes.pdf
//	inkcap init                     # write ~/.inkcap/config.toml
//	inkcap -toc -style nord doc.md
//	inkcap doc.md -toc              # flags may follow the file
//	cat doc.md | inkcap - > doc.pdf # stdin to stdout
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/scttfrdmn/inkcap/internal/config"
	"github.com/scttfrdmn/inkcap/internal/pdfdoc"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			initCmd(os.Args[2:])
			return
		case "version", "-version", "--version":
			printVersion()
			return
		}
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// parseInterspersed parses flags that may appear before or after the
// positional arguments (inkcap doc.md -toc), which the flag package alone
// does not allow. Everything after a literal "--" is positional.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(pos, rest...), nil
		}
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// run is the converter, separated from main so it can be tested. It returns
// the process exit code: 0 on success, 1 on failure, 2 on a usage error.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inkcap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		out     = fs.String("o", "", "output file, or - for stdout (default: input with .pdf; stdout when reading stdin)")
		title   = fs.String("title", "", "document title (default: first H1)")
		cfgPath = fs.String("config", "", "config file (default: ~/.inkcap/config.toml)")
		paper   = fs.String("paper", "", "override page size: a4 | letter | legal | a5")
		style   = fs.String("style", "", "override chroma syntax style")
		margin  = fs.Float64("margin", 0, "override all margins, in mm")
		toc     = fs.Bool("toc", false, "generate a table of contents")
		notes   = fs.String("footnotes", "", "override footnote placement: page | end | none")
		nums    = fs.Bool("linenumbers", false, "number lines in code blocks")
		quiet   = fs.Bool("q", false, "suppress warnings")
		watch   = fs.Bool("watch", false, "re-render when the input, images, or config changes")
	)
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: inkcap [flags] file.md   (use - to read stdin)\n       inkcap init [-force]\n       inkcap version\n\n")
		fmt.Fprintf(stderr, "config: %s\n\n", config.Path())
		fs.PrintDefaults()
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2 // flag has already printed the error and usage
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	warn := func(ws []string) {
		if *quiet {
			return
		}
		for _, w := range ws {
			fmt.Fprintln(stderr, "inkcap: warning:", w)
		}
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "inkcap:", err)
		return 1
	}

	cfg, cfgWarn, err := config.Load(*cfgPath)
	if err != nil {
		return fail(err)
	}
	warn(cfgWarn)

	// Flags win over the file.
	if *paper != "" {
		cfg.Page.Size = strings.ToLower(*paper)
		cfg.Page.Width, cfg.Page.Height = 0, 0
	}
	if *style != "" {
		cfg.Code.Style = *style
	}
	if set["margin"] { // explicit, so -margin 0 means zero margins
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
	if err := cfg.Validate(); err != nil {
		return fail(err)
	}

	// Resolve input and output. "-" means stdin / stdout; reading stdin with
	// no -o writes the PDF to stdout, so inkcap works in a pipe.
	in := pos[0]
	fromStdin := in == "-"
	dst := *out
	if dst == "" {
		if fromStdin {
			dst = "-"
		} else {
			dst = strings.TrimSuffix(in, filepath.Ext(in)) + ".pdf"
		}
	}
	if !fromStdin && dst != "-" && samePath(in, dst) {
		return fail(fmt.Errorf("output %s would overwrite the input; use -o to pick another name", dst))
	}
	if *watch {
		if fromStdin || dst == "-" {
			return fail(fmt.Errorf("-watch requires a file input and file output"))
		}
		cleanArgs := withoutWatch(args)
		previous := fileSnapshot(renderInputs(in, *cfgPath))
		if code := run(cleanArgs, stdin, stdout, stderr); code != 0 {
			return code
		}
		watchFiles := func() []string { return renderInputs(in, *cfgPath) }
		for range time.NewTicker(500 * time.Millisecond).C {
			current := fileSnapshot(watchFiles())
			if snapshotsEqual(previous, current) {
				continue
			}
			previous = current
			if code := run(cleanArgs, stdin, stdout, stderr); code != 0 {
				fmt.Fprintln(stderr, "inkcap: watch render failed; waiting for another change")
			}
		}
	}

	theme, err := pdfdoc.New(cfg)
	if err != nil {
		return fail(err)
	}
	var src []byte
	baseDir := "."
	if fromStdin {
		src, err = io.ReadAll(stdin)
	} else {
		src, err = os.ReadFile(in)
		baseDir = filepath.Dir(in)
	}
	if err != nil {
		return fail(err)
	}
	doc, err := pdfdoc.Parse(src, baseDir, theme)
	if err != nil {
		return fail(err)
	}

	if dst == "-" {
		if err := doc.Render(stdout, *title); err != nil {
			return fail(err)
		}
	} else {
		if err := writeAtomic(dst, func(w io.Writer) error { return doc.Render(w, *title) }); err != nil {
			return fail(err)
		}
	}
	warn(doc.Warn)
	if dst != "-" {
		fmt.Fprintln(stdout, dst)
	}
	return 0
}

func withoutWatch(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "-watch" || arg == "--watch" {
			continue
		}
		if name, value, ok := strings.Cut(arg, "="); ok && (name == "-watch" || name == "--watch") {
			if enabled, err := strconv.ParseBool(value); err == nil && enabled {
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

func renderInputs(input, configPath string) []string {
	cfg, _, _ := config.Load(configPath)
	files := []string{input}
	if configPath == "" {
		configPath = config.Path()
	}
	if configPath != "" {
		files = append(files, configPath)
	}
	if src, err := os.ReadFile(input); err == nil {
		if refs, err := pdfdoc.Resources(src, filepath.Dir(input)); err == nil {
			files = append(files, refs...)
		}
	}
	for _, face := range []config.Face{cfg.Fonts.Text, cfg.Fonts.Mono} {
		files = append(files, face.Regular, face.Bold, face.Italic, face.BoldItalic)
	}
	files = append(files, cfg.Fonts.Fallback...)
	return files
}

type fileState struct {
	size     int64
	modified int64
	mode     os.FileMode
}

func fileSnapshot(paths []string) map[string]fileState {
	snapshot := make(map[string]fileState, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			snapshot[abs] = fileState{}
			continue
		}
		snapshot[abs] = fileState{size: info.Size(), modified: info.ModTime().UnixNano(), mode: info.Mode()}
	}
	return snapshot
}

func snapshotsEqual(a, b map[string]fileState) bool {
	if len(a) != len(b) {
		return false
	}
	for path, state := range a {
		if b[path] != state {
			return false
		}
	}
	return true
}

// samePath reports whether a and b name the same file: the same file on disk
// if both exist, otherwise the same cleaned absolute path.
func samePath(a, b string) bool {
	if fa, err := os.Stat(a); err == nil {
		if fb, err := os.Stat(b); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

// writeAtomic renders into a temporary file next to dst and renames it over
// dst only on success, so a failed render never leaves a truncated PDF behind
// (or destroys a good one from an earlier run).
func writeAtomic(dst string, render func(io.Writer) error) (err error) {
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if err = render(f); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
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
