package qsert

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type tokenKind int

const (
	tkEOF         tokenKind = iota
	tkDelim                 // statement delimiter
	tkIdent                 // unquoted identifier or keyword
	tkQuotedIdent           // `ident` or [ident]
	tkString                // 'string', N'string', E'string' or $$string$$
	tkDQString              // "string" (a string in MySQL, an identifier in the others)
	tkNumber                // 123, 1.5, 1e3
	tkHex                   // 0x1F, X'1F'
	tkBit                   // 0b01, B'01'
	tkOctal                 // 0o17
	tkVar                   // @var, @@var, ?, $1
	tkOp                    // operators and punctuation
	tkLiteral               // a literal output as written: a UUID or a duration of CQL
	tkBytes                 // a bytes literal of GoogleSQL: b'...', output as written
)

type token struct {
	kind    tokenKind
	text    string // source text
	val     string // decoded content of quoted strings and identifiers
	line    int    // line where the token starts
	endLine int    // line where the token ends
}

func (t token) isWord(w string) bool { return t.kind == tkIdent && strings.EqualFold(t.text, w) }

func (t token) isOp(op string) bool { return t.kind == tkOp && t.text == op }

// multiCharOps lists multi-character operators, longest first.
var multiCharOps = []string{"<=>", "->>", "<=", ">=", "<>", "!=", "<<", ">>", "||", "&&", ":=", "->", "::"}

// pgOpChars are the characters of which PostgreSQL operators consist.
const pgOpChars = "+-*/<>=~!@#%^&|`?"

type syntaxError struct {
	line int
	msg  string
}

func (e *syntaxError) Error() string { return fmt.Sprintf("%d: %s", e.line, e.msg) }

type lexer struct {
	src       string
	pos       int
	line      int
	d         Dialect
	delim     string
	backslash bool   // whether backslash escapes are recognized in string literals
	stmtStart bool   // whether no token has been read since the last delimiter
	inExec    bool   // whether in a MySQL executable comment /*! ... */
	pending   *token // the first token of the next statement, which has been read
}

func newLexer(src string, d Dialect, backslash bool) *lexer {
	src = strings.TrimPrefix(src, "\xef\xbb\xbf") // UTF-8 BOM
	return &lexer{src: src, line: 1, d: d, delim: ";", backslash: backslash && d == dMySQL, stmtStart: true}
}

// statement returns the tokens of the next statement, excluding the delimiter.
// eof reports whether the end of the input has been reached.
func (lx *lexer) statement() (toks []token, eof bool, err error) {
	depth := 0
	for {
		var t token
		if lx.pending != nil {
			t, lx.pending = *lx.pending, nil
		} else if t, err = lx.next(); err != nil {
			return nil, false, err
		}
		switch {
		case t.kind == tkEOF:
			return toks, true, nil
		case t.kind == tkDelim:
			return toks, false, nil
		case depth == 0 && lx.d == dMSSQL && len(toks) > 0 && startsStatement(toks, t):
			// Statements of T-SQL need not be terminated by semicolons.
			lx.pending = &t
			return toks, false, nil
		case isOpening(t):
			depth++
		case isClosing(t):
			depth--
		}
		toks = append(toks, t)
	}
}

// startsStatement reports whether a token at the top level begins a new T-SQL statement following the tokens of the
// current statement: CREATE or ALTER, INSERT unless it is a part of the statement such as in CREATE TRIGGER and GRANT
// INSERT, and any token following the rows of INSERT ... VALUES or INSERT ... DEFAULT VALUES.
func startsStatement(toks []token, t token) bool {
	first, prev := toks[0], toks[len(toks)-1]
	switch {
	case t.isWord("CREATE") || t.isWord("ALTER"):
		return true
	case t.isWord("INSERT"):
		return !first.isWord("CREATE") && !first.isWord("ALTER") && !first.isWord("MERGE") &&
			!prev.isOp(",") && !prev.isWord("GRANT") && !prev.isWord("REVOKE") && !prev.isWord("DENY")
	case !first.isWord("INSERT"):
		return false
	case prev.isWord("VALUES"):
		return len(toks) >= 2 && toks[len(toks)-2].isWord("DEFAULT")
	}
	// Every closing parenthesis at the top level after VALUES ends a row.
	return prev.isOp(")") && !t.isOp(",") && findTopLevel(toks, func(i int) bool { return toks[i].isWord("VALUES") }) >= 0
}

