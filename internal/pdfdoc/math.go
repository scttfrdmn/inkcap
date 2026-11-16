package pdfdoc

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/scttfrdmn/canvas"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Markdown math, as GitHub (and GitLab, Obsidian, Jupyter, Pandoc) write it:
//
//	inline   $x^2$  and  $`x^2`$
//	display  $$\sum_i x_i$$  (in a paragraph, or on lines of its own)
//	         ```math fenced blocks
//
// Formulas are typeset by the pure-Go TeX engine inside canvas (star-tex), so
// the binary stays static. That engine is plain TeX, so mathPreamble maps the
// common LaTeX/KaTeX commands people write in Markdown onto plain TeX.

// mathNode is a formula found inline: $...$, $`...`$ or $$...$$.
type mathNode struct {
	ast.BaseInline
	Src     string
	Display bool
}

var kindMath = ast.NewNodeKind("Math")

func (n *mathNode) Kind() ast.NodeKind { return kindMath }
func (n *mathNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"Src": n.Src}, nil)
}

// mathBlock is a display formula on lines of its own: $$ ... $$.
type mathBlock struct {
	ast.BaseBlock
	Src  strings.Builder
	done bool
}

var kindMathBlock = ast.NewNodeKind("MathBlock")

func (n *mathBlock) Kind() ast.NodeKind { return kindMathBlock }
func (n *mathBlock) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"Src": n.Src.String()}, nil)
}

// mathExtension registers the inline and block math parsers.
type mathExtension struct{}

func (mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(util.Prioritized(mathInlineParser{}, 150)),
		parser.WithBlockParsers(util.Prioritized(mathBlockParser{}, 150)),
	)
}

type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{'$'} }

func (mathInlineParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	n, src, display := scanMath(line)
	if n == 0 {
		return nil
	}
	block.Advance(n)
	return &mathNode{Src: src, Display: display}
}

