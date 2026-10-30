package config

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// parseDotenv parses .env syntax:
//
//	KEY=value              unquoted; trailing " # comment" is stripped
//	export KEY=value       "export " prefix is ignored
//	KEY='literal'          no escapes
//	KEY="line\nnext"       \n \r \t \" \\ escapes; may span lines
//	# comment
//
// There is no variable expansion: "$OTHER" stays literal (FIXED).
func parseDotenv(r io.Reader, file string) (map[string]string, []Problem) {
	vals := map[string]string{}
	var problems []Problem
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		startLine := lineNo
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, rest, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validKey(key) {
			problems = append(problems, Problem{fmt.Sprintf("%s:%d", file, startLine), "expected KEY=value"})
			continue
		}
		rest = strings.TrimLeft(rest, " \t")

		var val string
		var err error
		switch {
		case strings.HasPrefix(rest, `"`):
			// Double-quoted values may continue on following lines.
			for {
				val, err = parseDoubleQuoted(rest)
				if err != errUnterminated || !sc.Scan() {
					break
				}
				lineNo++
				rest += "\n" + sc.Text()
			}
		case strings.HasPrefix(rest, "'"):
			end := strings.IndexByte(rest[1:], '\'')
			if end < 0 {
				err = errUnterminated
			} else {
				val, err = rest[1:1+end], checkTrailing(rest[2+end:])
			}
		default:
			val = rest
			if i := strings.Index(val, " #"); i >= 0 {
				val = val[:i]
			}
			val = strings.TrimSpace(val)
		}
		if err != nil {
			problems = append(problems, Problem{fmt.Sprintf("%s:%d", file, startLine), err.Error()})
			continue
		}
		vals[key] = val
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, Problem{file, err.Error()})
	}
	return vals, problems
}

type parseError string

func (e parseError) Error() string { return string(e) }

const errUnterminated = parseError("unterminated quoted value")

func parseDoubleQuoted(s string) (string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b.String(), checkTrailing(s[i+1:])
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"', '\\':
				b.WriteByte(s[i])
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", errUnterminated
}

// checkTrailing allows only whitespace and a comment after a quoted value.
func checkTrailing(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return nil
	}
	return parseError("unexpected text after quoted value")
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		switch {
		case c == '_', 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z':
		case '0' <= c && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