func (lx *lexer) next() (token, error) {
	if err := lx.skip(); err != nil {
		return token{}, err
	}
	t, err := lx.scan()
	t.endLine = lx.line
	lx.stmtStart = t.kind == tkDelim
	return t, err
}

func (lx *lexer) scan() (token, error) {
	t := token{line: lx.line}
	if lx.pos >= len(lx.src) {
		t.kind = tkEOF
		return t, nil
	}
	start := lx.pos
	rest := lx.src[lx.pos:]
	var err error
	c := rest[0]
	switch {
	case strings.HasPrefix(rest, lx.delim):
		lx.pos += len(lx.delim)
		t.kind = tkDelim
	case lx.d == dMSSQL && lx.batchSeparator():
		lx.pos = lx.lineEnd()
		t.kind = tkDelim
	case lx.d == dSpanner && (c == '\'' || c == '"' || c == '`'):
		t.kind = tkString
		if c == '`' {
			t.kind = tkQuotedIdent
		}
		t.val, err = lx.gsqlQuoted(false)
	case c == '\'':
		t.kind = tkString
		t.val, err = lx.quoted('\'', lx.stringEscapes())
	case c == '"':
		t.kind = tkDQString
		t.val, err = lx.quoted('"', lx.stringEscapes())
	case c == '`':
		t.kind = tkQuotedIdent
		t.val, err = lx.quoted('`', nil)
	case c == '[' && lx.d.bracketIdentifiers():
		t.kind = tkQuotedIdent
		t.val, err = lx.quoted(']', nil)
	case lx.d == dCassandra && cqlLiteralLen(rest) > 0:
		t.kind = tkLiteral
		lx.pos += cqlLiteralLen(rest)
	case isDigit(c) || c == '.' && len(rest) > 1 && isDigit(rest[1]):
		t.kind = lx.number()
	case c == '@' && lx.d != dPostgres:
		t.kind = tkVar
		err = lx.variable()
	case c == '?' && lx.d != dPostgres:
		t.kind = tkVar
		lx.pos++
	case c == '$' && len(rest) > 1 && isDigit(rest[1]):
		t.kind = tkVar
		lx.pos++
		lx.scanWhile(isDigit)
	case c == '$' && (lx.d == dPostgres && lx.dollarTag() != "" || lx.d == dCassandra && strings.HasPrefix(rest, "$$")):
		t.kind = tkString
		t.val, err = lx.dollarQuoted()
	case isIdentStart(c):
		t.kind, t.val, err = lx.word()
	case lx.d == dPostgres && strings.IndexByte(pgOpChars, c) >= 0:
		t.kind = tkOp
		lx.pos += pgOperatorLen(rest)
	default:
		t.kind = tkOp
		lx.pos++
		for _, op := range multiCharOps {
			if strings.HasPrefix(rest, op) {
				lx.pos = start + len(op)
				break
			}
		}
	}
	t.text = lx.src[start:lx.pos]
	return t, err
}

// batchSeparator reports whether the current line is the batch separator GO of SQL Server tools, optionally
// followed by a count.
func (lx *lexer) batchSeparator() bool {
	start := strings.LastIndexByte(lx.src[:lx.pos], '\n') + 1
	if strings.TrimSpace(lx.src[start:lx.pos]) != "" {
		return false
	}
	line := strings.Fields(lx.src[lx.pos:lx.lineEnd()])
	switch {
	case len(line) == 0 || !strings.EqualFold(line[0], "GO"):
		return false
	case len(line) == 1:
		return true
	}
	_, err := strconv.ParseUint(line[1], 10, 32)
	return len(line) == 2 && err == nil
}

// pgOperatorLen returns the length of the PostgreSQL operator at the beginning of s.
func pgOperatorLen(s string) int {
	n := 0
	for n < len(s) && strings.IndexByte(pgOpChars, s[n]) >= 0 {
		n++
	}
	op := s[:n]
	for _, c := range []string{"/*", "--"} {
		if i := strings.Index(op, c); i > 0 {
			op = op[:i]
		}
	}
	// A multi-character operator cannot end in + or - unless it contains one of ~!@#%^&|`?.
	if len(op) > 1 && strings.ContainsAny(op[len(op)-1:], "+-") && !strings.ContainsAny(op, "~!@#%^&|`?") {
		for len(op) > 1 && strings.ContainsAny(op[len(op)-1:], "+-") {
			op = op[:len(op)-1]
		}
	}
	return len(op)
}

