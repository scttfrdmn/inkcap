package pdfdoc

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/canvas"
	"github.com/scttfrdmn/inkcap/internal/config"
)

// The $ rules follow GitHub/Pandoc, so ordinary dollar signs stay text.
func TestScanMath(t *testing.T) {
	for in, want := range map[string]string{
		"$x^2$ rest":        "x^2",
		"$`a<b`$ rest":      "a<b",
		"$$\\sum_i x$$ end": "\\sum_i x",
		"$a\\$b$":           "a\\$b",
		"$5 and $10":        "", // price
		"$ x$":              "", // space after the opening $
		"$x $":              "", // space before the closing $
		"$x$5":              "", // digit after the closing $
		"$HOME and `$`":     "", // a backtick ends the search
		"$x\ny$":            "", // no newlines in inline math
		"$$":                "",
	} {
		_, got, _ := scanMath([]byte(in))
		if got != want {
			t.Errorf("scanMath(%q) = %q, want %q", in, got, want)
		}
	}
}

func mathRuns(items []Item) (inline, display int) {
	for _, it := range items {
		p, ok := it.B.(*Para)
		if !ok {
			continue
		}
		for _, r := range p.Runs {
			if r.Math != nil {
				if p.Align == canvas.Center {
					display++
				} else {
					inline++
				}
			}
		}
	}
	return
}

// Every form GitHub accepts becomes a typeset formula: inline $...$ and
// $`...`$, display $$...$$ (one line, several lines, alone in a paragraph)
// and ```math. Code is left alone, and nothing warns.
func TestMathForms(t *testing.T) {
	th := theme(t, nil)
	src := "Inline $x^2$ and $`\\alpha < \\beta`$ and `$code$`.\n\n" +
		"$$\\int_0^1 x\\,dx$$\n\n" +
		"$$\n\\sum_{i=1}^n i\n= \\frac{n(n+1)}{2}\n$$\n\n" +
		"```math\n\\lim_{n\\to\\infty} \\left(1+\\frac{1}{n}\\right)^n = e\n```\n\n" +
		"```\n$not math$\n```\n"
	d := parse(t, src, th)
	if len(d.Warn) != 0 {
		t.Errorf("warnings: %v", d.Warn)
	}
	inline, display := mathRuns(d.Items)
	if inline != 2 || display != 3 {
		t.Errorf("got %d inline and %d display formulas, want 2 and 3", inline, display)
	}
	for _, it := range d.Items {
		if p, ok := it.B.(*Para); ok && strings.Contains(plainText(p.Runs), "code") && !strings.Contains(plainText(p.Runs), "$code$") {
			t.Error("math was parsed inside a code span")
		}
		if c, ok := it.B.(*Code); ok && plainText(c.Lines[0]) != "$not math$" {
			t.Error("math was parsed inside a code block")
		}
	}
	var buf bytes.Buffer
	if err := d.Render(&buf, ""); err != nil {
		t.Fatal(err)
	}
}

