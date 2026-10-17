package qsert

import "strings"

// array is an array value: an array constructor ARRAY[...] of standard SQL and PostgreSQL, an array literal
// '{...}' of PostgreSQL, a JSON array, or a list, set or tuple of CQL.
type array struct {
	elems     []cell
	unordered bool // the order of the elements is unknown, such as of a set of UUIDs in CQL
}

// arrayOf returns the array that a value expression represents, or nil if it is not an array.
// If nested is true, the expression is an element of an array constructor, in which a bare [...] is an array.
func arrayOf(toks []token, d Dialect, nested bool) *array {
	if d != dISO && d != dPostgres && d != dSpanner {
		return nil
	}
	if inner, typ, ok := stripCast(toks); ok {
		if !isArrayType(typ) {
			return nil
		}
		toks = inner
	}
	if len(toks) > 0 && toks[0].isWord("ARRAY") {
		toks = toks[1:]
		if d == dSpanner {
			// GoogleSQL: ARRAY<INT64>[1, 2]
			toks = skipTypeParameters(toks)
		}
		if elems, ok := enclosed(toks, 0, "["); ok {
			return arrayConstructor(elems, d)
		}
		return nil
	}
	// A bare [...] is an array in GoogleSQL, and in an array constructor of the others.
	if elems, ok := enclosed(toks, 0, "["); ok && (nested || d == dSpanner) {
		return arrayConstructor(elems, d)
	}
	if d == dPostgres && len(toks) == 1 && toks[0].kind == tkString {
		return pgArrayLiteral(toks[0].val)
	}
	return nil
}

// skipTypeParameters removes the type parameters <...> at the beginning of toks.
func skipTypeParameters(toks []token) []token {
	if len(toks) == 0 || !toks[0].isOp("<") {
		return toks
	}
	depth := 0
	for i, t := range toks {
		switch {
		case t.isOp("<"):
			depth++
		case t.isOp(">"):
			depth--
		case t.isOp(">>"):
			depth -= 2 // ARRAY<STRUCT<x INT64>>
		}
		if depth <= 0 {
			return toks[i+1:]
		}
	}
	return nil
}

// isArrayType reports whether a type name is an array type, such as int[], int[3], int ARRAY or ARRAY<INT64>.
func isArrayType(toks []token) bool {
	n := len(toks)
	return n > 0 && (toks[n-1].isWord("ARRAY") || toks[n-1].isOp("]") || toks[0].isWord("ARRAY"))
}

// arrayConstructor returns the array of the comma-separated elements of an array constructor.
func arrayConstructor(toks []token, d Dialect) *array {
	items, ok := splitList(toks)
	if !ok {
		return nil
	}
	a := &array{}
	for _, item := range items {
		a.elems = append(a.elems, newCell(item, d, true))
	}
	return a
}

// pgArrayLiteral parses the text representation of a PostgreSQL array such as {1,"a b",NULL,{2,3}}.
func pgArrayLiteral(s string) *array {
	p := &pgArrayParser{s: s}
	p.skipSpace()
	a, ok := p.array()
	if !ok {
		return nil
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		return nil
	}
	return a
}

type pgArrayParser struct {
	s   string
	pos int
}

func (p *pgArrayParser) skipSpace() {
	for p.pos < len(p.s) && strings.IndexByte(" \t\n\r\v\f", p.s[p.pos]) >= 0 {
		p.pos++
	}
}

func (p *pgArrayParser) array() (*array, bool) {
	if p.pos >= len(p.s) || p.s[p.pos] != '{' {
		return nil, false
	}
	p.pos++
	a := &array{}
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return a, true
	}
	for {
		p.skipSpace()
		c, ok := p.element()
		if !ok {
			return nil, false
		}
		a.elems = append(a.elems, c)
		p.skipSpace()
		if p.pos >= len(p.s) {
			return nil, false
		}
		p.pos++
		switch p.s[p.pos-1] {
		case ',':
		case '}':
			return a, true
		default:
			return nil, false
		}
	}
}

func (p *pgArrayParser) element() (cell, bool) {
	if p.pos >= len(p.s) {
		return cell{}, false
	}
	start := p.pos
	switch p.s[p.pos] {
	case '{':
		a, ok := p.array()
		return cell{text: p.s[start:p.pos], array: a}, ok
	case '"':
		var b strings.Builder
		for p.pos++; p.pos < len(p.s); p.pos++ {
			switch c := p.s[p.pos]; c {
			case '"':
				p.pos++
				return leafCell(b.String()), true
			case '\\':
				if p.pos++; p.pos >= len(p.s) {
					return cell{}, false
				}
				b.WriteByte(p.s[p.pos])
			default:
				b.WriteByte(c)
			}
		}
		return cell{}, false
	}
	// An unquoted element ends with a comma or a closing brace, and its surrounding whitespace is ignored.
	var b strings.Builder
	trailing := 0 // number of trailing unescaped whitespace bytes in b
	escaped := false
	for ; p.pos < len(p.s); p.pos++ {
		c := p.s[p.pos]
		switch {
		case c == ',' || c == '}':
			s := b.String()
			s = s[:len(s)-trailing]
			if s == "" {
				return cell{}, false
			}
			if !escaped && strings.EqualFold(s, "NULL") {
				return cell{null: true}, true
			}
			return leafCell(s), true
		case c == '"' || c == '{':
			return cell{}, false
		case c == '\\':
			if p.pos++; p.pos >= len(p.s) {
				return cell{}, false
			}
			b.WriteByte(p.s[p.pos])
			escaped, trailing = true, 0
		case strings.IndexByte(" \t\n\r\v\f", c) >= 0:
			b.WriteByte(c)
			trailing++
		default:
			b.WriteByte(c)
			trailing = 0
		}
	}
	return cell{}, false
}

// leafCell returns a non-NULL element of an array literal of PostgreSQL, which may be JSON if the array is of a JSON
// type.
func leafCell(s string) cell {
	return cell{text: s, json: &jsonText{s, dPostgres}}
}