// skip skips whitespace, comments and DELIMITER commands.
func (lx *lexer) skip() error {
	for lx.pos < len(lx.src) {
		rest := lx.src[lx.pos:]
		c := rest[0]
		switch {
		case c == '\n':
			lx.line++
			lx.pos++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			lx.pos++
		case c == '#' && (lx.d == dMySQL || lx.d == dSpanner):
			lx.skipLine()
		case lx.d == dCassandra && strings.HasPrefix(rest, "//"):
			lx.skipLine()
		case strings.HasPrefix(rest, "--") && (lx.d != dMySQL || len(rest) == 2 || rest[2] <= ' '):
			lx.skipLine()
		case lx.d == dMySQL && strings.HasPrefix(rest, "/*!"):
			// The content of an executable comment is SQL to MySQL.
			lx.pos += 3
			lx.scanWhile(isDigit)
			lx.inExec = true
		case lx.inExec && strings.HasPrefix(rest, "*/"):
			lx.pos += 2
			lx.inExec = false
		case strings.HasPrefix(rest, "/*"):
			if err := lx.blockComment(); err != nil {
				return err
			}
		case lx.stmtStart && len(rest) > 9 && strings.EqualFold(rest[:9], "delimiter") &&
			(rest[9] == ' ' || rest[9] == '\t'):
			lx.pos += 9
			end := lx.lineEnd()
			fields := strings.Fields(lx.src[lx.pos:end])
			if len(fields) == 0 {
				return &syntaxError{lx.line, "DELIMITER requires an argument"}
			}
			lx.delim = fields[0]
			lx.pos = end
		default:
			return nil
		}
	}
	return nil
}

func (lx *lexer) blockComment() error {
	line := lx.line
	depth := 0
	for lx.pos < len(lx.src) {
		rest := lx.src[lx.pos:]
		switch {
		case strings.HasPrefix(rest, "/*") && (depth == 0 || lx.d.nestedComments()):
			depth++
			lx.pos += 2
		case strings.HasPrefix(rest, "*/"):
			lx.pos += 2
			if depth--; depth == 0 {
				return nil
			}
		default:
			if rest[0] == '\n' {
				lx.line++
			}
			lx.pos++
		}
	}
	return &syntaxError{line, "unterminated comment"}
}

func (lx *lexer) lineEnd() int {
	if i := strings.IndexByte(lx.src[lx.pos:], '\n'); i >= 0 {
		return lx.pos + i
	}
	return len(lx.src)
}

func (lx *lexer) skipLine() { lx.pos = lx.lineEnd() }

func (lx *lexer) scanWhile(f func(byte) bool) {
	for lx.pos < len(lx.src) && f(lx.src[lx.pos]) {
		lx.pos++
	}
}

// quoted scans a quoted string or identifier starting at the opening quote and returns its decoded content.
// A doubled closing quote stands for the quote itself. If unescape is not nil, it decodes a backslash escape, whose
// backslash has been read.
func (lx *lexer) quoted(closing byte, unescape func(*strings.Builder) error) (string, error) {
	line := lx.line
	lx.pos++
	var b strings.Builder
	for lx.pos < len(lx.src) {
		c := lx.src[lx.pos]
		lx.pos++
		switch {
		case c == closing:
			if lx.pos < len(lx.src) && lx.src[lx.pos] == closing {
				lx.pos++
				b.WriteByte(c)
				continue
			}
			return b.String(), nil
		case c == '\\' && unescape != nil && lx.pos < len(lx.src):
			if err := unescape(&b); err != nil {
				return "", err
			}
			continue
		case c == '\n':
			lx.line++
		}
		b.WriteByte(c)
	}
	return "", &syntaxError{line, "unterminated quoted string"}
}

// stringEscapes returns the decoder of backslash escapes in ordinary string literals, or nil if they are not escapes.
func (lx *lexer) stringEscapes() func(*strings.Builder) error {
	if lx.backslash {
		return lx.mysqlEscape
	}
	return nil
}

