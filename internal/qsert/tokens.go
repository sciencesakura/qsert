package qsert

// Utilities for sequences of tokens of an expression.

func isOpening(t token) bool { return t.isOp("(") || t.isOp("[") || t.isOp("{") }

func isClosing(t token) bool { return t.isOp(")") || t.isOp("]") || t.isOp("}") }

// findTopLevel returns the index of the first token outside brackets for which f returns true, or -1.
func findTopLevel(toks []token, f func(i int) bool) int {
	depth := 0
	for i, t := range toks {
		switch {
		case isOpening(t):
			depth++
		case isClosing(t):
			depth--
		case depth == 0 && f(i):
			return i
		}
	}
	return -1
}

// closing returns the index of the bracket closing the one at open, or -1.
func closing(toks []token, open int) int {
	depth := 0
	for i := open; i < len(toks); i++ {
		switch {
		case isOpening(toks[i]):
			depth++
		case isClosing(toks[i]):
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// enclosed returns the tokens between the brackets if toks consists of a bracketed list preceded by prefix tokens,
// such as f(...) with prefix 1 and [...] with prefix 0.
func enclosed(toks []token, prefix int, open string) ([]token, bool) {
	n := len(toks)
	if n < prefix+2 || !toks[prefix].isOp(open) || closing(toks, prefix) != n-1 {
		return nil, false
	}
	return toks[prefix+1 : n-1], true
}

// splitList splits a comma-separated list at the top level. It fails if an item is empty.
func splitList(toks []token) ([][]token, bool) {
	if len(toks) == 0 {
		return nil, true
	}
	var items [][]token
	for {
		i := findTopLevel(toks, func(i int) bool { return toks[i].isOp(",") })
		if i == 0 || i == len(toks)-1 {
			return nil, false
		}
		if i < 0 {
			return append(items, toks), true
		}
		items = append(items, toks[:i])
		toks = toks[i+1:]
	}
}

// stripCast splits a cast CAST(x AS type), or x::type of PostgreSQL, into the expression and the type. Of a chain of
// casts x::t1::t2, the type is the last one.
func stripCast(toks []token) (inner, typ []token, ok bool) {
	if args, ok := enclosed(toks, 1, "("); ok && toks[0].isWord("CAST") {
		i := findTopLevel(args, func(i int) bool { return args[i].isWord("AS") })
		if i < 0 {
			return nil, nil, false
		}
		return args[:i], args[i+1:], true
	}
	first := findTopLevel(toks, func(i int) bool { return toks[i].isOp("::") })
	if first < 0 {
		return nil, nil, false
	}
	last := first
	for {
		rest := toks[last+1:]
		i := findTopLevel(rest, func(i int) bool { return rest[i].isOp("::") })
		if i < 0 {
			return toks[:first], rest, true
		}
		last += i + 1
	}
}