// scanMath recognises a formula at the start of line (which begins with '$')
// and returns its length in bytes, its source, and whether it is display math.
// It follows GitHub's/Pandoc's rules for $...$, so prices like "$5 and $10"
// stay text: no space just inside either delimiter, and no digit right after
// the closing one.
func scanMath(line []byte) (int, string, bool) {
	switch {
	case bytes.HasPrefix(line, []byte("$`")):
		if k := bytes.Index(line[2:], []byte("`$")); k > 0 {
			return k + 4, string(line[2 : 2+k]), false
		}
		return 0, "", false
	case bytes.HasPrefix(line, []byte("$$")):
		if k := bytes.Index(line[2:], []byte("$$")); k > 0 {
			return k + 4, strings.TrimSpace(string(line[2 : 2+k])), true
		}
		return 0, "", false
	}
	if len(line) < 3 || isSpaceByte(line[1]) {
		return 0, "", false
	}
	for j := 1; j < len(line); j++ {
		switch line[j] {
		case '\n', '\r', '`':
			return 0, "", false // math doesn't run into a code span
		case '$':
			if line[j-1] == '\\' {
				continue // \$ inside a formula is TeX's dollar sign
			}
			if isSpaceByte(line[j-1]) || (j+1 < len(line) && line[j+1] >= '0' && line[j+1] <= '9') {
				return 0, "", false
			}
			content := line[1:j]
			if content[0] >= '0' && content[0] <= '9' && bytes.IndexByte(content, ' ') >= 0 {
				return 0, "", false // "$5 and ...$": a price, not a formula
			}
			return j + 1, string(content), false
		}
	}
	return 0, "", false
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

type mathBlockParser struct{}

func (mathBlockParser) Trigger() []byte { return []byte{'$'} }

func (mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	rest := bytes.TrimSpace(line[pc.BlockIndent():])
	if !bytes.HasPrefix(rest, []byte("$$")) {
		return nil, parser.NoChildren
	}
	node := &mathBlock{}
	body := rest[2:]
	if k := bytes.Index(body, []byte("$$")); k >= 0 {
		if len(bytes.TrimSpace(body[k+2:])) > 0 {
			return nil, parser.NoChildren // "$$a$$ text": inline display math in a paragraph
		}
		node.Src.Write(body[:k])
		node.done = true
	} else {
		node.Src.Write(body)
	}
	reader.AdvanceToEOL()
	return node, parser.NoChildren
}

func (mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	n := node.(*mathBlock)
	if n.done {
		return parser.Close
	}
	line, _ := reader.PeekLine()
	if k := bytes.Index(line, []byte("$$")); k >= 0 {
		n.Src.WriteString("\n")
		n.Src.Write(line[:k])
		n.done = true
		reader.AdvanceToEOL()
		return parser.Continue | parser.NoChildren
	}
	n.Src.WriteString("\n")
	n.Src.Write(bytes.TrimRight(line, "\r\n"))
	reader.AdvanceToEOL()
	return parser.Continue | parser.NoChildren
}

func (mathBlockParser) Close(ast.Node, text.Reader, parser.Context) {}
func (mathBlockParser) CanInterruptParagraph() bool                 { return true }
func (mathBlockParser) CanAcceptIndentedLine() bool                 { return false }

// mathPreamble maps LaTeX/KaTeX commands common in Markdown math onto the
// plain TeX that canvas's engine understands. Definitions made inside the
// formula's math group are local to it.
// mathPreamble maps LaTeX/KaTeX commands common in Markdown math onto the
// plain TeX that canvas's engine understands (\mathbb has no blackboard-bold
// font there, so bold stands in). Definitions made inside the formula's math
// group are local to it. One per line: TeX reads its input a line at a time
// into a 500-character buffer, and a longer line makes the run fail.
var mathPreamble = strings.Join([]string{
	`\def\mathrm#1{{\rm #1}}`,
	`\def\mathbf#1{{\bf #1}}`,
	`\def\mathit#1{{\it #1}}`,
	`\def\mathsf#1{{\rm #1}}`,
	`\def\mathtt#1{{\tt #1}}`,
	`\def\mathbb#1{{\bf #1}}`,
	`\def\text#1{\hbox{\rm #1}}`,
	`\def\textrm#1{\hbox{\rm #1}}`,
	`\def\textbf#1{\hbox{\bf #1}}`,
	`\def\operatorname#1{\mathop{\rm #1}\nolimits}`,
	`\def\dfrac#1#2{{\displaystyle{#1\over#2}}}`,
	`\def\tfrac#1#2{{\textstyle{#1\over#2}}}`,
	`\def\binom#1#2{{#1\choose#2}}`,
	`\def\ln{\mathop{\rm ln}\nolimits}`,
	`\def\cdots{\mathinner{\cdotp\cdotp\cdotp}}`,
	`\def\implies{\Longrightarrow}`,
	`\def\iff{\Longleftrightarrow}`,
	`\def\le{\leq}\def\ge{\geq}`,
	// cases with both columns in math mode, as in KaTeX/MathJax (plain TeX's
	// \cases sets the second column as text, where \le is an error)
	`\def\cases#1{\left\{\,\vcenter{\normalbaselines\mathsurround=0pt\ialign{$##\hfil$&\quad$##\hfil$\crcr#1\crcr}}\right.}`,
}, "\n") + "\n"

// texLines breaks a long formula into lines of at most texLineMax characters,
// at a space or before a control sequence; a newline in math mode is just
// a space to TeX.
const texLineMax = 200

func texLines(src string) string {
	var sb strings.Builder
	n := 0
	for i, r := range src {
		if n >= texLineMax && (r == ' ' || r == '\\') && i > 0 {
			sb.WriteByte('\n')
			n = 0
		}
		if r == '\n' {
			n = 0
		}
		sb.WriteRune(r)
		n++
	}
	return sb.String()
}

// mathPath typesets a formula, cached per (source, display) since documents
// repeat formulas and the engine re-reads its fonts on every call. size is the
// surrounding text size in points; the engine sets at 10pt.
func (b *builder) mathPath(src string, display bool, size float64) (*canvas.Path, error) {
	key := fmt.Sprintf("%v|%s", display, src)
	if b.mathCache == nil {
		b.mathCache = map[string]mathResult{}
	}
	res, ok := b.mathCache[key]
	if !ok {
		src := rewriteEnvs(src)
		style := ""
		if display {
			style = `\displaystyle `
		}
		// canvas puts the path's origin at the first character it draws, which
		// for a fraction or a big operator is raised or lowered off the
		// baseline. So start with a period (on the baseline, one contour), then
		// drop it: the origin is then the formula's baseline.
		res.path, res.err = canvas.ParseLaTeX(mathPreamble + baseRef + style + texLines(src))
		if res.err == nil {
			res.path, res.err = dropBaseRef(res.path)
		}
		b.mathCache[key] = res
	}
	if res.err != nil {
		return nil, res.err
	}
	return res.path.Transform(canvas.Identity.Scale(size/10, size/10)), nil
}

// envRewrites maps the LaTeX environments common in Markdown math onto plain
// TeX: each \begin{name}...\end{name} becomes open + body + close, with the
// body's \\ row separators turned into \cr.
var envRewrites = map[string][2]string{
	"matrix":   {`\matrix{`, `}`},
	"pmatrix":  {`\pmatrix{`, `}`},
	"bmatrix":  {`\left[\matrix{`, `}\right]`},
	"Bmatrix":  {`\left\{\matrix{`, `}\right\}`},
	"vmatrix":  {`\left|\matrix{`, `}\right|`},
	"Vmatrix":  {`\left\|\matrix{`, `}\right\|`},
	"cases":    {`\cases{`, `}`},
	"aligned":  {`\eqalign{`, `}`},
	"align":    {`\eqalign{`, `}`},
	"align*":   {`\eqalign{`, `}`},
	"gathered": {`\eqalign{`, `}`},
	"split":    {`\eqalign{`, `}`},
}

// rewriteEnvs rewrites supported \begin{...}...\end{...} environments into
// plain TeX. Unknown environments are left for TeX to reject (the formula then
// falls back to its source with a warning). Environments may nest.
func rewriteEnvs(src string) string {
	for {
		i := strings.LastIndex(src, `\begin{`) // innermost first
		if i < 0 {
			return src
		}
		j := strings.IndexByte(src[i+7:], '}')
		if j < 0 {
			return src
		}
		name := src[i+7 : i+7+j]
		rw, ok := envRewrites[name]
		endTag := `\end{` + name + `}`
		k := strings.Index(src[i:], endTag)
		if !ok || k < 0 {
			return src
		}
		body := src[i+7+j+1 : i+k]
		body = strings.ReplaceAll(body, `\\`, `\cr `)
		src = src[:i] + rw[0] + body + rw[1] + src[i+k+len(endTag):]
	}
}

// baseRef is the baseline reference typeset before every formula; see
// dropBaseRef.
const baseRef = `.\kern2pt `

// dropBaseRef removes the leading reference period from a typeset formula and
// moves the formula to start at x = 0. If the first contour doesn't look like
// that period (a small dot on the baseline), the path is returned unchanged.
func dropBaseRef(p *canvas.Path) (*canvas.Path, error) {
	sub := p.Split()
	if len(sub) < 2 {
		return nil, fmt.Errorf("empty formula")
	}
	dot := sub[0].Bounds()
	if dot.W() > 1 || dot.H() > 1 || dot.Y0 < -0.1 || dot.Y0 > 0.1 {
		return p, nil
	}
	rest := &canvas.Path{}
	for _, sp := range sub[1:] {
		rest = rest.Append(sp)
	}
	return rest.Translate(-rest.Bounds().X0, 0), nil
}

type mathResult struct {
	path *canvas.Path
	err  error
}

// mathRun builds the run for a formula: a typeset path on its own face
// instance (canvas merges consecutive runs that share a face into one span,
// and a span holding an object draws only the object), or, if TeX can't set
// it, the source in code style with a warning.
func (b *builder) mathRun(s style, src string, display bool) []Run {
	p, err := b.mathPath(src, display, s.size)
	if err != nil {
		msg := strings.SplitN(err.Error(), "\n", 2)[0]
		w := fmt.Sprintf("math: could not typeset %q (%s); showing the source", src, msg)
		if !containsStr(b.warn, w) {
			b.warn = append(b.warn, w)
		}
		cs := s
		cs.code = true
		var out []Run
		b.emit(cs, src, &out)
		return out
	}
	f := b.face(s)
	if s.link != "" {
		b.links[f] = s.link
	}
	return []Run{{Face: f, Math: p, Link: s.link}}
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
