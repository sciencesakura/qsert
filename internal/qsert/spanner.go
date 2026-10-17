package qsert

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// gsqlQuoted scans a string literal or a quoted identifier of GoogleSQL starting at the opening quote, which may be
// triple-quoted with three single or double quotes. A quote is not escaped by doubling it, and a backslash escape is
// decoded unless raw is true.
func (lx *lexer) gsqlQuoted(raw bool) (string, error) {
	line := lx.line
	q := lx.src[lx.pos]
	end := string(q)
	if q != '`' && strings.HasPrefix(lx.src[lx.pos:], strings.Repeat(end, 3)) {
		end = strings.Repeat(end, 3)
	}
	lx.pos += len(end)
	var b strings.Builder
	for lx.pos < len(lx.src) {
		if strings.HasPrefix(lx.src[lx.pos:], end) {
			lx.pos += len(end)
			return b.String(), nil
		}
		c := lx.src[lx.pos]
		lx.pos++
		switch {
		case c == '\\' && lx.pos < len(lx.src):
			if raw {
				// A backslash in a raw string is kept, but it prevents the following quote from closing the string.
				b.WriteByte(c)
				c = lx.src[lx.pos]
				lx.pos++
			} else if err := lx.gsqlEscape(&b); err != nil {
				return "", err
			} else {
				continue
			}
		case c == '\n' && len(end) == 1:
			return "", &syntaxError{line, "unterminated quoted string"}
		}
		if c == '\n' {
			lx.line++
		}
		b.WriteByte(c)
	}
	return "", &syntaxError{line, "unterminated quoted string"}
}

// gsqlEscape decodes a backslash escape of GoogleSQL.
func (lx *lexer) gsqlEscape(b *strings.Builder) error {
	e := lx.src[lx.pos]
	lx.pos++
	if i := strings.IndexByte(`abfnrtv\?"'`+"`", e); i >= 0 {
		b.WriteByte("\a\b\f\n\r\t\v\\?\"'`"[i])
		return nil
	}
	n, base, unicode := 0, 16, false
	switch e {
	case '0', '1', '2', '3':
		lx.pos--
		n, base = 3, 8
	case 'x', 'X':
		n = 2
	case 'u':
		n, unicode = 4, true
	case 'U':
		n, unicode = 8, true
	}
	invalid := &syntaxError{lx.line, "illegal escape sequence"}
	if n == 0 || lx.pos+n > len(lx.src) {
		return invalid
	}
	v, err := strconv.ParseUint(lx.src[lx.pos:lx.pos+n], base, 32)
	if err != nil {
		return invalid
	}
	lx.pos += n
	if !unicode {
		b.WriteByte(byte(v))
		return nil
	}
	if !utf8.ValidRune(rune(v)) {
		return invalid
	}
	b.WriteRune(rune(v))
	return nil
}

// gsqlPrefixed scans a string literal with a prefix: raw r'...', bytes b'...', or raw bytes rb'...' and br'...'.
// The current position is at the opening quote.
func (lx *lexer) gsqlPrefixed(prefix string) (tokenKind, string, bool, error) {
	p := strings.ToLower(prefix)
	if p != "r" && p != "b" && p != "rb" && p != "br" {
		return 0, "", false, nil
	}
	s, err := lx.gsqlQuoted(strings.Contains(p, "r"))
	if strings.Contains(p, "b") {
		return tkBytes, s, true, err
	}
	return tkString, s, true, err
}

// spannerFloat formats a FLOAT64 as CAST(x AS STRING) of Spanner does: with 15 significant digits if they represent
// the number exactly, and otherwise 17.
func spannerFloat(f float64) string {
	if f == 0 {
		return "0"
	}
	s := strconv.FormatFloat(f, 'g', 15, 64)
	if g, _ := strconv.ParseFloat(s, 64); g != f {
		s = strconv.FormatFloat(f, 'g', 17, 64)
	}
	return s
}

// spannerJSONNumber returns the text of a number in JSON as Spanner stores it: an integer is a signed or unsigned
// 64-bit integer, and the others are doubles in the shortest representation, such as 1.5, 100.0 and 1e+15. A number
// that Spanner rejects is returned as it is.
func spannerJSONNumber(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			return strconv.FormatUint(n, 10)
		}
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return s
	}
	return spannerJSONDouble(f)
}

func spannerJSONDouble(f float64) string {
	neg, digits, exp := floatDigits(strconv.FormatFloat(f, 'e', -1, 64))
	if exp < -4 || exp >= 15 {
		return layoutFloat(neg, digits, exp, true, false)
	}
	return layoutFloat(neg, digits, exp, false, true)
}

// typedNode is a typed literal of GoogleSQL such as NUMERIC '1.5'.
type typedNode struct {
	typ string
	s   string
}

func (n *typedNode) eval(d Dialect) (value, error) {
	if !strings.EqualFold(n.typ, "NUMERIC") {
		return value{}, errUnsupported
	}
	r, ok := new(big.Rat).SetString(strings.TrimSpace(n.s))
	if !ok {
		return value{}, errUnsupported
	}
	// NUMERIC has 29 digits before the decimal point and 9 after it.
	x := decimal{new(big.Int).Mul(r.Num(), pow10(9)), 9}
	q, rem := new(big.Int).QuoRem(x.u, r.Denom(), new(big.Int))
	if rem.Sign() != 0 {
		return value{}, errUnsupported
	}
	return d.dec(decimal{q, 9}.normalize())
}

// spannerRound implements ROUND and TRUNC of Spanner. INT64 is converted to FLOAT64, which is rounded half away from
// zero. The rounding of FLOAT64 to decimal places is not evaluated, since it does not round the binary value as is.
func spannerRound(x value, places int64, hasPlaces, truncate bool) (value, error) {
	switch x.kind {
	case kindInt, kindFloat:
		if hasPlaces {
			return value{}, errUnsupported
		}
		if truncate {
			return dSpanner.float(math.Trunc(x.float()))
		}
		return dSpanner.float(math.Round(x.float()))
	case kindDecimal:
		return dSpanner.dec(x.n.roundTo(int(places), truncate))
	}
	return value{}, errUnsupported
}

// fnDiv implements DIV of INT64, which truncates the quotient toward zero.
func fnDiv(d Dialect, args []value) (value, error) {
	if args[0].kind != kindInt || args[1].kind != kindInt {
		return value{}, errUnsupported
	}
	return d.binary(opIntDiv, args[0], args[1])
}

// fnSpannerConcat implements CONCAT of GoogleSQL, whose arguments must be strings. It returns NULL if any argument
// is NULL.
func fnSpannerConcat(d Dialect, args []value) (value, error) {
	var b strings.Builder
	null := false
	for _, a := range args {
		switch a.kind {
		case kindNull:
			null = true
		case kindString:
			b.WriteString(a.s)
		default:
			return value{}, errUnsupported
		}
	}
	if null {
		return nullValue, nil
	}
	return d.str(b.String()), nil
}
