package qsert

//go:generate go tool goyacc -l -o expr_parser.go -p expr -v "" expr.y

import (
	"math"
	"math/big"
	"strings"
)

// opTokens maps precedence levels to the tokens of the expression grammar.
var opTokens = [...]int{1: OP1, 2: OP2, 3: OP3, 4: OP4, 5: OP5, 6: OP6, 7: OP7}

// exprScanner feeds the tokens of a value expression to the expression parser.
type exprScanner struct {
	d      Dialect
	toks   []token
	pos    int
	prev   int // the previously returned token
	failed bool
	result exprNode
}

func (l *exprScanner) Lex(lval *exprSymType) int {
	id := l.lex(lval)
	l.prev = id
	return id
}

func (l *exprScanner) lex(lval *exprSymType) int {
	if l.pos >= len(l.toks) {
		return 0
	}
	t := l.toks[l.pos]
	l.pos++
	lval.tok = t
	switch t.kind {
	case tkNumber:
		return NUMBER
	case tkString:
		// A non-ASCII character of a string literal other than N'...' is converted to the code page of the
		// collation in SQL Server.
		if l.d == dMSSQL && !isASCII(t.val) && !strings.HasPrefix(strings.ToUpper(t.text), "N'") {
			return ILLEGAL
		}
		return l.stringToken()
	case tkDQString:
		if l.d == dMySQL || l.d == dSpanner {
			return l.stringToken()
		}
	case tkHex, tkBytes:
		return HEX
	case tkBit:
		return BIT
	case tkOctal:
		return OCTAL
	case tkLiteral:
		return LITERAL
	case tkIdent:
		w := strings.ToUpper(t.text)
		switch w {
		case "NULL":
			return NULL
		case "TRUE":
			return TRUE
		case "FALSE":
			return FALSE
		}
		// A typed literal of GoogleSQL: NUMERIC '1.5'
		if l.d == dSpanner && w == "NUMERIC" && l.peek().kind == tkString {
			return TYPED
		}
		if op, ok := operators[l.d][w]; ok && !l.peek().isOp("(") { // MySQL's DIV and MOD
			lval.tok.val = op.name
			return opTokens[op.level]
		}
		// A character set introducer such as _utf8mb4 'text' or _binary 0x00.
		if l.d == dMySQL && strings.HasPrefix(t.text, "_") {
			switch l.peek().kind {
			case tkString, tkDQString, tkHex, tkBit:
				return INTRODUCER
			}
		}
		return NAME
	case tkOp:
		switch t.text {
		case "(", ")", ",", "+", "-":
			return int(t.text[0])
		case "~":
			if l.d.bitwiseNot() {
				return '~'
			}
			return ILLEGAL
		}
		if op, ok := operators[l.d][t.text]; ok {
			lval.tok.val = op.name
			return opTokens[op.level]
		}
	}
	return ILLEGAL
}

// stringToken returns STRING, or ILLEGAL if the string follows another string and the dialect does not concatenate
// them.
func (l *exprScanner) stringToken() int {
	if l.prev == STRING && !l.d.adjacentStrings(l.toks[l.pos-1].line > l.toks[l.pos-2].endLine) {
		return ILLEGAL
	}
	return STRING
}

func (l *exprScanner) peek() token {
	if l.pos < len(l.toks) {
		return l.toks[l.pos]
	}
	return token{kind: tkEOF}
}

func (l *exprScanner) Error(string) { l.failed = true }

// evaluate parses and evaluates a value expression.
func evaluate(toks []token, d Dialect) (value, error) {
	l := &exprScanner{d: d, toks: toks}
	if exprParse(l) != 0 || l.failed {
		return value{}, errUnsupported
	}
	return l.result.eval(d)
}

type exprNode interface {
	eval(d Dialect) (value, error)
}

type valueNode struct{ v value }

func (n *valueNode) eval(Dialect) (value, error) { return n.v, nil }

