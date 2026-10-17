// Package qsert parses SQL INSERT statements, evaluates their values according to a dialect, and writes them as CSV.
package qsert

// Convert parses the INSERT and REPLACE statements in src and adds them to c. Other statements are ignored.
// backslashEscapes is false for MySQL with NO_BACKSLASH_ESCAPES.
func Convert(src string, d Dialect, backslashEscapes bool, c *Collector) []error {
	var errs []error
	lx := newLexer(src, d, backslashEscapes)
	for {
		toks, eof, err := lx.statement()
		if err != nil {
			return append(errs, err)
		}
		stmts := [][]token{toks}
		if d == dCassandra {
			stmts = cqlStatements(toks)
		}
		for _, toks := range stmts {
			if !isInsert(toks) {
				continue
			}
			if st, err := parseInsert(toks, d); err != nil {
				errs = append(errs, err)
			} else {
				c.add(st)
			}
		}
		if eof {
			return errs
		}
	}
}