// mysqlEscape decodes a backslash escape of MySQL.
func (lx *lexer) mysqlEscape(b *strings.Builder) error {
	e := lx.src[lx.pos]
	lx.pos++
	switch e {
	case '0':
		b.WriteByte(0)
	case 'b':
		b.WriteByte('\b')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'Z':
		b.WriteByte(0x1a)
	case '%', '_':
		// \% and \_ are kept for LIKE patterns.
		b.WriteByte('\\')
		b.WriteByte(e)
	default:
		lx.escapedByte(b, e)
	}
	return nil
}

// pgEscape decodes a backslash escape of PostgreSQL's escape string E'...'.
func (lx *lexer) pgEscape(b *strings.Builder) error {
	e := lx.src[lx.pos]
	lx.pos++
	switch e {
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case '0', '1', '2', '3', '4', '5', '6', '7':
		lx.pos--
		v, _ := strconv.ParseUint(lx.digits(3, isOctalDigit), 8, 32)
		b.WriteByte(byte(v))
	case 'x':
		if s := lx.digits(2, isHexDigit); s != "" {
			v, _ := strconv.ParseUint(s, 16, 8)
			b.WriteByte(byte(v))
		} else {
			b.WriteByte(e)
		}
	case 'u', 'U':
		n := 4
		if e == 'U' {
			n = 8
		}
		if lx.pos+n > len(lx.src) {
			return &syntaxError{lx.line, "invalid Unicode escape"}
		}
		r, err := strconv.ParseUint(lx.src[lx.pos:lx.pos+n], 16, 32)
		if err != nil || !utf8.ValidRune(rune(r)) {
			return &syntaxError{lx.line, "invalid Unicode escape"}
		}
		lx.pos += n
		b.WriteRune(rune(r))
	default:
		lx.escapedByte(b, e)
	}
	return nil
}

// escapedByte writes a character escaped by a backslash as it is.
func (lx *lexer) escapedByte(b *strings.Builder, e byte) {
	if e == '\n' {
		lx.line++
	}
	b.WriteByte(e)
}

// digits reads up to n digits.
func (lx *lexer) digits(n int, isDigit func(byte) bool) string {
	start := lx.pos
	for lx.pos < len(lx.src) && lx.pos-start < n && isDigit(lx.src[lx.pos]) {
		lx.pos++
	}
	return lx.src[start:lx.pos]
}

// dollarTag returns the opening tag of a PostgreSQL dollar-quoted string at the current position, or "" if none.
func (lx *lexer) dollarTag() string {
	rest := lx.src[lx.pos:]
	i := 1
	if i < len(rest) && isIdentStart(rest[i]) {
		for i < len(rest) && isIdentChar(rest[i]) && rest[i] != '$' {
			i++
		}
	}
	if i < len(rest) && rest[i] == '$' {
		return rest[:i+1]
	}
	return ""
}

func (lx *lexer) dollarQuoted() (string, error) {
	tag := lx.dollarTag()
	lx.pos += len(tag)
	end := strings.Index(lx.src[lx.pos:], tag)
	if end < 0 {
		return "", &syntaxError{lx.line, "unterminated dollar-quoted string"}
	}
	s := lx.src[lx.pos : lx.pos+end]
	lx.line += strings.Count(s, "\n")
	lx.pos += end + len(tag)
	return s, nil
}

