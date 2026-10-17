package qsert

//go:generate go tool goyacc -l -o insert_parser.go -p insert -v "" insert.y

import (
	"fmt"
	"strconv"
	"strings"
)

// insertStmt is a parsed INSERT or REPLACE statement whose values have been evaluated.
type insertStmt struct {
	table   []string // qualified table name
	columns []string
	rows    [][]cell
}

// rawInsert is the result of the INSERT grammar, holding each value as a sequence of tokens.
type rawInsert struct {
	line        int
	table       []string
	columns     []string // nil if the column list is omitted
	rows        []rawRow
	unsupported string  // set if the statement is a valid but unsupported form
	json        []token // the value of INSERT ... JSON of CQL
}

type rawRow struct {
	line   int
	values [][]token
}

type assignment struct {
	column string
	value  []token
}

func wrap(open token, toks []token, close token) []token {
	r := make([]token, 0, len(toks)+2)
	r = append(r, open)
	r = append(r, toks...)
	return append(r, close)
}

var insertKeywords = map[string]int{
	"INSERT":        INSERT,
	"REPLACE":       REPLACE,
	"INTO":          INTO,
	"VALUES":        VALUES,
	"VALUE":         VALUE,
	"ROW":           ROW,
	"SET":           SET,
	"IGNORE":        IGNORE,
	"LOW_PRIORITY":  LOW_PRIORITY,
	"DELAYED":       DELAYED,
	"HIGH_PRIORITY": HIGH_PRIORITY,
	"PARTITION":     PARTITION,
	"AS":            AS,
	"OR":            OR,
	"ON":            ON,
	"RETURNING":     RETURNING,
	"DEFAULT":       DEFAULT,
	"SELECT":        SELECT,
	"WITH":          WITH,
	"TABLE":         TABLE,
}

// dialectKeywords are the keywords of INSERT statements of a dialect, which are names in the other dialects.
var dialectKeywords = map[Dialect]map[string]int{
	dCassandra: {"IF": IF, "USING": USING, "JSON": JSON},
	dSpanner:   {"THEN": THEN, "ASSERT_ROWS_MODIFIED": ASSERT_ROWS_MODIFIED, "UPDATE": UPDATE},
}

// insertScanner feeds the tokens of a statement to the INSERT parser.
type insertScanner struct {
	d      Dialect
	toks   []token
	pos    int
	last   token
	err    error
	result *rawInsert
}

func (l *insertScanner) Lex(lval *insertSymType) int {
	if l.pos >= len(l.toks) {
		l.last = token{kind: tkEOF, line: l.last.line}
		return 0
	}
	t := l.toks[l.pos]
	l.pos++
	l.last = t
	lval.tok = t
	switch t.kind {
	case tkIdent:
		lval.tok.val = t.text
		if id, ok := insertKeywords[strings.ToUpper(t.text)]; ok {
			return id
		}
		if id, ok := dialectKeywords[l.d][strings.ToUpper(t.text)]; ok {
			return id
		}
		return IDENT
	case tkQuotedIdent:
		return QIDENT
	case tkDQString:
		return DQSTRING
	case tkOp:
		switch t.text {
		case "(", ")", "[", "]", "{", "}", ",", ".", "=":
			return int(t.text[0])
		case ":=":
			return ASSIGN
		}
	}
	return OTHER
}

func (l *insertScanner) Error(string) {
	if l.err != nil {
		return
	}
	near := "end of statement"
	if l.last.kind != tkEOF {
		near = strconv.Quote(l.last.text)
	}
	l.err = &syntaxError{l.last.line, "syntax error near " + near}
}

func init() {
	insertErrorVerbose = false
}

// parseInsert parses an INSERT or REPLACE statement and evaluates its values.
func parseInsert(toks []token, d Dialect) (*insertStmt, error) {
	l := &insertScanner{toks: toks, d: d}
	if insertParse(l) != 0 {
		return nil, l.err
	}
	raw := l.result
	if raw.unsupported != "" {
		return nil, &syntaxError{raw.line, raw.unsupported + " is not supported"}
	}
	if raw.json != nil {
		return cqlJSONInsert(raw)
	}
	columns := raw.columns
	if columns == nil {
		columns = ordinals(len(raw.rows[0].values))
	}
	if err := checkDuplicateColumns(columns, raw.line); err != nil {
		return nil, err
	}
	st := &insertStmt{table: raw.table, columns: columns, rows: make([][]cell, len(raw.rows))}
	for i, r := range raw.rows {
		if len(r.values) != len(columns) {
			return nil, &syntaxError{r.line, fmt.Sprintf("column count doesn't match value count at row %d", i+1)}
		}
		row := make([]cell, len(r.values))
		for j, v := range r.values {
			row[j] = evalCell(v, d)
		}
		st.rows[i] = row
	}
	return st, nil
}

func isInsert(toks []token) bool {
	return len(toks) > 0 && (toks[0].isWord("INSERT") || toks[0].isWord("REPLACE"))
}

func ordinals(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = strconv.Itoa(i + 1)
	}
	return names
}

func checkDuplicateColumns(columns []string, line int) error {
	seen := make(map[string]bool, len(columns))
	for _, c := range columns {
		k := strings.ToLower(c)
		if seen[k] {
			return &syntaxError{line, fmt.Sprintf("duplicate column %q", c)}
		}
		seen[k] = true
	}
	return nil
}