type stringNode struct{ s string }

func (n *stringNode) eval(d Dialect) (value, error) { return d.str(n.s), nil }

type boolNode struct{ b bool }

func (n *boolNode) eval(d Dialect) (value, error) {
	switch d {
	case dMySQL, dSQLite:
		if n.b {
			return d.integer(big.NewInt(1), false)
		}
		return d.integer(big.NewInt(0), false)
	case dPostgres, dISO, dCassandra, dSpanner:
		return value{kind: kindBool, b: n.b}, nil
	}
	return value{}, errUnsupported
}

// numberNode is a numeric literal, whose type depends on the dialect.
type numberNode struct {
	text string
	neg  bool // negated by a unary minus
}

func (n *numberNode) eval(d Dialect) (value, error) {
	text := strings.ReplaceAll(n.text, "_", "")
	mant, exp, sci := strings.Cut(strings.ToLower(text), "e")
	x, ok := parseDecimal(mant)
	if !ok {
		return value{}, errUnsupported
	}
	if n.neg {
		x = x.neg()
		text = "-" + text
	}
	if d == dSpanner {
		// An integer literal is INT64, and the others are FLOAT64.
		if !sci && !strings.Contains(mant, ".") {
			return d.integer(x.u, false)
		}
		f, err := parseFloat(text)
		if err != nil {
			return value{}, err
		}
		return d.float(f)
	}
	if d == dCassandra {
		// Literals of CQL are untyped: an integer is of any integer type, and the others, which are of decimal, float
		// or double, are output as written.
		if !sci && !strings.Contains(mant, ".") {
			return value{kind: kindInt, n: x}, nil
		}
		return value{kind: kindVerbatim, s: text}, nil
	}
	switch {
	case !sci && !strings.Contains(mant, "."): // integer
		switch d {
		case dMySQL, dPostgres, dSQLite, dMSSQL:
			if v, err := d.integer(x.u, false); err == nil {
				return v, nil
			}
			if d == dPostgres {
				if v, err := d.integer(x.u, true); err == nil {
					return v, nil
				}
			}
			if d == dSQLite {
				return d.float(x.float())
			}
			return d.dec(x)
		case dISO:
			return value{kind: kindInt, n: x}, nil
		}
		return d.dec(x)
	case !sci: // decimal
		return d.dec(x)
	}
	// approximate-value literal
	switch d {
	case dMySQL, dSQLite:
		f, err := parseFloat(text)
		if err != nil {
			return value{}, err
		}
		return d.float(f)
	case dPostgres, dOracle:
		// PostgreSQL and Oracle treat it as an exact number.
		e, ok := new(big.Int).SetString(exp, 10)
		if !ok || !e.IsInt64() || e.Int64() < -1000 || e.Int64() > 1000 {
			return value{}, errUnsupported
		}
		x.scale -= int(e.Int64())
		if x.scale < 0 {
			x = decimal{new(big.Int).Mul(x.u, pow10(-x.scale)), 0}
		}
		return d.dec(x)
	}
	return value{}, errUnsupported
}

// radixNode is a hexadecimal, binary or octal literal. 0x41 is an integer in PostgreSQL and SQLite, and a binary
// string in the others, which is output as written.
type radixNode struct {
	text string
	base int
}

