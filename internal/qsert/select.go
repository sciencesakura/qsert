package qsert

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Selector selects a column by -e, such as .column, .table.column, .column[0] or .column["name"].
type Selector struct {
	table  string // lower-cased qualified table name, or "" for any table
	column string // lower-cased column name
	path   []step // path to an element of an array or JSON
	header string // header of the output column: the selector without the leading dot
}

// Header returns the header of the output column, which is the selector without the leading dot.
func (s Selector) Header() string { return s.header }

// ParseSelectors parses a comma-separated list of selectors. A selector is a sequence of names each preceded by a
// dot, whose last name is a column and the others are a (qualified) table, optionally followed by a path to an
// element such as [0][-1]["name"]. A name can be quoted with " or `, in which the quote is escaped by doubling it.
// A member name of JSON is a JSON string literal.
func ParseSelectors(s string) ([]Selector, error) {
	var sels []Selector
	p := &selectorParser{s: s}
	for {
		p.skipSpace()
		var names []string
		for p.peek() == '.' {
			p.pos++
			name, err := p.name()
			if err != nil {
				return nil, err
			}
			names = append(names, name)
		}
		if len(names) == 0 {
			return nil, p.errorf("expected '.'")
		}
		var path []step
		for p.peek() == '[' {
			s, err := p.step()
			if err != nil {
				return nil, err
			}
			path = append(path, s)
		}
		sels = append(sels, newSelector(names, path))
		p.skipSpace()
		if p.pos == len(p.s) {
			return sels, nil
		}
		if p.peek() != ',' {
			return nil, p.errorf("expected ',', '.' or '['")
		}
		p.pos++
	}
}

func newSelector(names []string, path []step) Selector {
	headers := make([]string, len(names))
	for i, n := range names {
		headers[i] = n
		if !isBareName(n) {
			headers[i] = `"` + strings.ReplaceAll(n, `"`, `""`) + `"`
		}
	}
	header := strings.Join(headers, ".")
	for _, s := range path {
		header += s.String()
	}
	column := names[len(names)-1]
	table := names[:len(names)-1]
	return Selector{
		table:  strings.ToLower(strings.Join(table, ".")),
		column: strings.ToLower(column),
		path:   path,
		header: header,
	}
}

// step parses an index or a member name in brackets such as [0], [-1] or ["name"].
func (p *selectorParser) step() (step, error) {
	p.pos++ // [
	p.skipSpace()
	var s step
	start := p.pos
	if p.peek() == '"' {
		for p.pos++; p.pos < len(p.s) && p.s[p.pos] != '"'; p.pos++ {
			if p.s[p.pos] == '\\' {
				p.pos++
			}
		}
		p.pos++
		if p.pos > len(p.s) || json.Unmarshal([]byte(p.s[start:p.pos]), &s.key) != nil {
			p.pos = start
			return step{}, p.errorf("invalid member name")
		}
		s.member = true
	} else {
		if p.peek() == '-' {
			p.pos++
		}
		for isDigit(p.peek()) {
			p.pos++
		}
		var err error
		if s.index, err = strconv.Atoi(p.s[start:p.pos]); err != nil {
			p.pos = start
			return step{}, p.errorf("expected an integer index or a member name")
		}
	}
	p.skipSpace()
	if p.peek() != ']' {
		return step{}, p.errorf("expected ']'")
	}
	p.pos++
	return s, nil
}

type selectorParser struct {
	s   string
	pos int
}

func (p *selectorParser) peek() byte {
	if p.pos < len(p.s) {
		return p.s[p.pos]
	}
	return 0
}

func (p *selectorParser) skipSpace() {
	for p.pos < len(p.s) && strings.IndexByte(" \t\r\n", p.s[p.pos]) >= 0 {
		p.pos++
	}
}

func (p *selectorParser) name() (string, error) {
	start := p.pos
	switch q := p.peek(); q {
	case '"', '`':
		var b strings.Builder
		for p.pos++; p.pos < len(p.s); p.pos++ {
			if c := p.s[p.pos]; c != q {
				b.WriteByte(c)
			} else if p.pos+1 < len(p.s) && p.s[p.pos+1] == q {
				b.WriteByte(c)
				p.pos++
			} else {
				p.pos++
				return b.String(), nil
			}
		}
		p.pos = start
		return "", p.errorf("unterminated quoted name")
	}
	for p.pos < len(p.s) {
		r, n := utf8.DecodeRuneInString(p.s[p.pos:])
		if !isNameRune(r) {
			break
		}
		p.pos += n
	}
	if p.pos == start {
		return "", p.errorf("expected a name")
	}
	return p.s[start:p.pos], nil
}

func (p *selectorParser) errorf(format string, a ...any) error {
	return fmt.Errorf("invalid selector at position %d: %s", p.pos+1, fmt.Sprintf(format, a...))
}

func isNameRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isBareName(s string) bool {
	for _, r := range s {
		if !isNameRune(r) {
			return false
		}
	}
	return s != ""
}

// columnOf returns the index of the column of a table that the selector selects, or -1 if none.
func (s Selector) columnOf(t *table) (int, bool) {
	if s.table != "" && s.table != t.key {
		return -1, false
	}
	if j, ok := t.index[s.column]; ok {
		return j, true
	}
	return -1, false
}