// A corpus of formulas as people write them on GitHub, including the LaTeX
// commands mathPreamble maps onto plain TeX, all typeset without falling back.
func TestMathCorpus(t *testing.T) {
	th := theme(t, nil)
	corpus := []string{
		`E = mc^2`, `a^2 + b^2 = c^2`, `x_{i,j}^{(k)}`, `\frac{a+b}{c}`, `\dfrac{1}{2}`, `\tfrac{1}{2}`,
		`\sqrt{x^2+1}`, `\sum_{i=1}^{n} i`, `\prod_{k} p_k`, `\int_0^\infty e^{-x}\,dx`, `\lim_{x\to 0} \frac{\sin x}{x}`,
		`\alpha\beta\gamma\delta\epsilon\theta\lambda\mu\pi\sigma\phi\omega`, `\Gamma\Delta\Theta\Lambda\Sigma\Phi\Omega`,
		`\mathbb{R}^n`, `\mathbf{v} \cdot \mathbf{w}`, `\mathrm{d}x`, `\mathit{rate}`, `\text{if } x > 0`,
		`\operatorname{argmax}_x f(x)`, `\log n`, `\ln x`, `\exp(x)`, `\max(a, b)`, `O(n \log n)`,
		`\left( \frac{1}{n} \right)^n`, `\binom{n}{k}`, `a \le b \ge c \ne d`, `x \in A \subset B`, `\forall x \exists y`,
		`p \implies q \iff r`, `\infty`, `\partial f / \partial x`, `\nabla \cdot F`, `f'(x)`, `\hat{x}\bar{y}\vec{v}\tilde{z}`,
		`\{1, 2, 3\}`, `|x|`, `\langle u, v \rangle`, `1 + 2 + \cdots + n`, `\pmatrix{a & b \cr c & d}`,
		`\cases{1 & x > 0 \cr 0 & x \le 0}`,
		`\begin{cases} 1 & x > 0 \\ 0 & x \le 0 \end{cases}`,
		`\begin{pmatrix} a & b \\ c & d \end{pmatrix}`, `\begin{bmatrix} 1 & 0 \\ 0 & 1 \end{bmatrix}`,
		`\begin{vmatrix} a & b \\ c & d \end{vmatrix}`, `\begin{aligned} a &= b + c \\ d &= e \end{aligned}`,
		`\matrix{1 & 2 \cr 3 & 4}`,
	}
	for _, f := range corpus {
		b := &builder{t: th, links: map[*canvas.FontFace]string{}}
		runs := b.mathRun(style{size: th.BodySize}, f, false)
		if len(runs) != 1 || runs[0].Math == nil || len(b.warn) != 0 {
			t.Errorf("%q did not typeset: %v", f, b.warn)
		}
	}
}

// Typeset formulas sit on the text baseline: "x" has no depth, and a fraction
// straddles the baseline (its bar on the math axis, the denominator below).
// canvas's origin is the first glyph drawn, so without baseRef a fraction came
// out raised by its numerator.
func TestMathBaseline(t *testing.T) {
	th := theme(t, nil)
	b := &builder{t: th, links: map[*canvas.FontFace]string{}}
	x, _ := b.mathPath("x", false, 10)
	if y := x.Bounds().Y0; y < -0.1 || y > 0.1 {
		t.Errorf("x sits at %.2fmm from the baseline, want 0", y)
	}
	fr, _ := b.mathPath(`\frac{a+b}{c}`, false, 10)
	if bb := fr.Bounds(); bb.Y0 > -0.5 || bb.Y1 < 1.5 {
		t.Errorf("fraction y[%.2f,%.2f]: should extend below and well above the baseline", bb.Y0, bb.Y1)
	}
	if x0 := fr.Bounds().X0; x0 < -0.01 || x0 > 0.01 {
		t.Errorf("formula starts at x=%.2f, want 0 (reference dot removed)", x0)
	}
}

// A formula TeX can't set falls back to its source in code style, warns once,
// and writes nothing to stdout (which may be the PDF stream: inkcap - > out.pdf).
func TestMathFailureFallback(t *testing.T) {
	th := theme(t, nil)
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	d := parse(t, "Bad $\\begin{nonsense}x$ and again $\\begin{nonsense}x$.\n", th)
	os.Stdout = old
	w.Close()
	leaked, _ := io.ReadAll(r)
	if len(leaked) != 0 {
		t.Errorf("typesetting wrote %d bytes to stdout: %.80q", len(leaked), leaked)
	}
	n := 0
	for _, w := range d.Warn {
		if strings.Contains(w, "could not typeset") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want one warning for the repeated bad formula, got %v", d.Warn)
	}
	p := d.Items[0].B.(*Para)
	if !strings.Contains(plainText(p.Runs), `\begin{nonsense}x`) {
		t.Errorf("fallback text = %q", plainText(p.Runs))
	}
}

// document.math = false leaves dollar signs alone.
func TestMathDisabled(t *testing.T) {
	th := theme(t, func(c *config.Config) { c.Document.Math = false })
	d := parse(t, "Cost $x^2$ here.\n\n$$y$$\n", th)
	if in, disp := mathRuns(d.Items); in+disp != 0 {
		t.Errorf("math typeset with math = false")
	}
	if got := plainText(d.Items[0].B.(*Para).Runs); got != "Cost $x^2$ here." {
		t.Errorf("text = %q", got)
	}
}
