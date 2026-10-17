package qsert

import "strings"

// keywordOps are keywords that act as operators or introduce clauses in expressions.
// A parenthesis following them is not a function call, and a sign following them is a unary operator.
var keywordOps = map[string]bool{
	"AND": true, "OR": true, "XOR": true, "NOT": true, "IS": true, "IN": true, "LIKE": true, "REGEXP": true,
	"RLIKE": true, "BETWEEN": true, "CASE": true, "WHEN": true, "THEN": true, "ELSE": true, "AS": true,
	"INTERVAL": true, "ESCAPE": true, "COLLATE": true, "BINARY": true, "EXISTS": true, "ANY": true, "ALL": true,
	"SOME": true, "SELECT": true, "FROM": true, "WHERE": true, "ON": true, "USING": true, "DISTINCT": true,
}

// formatTokens formats an expression by putting a single space between tokens, except around brackets, commas,
// dots, colons and unary operators. Comments have already been removed by the lexer.
func formatTokens(toks []token) string {
	var b strings.Builder
	var prev *token
	var brackets []string // the open brackets enclosing the current token
	angles := typeParameterBrackets(toks)
	prevUnary := false
	for i := range toks {
		t := &toks[i]
		if isClosing(*t) && len(brackets) > 0 {
			brackets = brackets[:len(brackets)-1]
		}
		inBrackets := len(brackets) > 0 && brackets[len(brackets)-1] == "["
		// A sign following a unary sign is separated, since -- would begin a comment.
		space := prev != nil && (!prevUnary || isSign(t)) && spaceBetween(prev, t, inBrackets)
		// ARRAY<INT64>[1, 2] and STRUCT<a INT64>(1)
		if angles[i] || i > 0 && angles[i-1] && (prev.isOp("<") || t.isOp("[") || t.isOp("(")) {
			space = false
		}
		if space {
			b.WriteByte(' ')
		}
		b.WriteString(t.text)
		if isOpening(*t) {
			brackets = append(brackets, t.text)
		}
		prevUnary = isSign(t) && unaryContext(prev)
		prev = t
	}
	return b.String()
}

// typeParameterBrackets returns the indices of the angle brackets of type parameters of GoogleSQL such as
// ARRAY<INT64> and STRUCT<a INT64>.
func typeParameterBrackets(toks []token) map[int]bool {
	var angles map[int]bool
	depth := 0
	for i, t := range toks {
		switch {
		case t.isOp("<") && i > 0 && (toks[i-1].isWord("ARRAY") || toks[i-1].isWord("STRUCT") ||
			toks[i-1].isWord("RANGE")):
			if angles == nil {
				angles = make(map[int]bool)
			}
			angles[i] = true
			depth++
		case depth > 0 && t.isOp(">"):
			angles[i] = true
			depth--
		case depth > 0 && t.isOp(">>"):
			angles[i] = true
			depth = max(depth-2, 0)
		}
	}
	return angles
}

// spaceBetween reports whether a space is put between tokens. inBrackets reports whether they are in square brackets,
// in which a colon separates the bounds of a slice such as a[1:2] in PostgreSQL.
func spaceBetween(prev, cur *token, inBrackets bool) bool {
	if prev.kind == tkOp {
		switch prev.text {
		case "(", "[", "{", ".", "::":
			return false
		case ":":
			return !inBrackets
		}
	}
	if isIntroducer(prev) {
		switch cur.kind {
		case tkString, tkDQString, tkHex, tkBit:
			return false
		}
	}
	if cur.kind == tkOp {
		switch cur.text {
		case ")", "]", "}", ",", ".", "::", ":":
			return false
		case "(":
			return !isCallee(prev)
		case "[":
			return !isCallee(prev) && !prev.isOp(")") && !prev.isOp("]")
		}
	}
	return true
}

// isIntroducer reports whether t is a character set introducer such as _utf8mb4.
func isIntroducer(t *token) bool { return t.kind == tkIdent && strings.HasPrefix(t.text, "_") }

func isCallee(t *token) bool {
	return t.kind == tkQuotedIdent || t.kind == tkIdent && !keywordOps[strings.ToUpper(t.text)]
}

func isSign(t *token) bool {
	if t.kind != tkOp {
		return false
	}
	switch t.text {
	case "-", "+", "~", "!":
		return true
	}
	return false
}

// unaryContext reports whether an operator following prev is a unary operator.
func unaryContext(prev *token) bool {
	if prev == nil {
		return true
	}
	switch prev.kind {
	case tkOp:
		return prev.text != ")" && prev.text != "]"
	case tkIdent:
		w := strings.ToUpper(prev.text)
		return keywordOps[w] || w == "DIV" || w == "MOD"
	}
	return false
}