func (n *radixNode) eval(d Dialect) (value, error) {
	text := n.text
	if d == dCassandra {
		// Cassandra outputs a blob in lower case.
		text = strings.ToLower(text)
	}
	if !strings.HasPrefix(text, "0") || d != dPostgres && d != dSpanner && (d != dSQLite || n.base != 16) {
		return value{kind: kindBinary, s: text}, nil
	}
	z, ok := new(big.Int).SetString(strings.ReplaceAll(text[2:], "_", ""), n.base)
	if !ok {
		return value{}, errUnsupported
	}
	if d == dSpanner && n.base != 16 {
		return value{}, errUnsupported
	}
	if d == dSpanner {
		// A hexadecimal literal is INT64.
		return d.integer(z, false)
	}
	if d == dSQLite {
		// SQLite interprets up to 16 hexadecimal digits as a two's complement 64-bit integer.
		if z.BitLen() > 64 {
			return value{}, errUnsupported
		}
		return d.integer(big.NewInt(int64(z.Uint64())), false)
	}
	if v, err := d.integer(z, false); err == nil {
		return v, nil
	}
	if v, err := d.integer(z, true); err == nil {
		return v, nil
	}
	return d.dec(decimal{z, 0})
}

type unaryNode struct {
	op string
	x  exprNode
}

func (n *unaryNode) eval(d Dialect) (value, error) {
	// A negative number literal is typed with its sign: -9223372036854775808 is a BIGINT, not an overflow.
	if lit, ok := n.x.(*literalNode); ok && n.op == "-" {
		// a negative duration of CQL: -1h30m
		if dur, ok := parseCQLDuration(lit.text); ok {
			dur.neg = true
			return value{kind: kindVerbatim, s: dur.String()}, nil
		}
	}
	if lit, ok := unparen(n.x).(*numberNode); ok && n.op == "-" {
		switch d {
		case dMySQL, dPostgres, dSQLite, dCassandra, dSpanner:
			return (&numberNode{text: lit.text, neg: !lit.neg}).eval(d)
		case dMSSQL:
			// SQL Server types -2147483648 as an int, but treats it inconsistently in the following operations.
			if strings.TrimLeft(lit.text, "0") == "2147483648" {
				return value{}, errUnsupported
			}
		}
	}
	if _, ok := n.x.(*unaryNode); ok && d == dOracle {
		// Oracle does not allow a sign following a sign: - -1.
		return value{}, errUnsupported
	}
	x, err := operand(n.x, d)
	if err != nil {
		return x, err
	}
	return d.unary(n.op, x)
}

type binaryNode struct {
	op   string
	l, r exprNode
}

func (n *binaryNode) eval(d Dialect) (value, error) {
	l, err := operand(n.l, d)
	if err != nil {
		return l, err
	}
	r, err := operand(n.r, d)
	if err != nil {
		return r, err
	}
	return d.binary(n.op, l, r)
}

// operand evaluates an operand of an operator or a function.
func operand(n exprNode, d Dialect) (value, error) {
	v, err := n.eval(d)
	if err == nil && v.rounded {
		return value{}, errUnsupported
	}
	return v, err
}

type callNode struct {
	name string
	args []exprNode
}

func (n *callNode) eval(d Dialect) (value, error) {
	f, ok := functions[d][strings.ToUpper(n.name)]
	if !ok || len(n.args) < f.minArgs || f.maxArgs >= 0 && len(n.args) > f.maxArgs {
		return value{}, errUnsupported
	}
	args := make([]value, len(n.args))
	for i, a := range n.args {
		v, err := operand(a, d)
		if err != nil {
			return v, err
		}
		if v.kind == kindNull && !f.handlesNull {
			return nullValue, nil
		}
		args[i] = v
	}
	return f.call(d, args)
}

func parseFloat(s string) (float64, error) {
	f, _, err := big.ParseFloat(s, 10, 53, big.ToNearestEven)
	if err != nil {
		return 0, errUnsupported
	}
	r, _ := f.Float64()
	if math.IsInf(r, 0) {
		return 0, errUnsupported
	}
	return r, nil
}

// parenNode is a parenthesized expression.
type parenNode struct{ x exprNode }

func (n *parenNode) eval(d Dialect) (value, error) { return n.x.eval(d) }

// unparen removes the parentheses around an expression.
func unparen(n exprNode) exprNode {
	for {
		p, ok := n.(*parenNode)
		if !ok {
			return n
		}
		n = p.x
	}
}
