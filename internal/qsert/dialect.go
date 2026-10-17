package qsert

import "strings"

// Dialect is a SQL dialect, which determines how SQL is tokenized and how expressions are evaluated. The zero value
// is standard SQL.
type Dialect int

const (
	dISO Dialect = iota
	dMySQL
	dPostgres
	dSQLite
	dMSSQL
	dOracle
	dCassandra
	dSpanner
)

// DialectNames are the names of the dialects, indexed by Dialect.
var DialectNames = [...]string{"iso", "mysql", "postgres", "sqlite", "mssql", "oracle", "cassandra", "spanner"}

func (d Dialect) String() string { return DialectNames[d] }

// ParseDialect returns the dialect of a name in DialectNames, ignoring case.
func ParseDialect(s string) (Dialect, bool) {
	for i, n := range DialectNames {
		if strings.EqualFold(s, n) {
			return Dialect(i), true
		}
	}
	return 0, false
}

// bracketIdentifiers reports whether [ident] is a quoted identifier.
func (d Dialect) bracketIdentifiers() bool { return d == dMSSQL || d == dSQLite }

// nestedComments reports whether block comments can be nested.
func (d Dialect) nestedComments() bool { return d == dISO || d == dPostgres || d == dMSSQL }

// adjacentStrings reports whether string literals separated by sep are concatenated.
func (d Dialect) adjacentStrings(newline bool) bool {
	switch d {
	case dMySQL:
		return true
	case dISO, dPostgres:
		return newline
	}
	return false
}

// unicodeCaseMapping reports whether UPPER and LOWER can be evaluated for non-ASCII letters.
// They depend on the locale or collation in PostgreSQL and SQL Server, and fold only ASCII letters in SQLite.
func (d Dialect) unicodeCaseMapping() bool {
	return d == dISO || d == dMySQL || d == dOracle || d == dSpanner
}

// Binary operators. Each operator is assigned a precedence level of the expression grammar per dialect.
const (
	opAdd    = "+"
	opSub    = "-"
	opMul    = "*"
	opDiv    = "/"
	opMod    = "%"
	opIntDiv = "DIV"
	opBitOr  = "|"
	opBitAnd = "&"
	opBitXor = "XOR"
	opShl    = "<<"
	opShr    = ">>"
	opConcat = "||"
	opPow    = "POW"
)

type operator struct {
	level int // 1 (lowest) to 7 (highest); 5 is the level of + and -
	name  string
}

// operators maps the text of binary operators other than + and - to their meanings per dialect.
var operators = [...]map[string]operator{
	dISO: {
		"||": {5, opConcat},
		"*":  {6, opMul},
		"/":  {6, opDiv},
	},
	dMySQL: {
		"|":   {1, opBitOr},
		"&":   {2, opBitAnd},
		"<<":  {3, opShl},
		">>":  {3, opShr},
		"*":   {6, opMul},
		"/":   {6, opDiv},
		"%":   {6, opMod},
		"DIV": {6, opIntDiv},
		"MOD": {6, opMod},
		"^":   {7, opBitXor},
	},
	dPostgres: {
		"||": {3, opConcat},
		"|":  {3, opBitOr},
		"&":  {3, opBitAnd},
		"#":  {3, opBitXor},
		"<<": {3, opShl},
		">>": {3, opShr},
		"*":  {6, opMul},
		"/":  {6, opDiv},
		"%":  {6, opMod},
		"^":  {7, opPow},
	},
	dSQLite: {
		"&":  {3, opBitAnd},
		"|":  {3, opBitOr},
		"<<": {3, opShl},
		">>": {3, opShr},
		"*":  {6, opMul},
		"/":  {6, opDiv},
		"%":  {6, opMod},
		"||": {7, opConcat},
	},
	dMSSQL: {
		"&": {5, opBitAnd},
		"|": {5, opBitOr},
		"^": {5, opBitXor},
		"*": {6, opMul},
		"/": {6, opDiv},
		"%": {6, opMod},
	},
	dOracle: {
		"||": {5, opConcat},
		"*":  {6, opMul},
		"/":  {6, opDiv},
	},
	// CQL's arithmetic depends on the types of the columns, since literals are untyped.
	dCassandra: {},
	dSpanner: {
		"|":  {1, opBitOr},
		"^":  {2, opBitXor},
		"&":  {3, opBitAnd},
		"<<": {4, opShl},
		">>": {4, opShr},
		"*":  {6, opMul},
		"/":  {6, opDiv},
		"||": {6, opConcat},
	},
}

// bitwiseNot reports whether ~ is the bitwise NOT operator.
func (d Dialect) bitwiseNot() bool {
	return d == dMySQL || d == dPostgres || d == dSQLite || d == dMSSQL || d == dSpanner
}