func (lx *lexer) number() tokenKind {
	start := lx.pos
	rest := lx.src[lx.pos:]
	// PostgreSQL and SQLite allow underscores between digits.
	digit := isDigit
	if lx.d == dPostgres || lx.d == dSQLite {
		digit = func(c byte) bool { return isDigit(c) || c == '_' }
	}
	if len(rest) > 2 && rest[0] == '0' {
		var kind tokenKind
		var f func(byte) bool
		switch rest[1] {
		case 'x':
			kind, f = tkHex, isHexDigit
		case 'b':
			kind, f = tkBit, isBitDigit
		case 'X':
			if lx.d != dMySQL {
				kind, f = tkHex, isHexDigit
			}
		case 'B':
			if lx.d == dPostgres {
				kind, f = tkBit, isBitDigit
			}
		case 'o', 'O':
			if lx.d == dPostgres {
				kind, f = tkOctal, isOctalDigit
			}
		}
		if f != nil && f(rest[2]) {
			lx.pos += 2
			lx.scanWhile(func(c byte) bool { return f(c) || lx.d == dPostgres && c == '_' })
			return kind
		}
	}
	lx.scanWhile(digit)
	digitsOnly := true
	if lx.pos < len(lx.src) && lx.src[lx.pos] == '.' {
		digitsOnly = false
		lx.pos++
		lx.scanWhile(digit)
	}
	if lx.pos < len(lx.src) && (lx.src[lx.pos] == 'e' || lx.src[lx.pos] == 'E') {
		p := lx.pos + 1
		if p < len(lx.src) && (lx.src[p] == '+' || lx.src[p] == '-') {
			p++
		}
		if p < len(lx.src) && isDigit(lx.src[p]) {
			digitsOnly = false
			lx.pos = p
			lx.scanWhile(isDigit)
		}
	}
	// MySQL allows identifiers that begin with a digit, such as 1st_column.
	if digitsOnly && lx.d == dMySQL && lx.pos < len(lx.src) && isIdentChar(lx.src[lx.pos]) {
		lx.pos = start
		lx.scanWhile(isIdentChar)
		return tkIdent
	}
	return tkNumber
}

func (lx *lexer) variable() error {
	lx.pos++
	if lx.pos < len(lx.src) && lx.src[lx.pos] == '@' {
		lx.pos++
	}
	if lx.pos < len(lx.src) {
		switch c := lx.src[lx.pos]; c {
		case '\'', '"':
			_, err := lx.quoted(c, lx.stringEscapes())
			return err
		case '`':
			_, err := lx.quoted(c, nil)
			return err
		}
	}
	lx.scanWhile(func(c byte) bool { return isIdentChar(c) || c == '.' })
	return nil
}

func (lx *lexer) word() (tokenKind, string, error) {
	start := lx.pos
	lx.scanWhile(isIdentChar)
	if lx.d == dSpanner && lx.pos < len(lx.src) && (lx.src[lx.pos] == '\'' || lx.src[lx.pos] == '"') {
		if kind, s, ok, err := lx.gsqlPrefixed(lx.src[start:lx.pos]); ok {
			return kind, s, err
		}
		return tkIdent, "", nil
	}
	if lx.pos < len(lx.src) && lx.src[lx.pos] == '\'' {
		switch lx.src[start:lx.pos] {
		case "X", "x":
			_, err := lx.quoted('\'', nil)
			return tkHex, "", err
		case "B", "b":
			_, err := lx.quoted('\'', nil)
			return tkBit, "", err
		case "N", "n":
			s, err := lx.quoted('\'', lx.stringEscapes())
			return tkString, s, err
		case "E", "e":
			if lx.d == dPostgres {
				s, err := lx.quoted('\'', lx.pgEscape)
				return tkString, s, err
			}
		case "Q", "q", "NQ", "Nq", "nQ", "nq":
			if lx.d == dOracle {
				s, err := lx.oracleQuoted()
				return tkString, s, err
			}
		}
	}
	return tkIdent, "", nil
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func isHexDigit(c byte) bool { return isDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

func isOctalDigit(c byte) bool { return '0' <= c && c <= '7' }

func isBitDigit(c byte) bool { return c == '0' || c == '1' }

func isIdentStart(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c >= 0x80
}

func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) || c == '$' }

// oracleQuoted scans Oracle's alternative quoting q'[...]' starting at the opening quote, in which the delimiter
// following the quote is closed by itself or the matching bracket.
func (lx *lexer) oracleQuoted() (string, error) {
	line := lx.line
	if lx.pos+1 >= len(lx.src) {
		return "", &syntaxError{line, "unterminated quoted string"}
	}
	open := lx.src[lx.pos+1]
	end := open
	if i := strings.IndexByte("[{<(", open); i >= 0 {
		end = "]}>)"[i]
	}
	start := lx.pos + 2
	i := strings.Index(lx.src[start:], string(end)+"'")
	if i < 0 {
		return "", &syntaxError{line, "unterminated quoted string"}
	}
	s := lx.src[start : start+i]
	lx.line += strings.Count(s, "\n")
	lx.pos = start + i + 2
	return s, nil
}
