package pdfdoc

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Meta is document metadata read from front matter. Empty fields are unset.
type Meta struct {
	Title    string
	Author   string // several authors are joined with ", "
	Subject  string // `subject`, or `description` as a fallback
	Keywords string // several keywords are joined with ", "
	Date     string // kept as written; shown on the title page
	Subtitle string // title page only
	Logo     string // image path for the title page, relative to the document
}

// fmKeyRe matches a top-level YAML mapping key: "key:" at column zero.
var fmKeyRe = regexp.MustCompile(`^([A-Za-z_][\w-]*)[ \t]*:(?:[ \t]+(.*))?$`)

// splitFrontMatter separates a leading front matter block from the Markdown
// body. YAML is delimited by "---" and closed by "---" or "..."; TOML by "+++".
//
// To avoid swallowing a document that merely opens with a thematic break, a
// YAML block must follow Pandoc's rules: "---" is the very first line, the next
// line is not blank, and the block's first line is a "key:" mapping entry.
// Without a closing delimiter it isn't front matter. The returned body keeps
// the delimiter lines as blank lines, so any line-based diagnostics still point
// at the right place.
func splitFrontMatter(src []byte) (Meta, []byte, error) {
	s := strings.TrimPrefix(string(src), "\ufeff")
	lines := strings.SplitAfter(s, "\n")
	if len(lines) < 3 {
		return Meta{}, src, nil
	}
	open := strings.TrimRight(lines[0], " \t\r\n")
	var closers []string
	switch open {
	case "---":
		closers = []string{"---", "..."}
	case "+++":
		closers = []string{"+++"}
	default:
		return Meta{}, src, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t\r\n")
		for _, c := range closers {
			if l == c {
				end = i
				break
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return Meta{}, src, nil
	}
	block := strings.Join(lines[1:end], "")
	if open == "---" && !fmKeyRe.MatchString(strings.TrimRight(lines[1], " \t\r\n")) {
		return Meta{}, src, nil // a thematic break followed by ordinary Markdown
	}

	var meta Meta
	var err error
	if open == "+++" {
		meta, err = tomlMeta(block)
	} else {
		meta = yamlMeta(block)
	}
	if err != nil {
		return Meta{}, src, err
	}
	body := strings.Repeat("\n", end+1) + strings.Join(lines[end+1:], "")
	return meta, []byte(body), nil
}

// yamlMeta reads the handful of top-level keys inkcap uses from a YAML block,
// accepting the forms front matter actually uses: `key: value` (optionally
// quoted), inline lists `key: [a, b]`, and block lists of `- item` lines.
// Anything else (nested maps, multi-line scalars, unknown keys) is ignored; the
// whole block is stripped from the output either way.
func yamlMeta(block string) Meta {
	vals := map[string][]string{}
	var cur string // key whose block list we're reading
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := fmKeyRe.FindStringSubmatch(line); m != nil {
			cur = strings.ToLower(m[1])
			if v := strings.TrimSpace(stripYAMLComment(m[2])); v != "" {
				cur = ""
				key := strings.ToLower(m[1])
				if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
					for _, item := range strings.Split(v[1:len(v)-1], ",") {
						if item = unquote(strings.TrimSpace(item)); item != "" {
							vals[key] = append(vals[key], item)
						}
					}
				} else {
					vals[key] = []string{unquote(v)}
				}
			}
			continue
		}
		if t := strings.TrimSpace(line); cur != "" && strings.HasPrefix(t, "- ") {
			if item := unquote(strings.TrimSpace(stripYAMLComment(t[2:]))); item != "" {
				vals[cur] = append(vals[cur], item)
			}
		}
	}
	return metaFrom(vals)
}

// tomlMeta reads the same keys from a TOML (+++) block.
func tomlMeta(block string) (Meta, error) {
	var raw map[string]any
	if _, err := toml.Decode(block, &raw); err != nil {
		return Meta{}, fmt.Errorf("front matter: %w", err)
	}
	vals := map[string][]string{}
	for k, v := range raw {
		key := strings.ToLower(k)
		switch t := v.(type) {
		case []any:
			for _, item := range t {
				vals[key] = append(vals[key], fmt.Sprint(item))
			}
		case map[string]any:
			// nested tables carry nothing inkcap uses
		default:
			vals[key] = []string{fmt.Sprint(t)}
		}
	}
	return metaFrom(vals), nil
}

func metaFrom(vals map[string][]string) Meta {
	join := func(keys ...string) string {
		for _, k := range keys {
			if v := vals[k]; len(v) > 0 {
				return strings.Join(v, ", ")
			}
		}
		return ""
	}
	return Meta{
		Title:    join("title"),
		Author:   join("author", "authors"),
		Subject:  join("subject", "description"),
		Keywords: join("keywords", "tags"),
		Date:     join("date"),
		Subtitle: join("subtitle"),
		Logo:     join("logo"),
	}
}

// unquote strips one layer of matching single or double quotes. Inside double
// quotes, \" and \\ are unescaped; inside single quotes, ” is a literal '.
func unquote(s string) string {
	if len(s) >= 2 {
		switch {
		case s[0] == '"' && s[len(s)-1] == '"':
			return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s[1 : len(s)-1])
		case s[0] == '\'' && s[len(s)-1] == '\'':
			return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
		}
	}
	return s
}

// stripYAMLComment drops a trailing " # comment" outside quotes.
func stripYAMLComment(s string) string {
	inS, inD := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS:
			inD = !inD
		case c == '#' && !inS && !inD && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}
