package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli runs the converter in an isolated temp dir with no user config.
func cli(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("INKCAP_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

func writeMD(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("# Title\n\n## One\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func isPDF(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no output: %v", err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatalf("%s is not a PDF", path)
	}
}

// Flags may follow the file name; the flag package alone stops at the first
// positional argument and turned `inkcap doc.md -toc` into a usage error.
func TestFlagsAfterFile(t *testing.T) {
	dir := t.TempDir()
	in := writeMD(t, dir, "doc.md")
	dst := filepath.Join(dir, "custom.pdf")
	code, out, errOut := cli(t, "", in, "-toc", "-o", dst)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	isPDF(t, dst)
	if strings.TrimSpace(out) != dst {
		t.Errorf("stdout = %q, want the output path %q", out, dst)
	}
}

// Everything after "--" is positional, so a file whose name starts with "-"
// can still be converted.
func TestDoubleDashEndsFlags(t *testing.T) {
	dir := t.TempDir()
	in := writeMD(t, dir, "-odd.md")
	t.Chdir(dir)
	code, _, errOut := cli(t, "", "-q", "--", "-odd.md")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	isPDF(t, strings.TrimSuffix(in, ".md")+".pdf")
}

func TestUsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no file":      {},
		"two files":    {"a.md", "b.md"},
		"unknown flag": {"-nope", "a.md"},
	} {
		if code, _, _ := cli(t, "", args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

// "-" reads markdown from stdin and, with no -o, writes the PDF to stdout
// (and does not print a path into the PDF stream).
func TestStdinToStdout(t *testing.T) {
	code, out, errOut := cli(t, "# Piped\n\nHello.\n", "-")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if !strings.HasPrefix(out, "%PDF-") {
		t.Fatalf("stdout is not a PDF: %.20q", out)
	}
	// -o with stdin input writes the file and prints its path.
	dst := filepath.Join(t.TempDir(), "piped.pdf")
	if code, out, errOut := cli(t, "# Piped\n", "-", "-o", dst); code != 0 || strings.TrimSpace(out) != dst {
		t.Fatalf("exit %d, stdout %q, stderr %s", code, out, errOut)
	}
	isPDF(t, dst)
}

// The output must never overwrite the input.
func TestRefusesToOverwriteInput(t *testing.T) {
	dir := t.TempDir()
	in := writeMD(t, dir, "notes.pdf") // a markdown file unluckily named .pdf
	before, _ := os.ReadFile(in)
	if code, _, errOut := cli(t, "", in); code != 1 || !strings.Contains(errOut, "overwrite the input") {
		t.Errorf("implicit dst == src: exit %d, stderr %q", code, errOut)
	}
	md := writeMD(t, dir, "doc.md")
	if code, _, errOut := cli(t, "", md, "-o", filepath.Join(dir, ".", "doc.md")); code != 1 || !strings.Contains(errOut, "overwrite the input") {
		t.Errorf("-o == input: exit %d, stderr %q", code, errOut)
	}
	if after, _ := os.ReadFile(in); !bytes.Equal(before, after) {
		t.Error("input was modified")
	}
}

// A render that fails part-way must leave neither a truncated file nor a temp
// file behind, and must not clobber a good PDF from an earlier run.
func TestWriteAtomicOnFailure(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.pdf")
	boom := errors.New("render failed")
	partial := func(w io.Writer) error {
		w.Write([]byte("%PDF-1.7 truncated"))
		return boom
	}

	if err := writeAtomic(dst, partial); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("failed render left %s behind", dst)
	}

	if err := os.WriteFile(dst, []byte("good"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = writeAtomic(dst, partial)
	if b, _ := os.ReadFile(dst); string(b) != "good" {
		t.Errorf("failed render clobbered the previous output: %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("temp files left behind: %v", ents)
	}

	if err := writeAtomic(dst, func(w io.Writer) error { _, err := w.Write([]byte("new")); return err }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Errorf("successful write: %q, want %q", b, "new")
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, want 0644", fi.Mode().Perm())
	}
}
