package qsert

import (
	"fmt"
	"strings"
	"testing"
)

func evalString(t *testing.T, d Dialect, expr string) string {
	t.Helper()
	toks, _, err := newLexer(expr, d, true).statement()
	if err != nil {
		t.Fatalf("%s: %q: %v", d, expr, err)
	}
	c := evalCell(toks, d)
	if c.null {
		return "<NULL>"
	}
	return c.text
}

type evalTest struct{ expr, want string }

func testEvaluate(t *testing.T, d Dialect, tests []evalTest) {
	t.Helper()
	for _, tt := range tests {
		if got := evalString(t, d, tt.expr); got != tt.want {
			t.Errorf("%s: %s = %q, want %q", d, tt.expr, got, tt.want)
		}
	}
}

func TestEvaluateISO(t *testing.T) {
	testEvaluate(t, dISO, []evalTest{
		{"123", "123"},
		{"-1.50", "-1.50"},
		{".5", "0.5"},
		{"'it''s'", "it's"},
		{`'C:\path'`, `C:\path`},
		{"N'national'", "national"},
		{"'a'\n'b'", "ab"},
		{"NULL", "<NULL>"},
		{"TRUE", "TRUE"},
		{"false", "FALSE"},
		{"1 + 2 * 3", "7"},
		{"1.5 * 1.5", "2.25"},
		{"1.50 * 2", "3.00"},
		{"99999999999999999999 + 1", "100000000000000000000"},
		{"'a' || 'b' || 'c'", "abc"},
		{"'a' || NULL", "<NULL>"},
		{"UPPER('äb')", "ÄB"},
		{"TRIM('  x  ')", "x"},
		{"CHAR_LENGTH('あい')", "2"},
		{"ABS(-1.50)", "1.50"},
		{"CEIL(1.2)", "2"},
		{"FLOOR(-1.2)", "-2"},
		{"MOD(-7, 3)", "-1"},
		{"COALESCE(NULL, 'x')", "x"},
		// implementation-defined or not in the standard
		{"1 / 2", "1 / 2"},
		{"1e3", "1e3"},
		{"7 % 2", "7 % 2"},
		{"'a' 'b'", "'a' 'b'"},
		{`"col"`, `"col"`},
		{"'a' || 1", "'a' || 1"},
		{"0x41", "0x41"},
		{"CONCAT('a', 'b')", "CONCAT('a', 'b')"},
		{"MOD(7.5, 2)", "MOD(7.5, 2)"},
		{"1 # comment", "1 # comment"},
		{"1 /* a /* nested */ b */ + 1", "2"},
	})
}

// The expected values of MySQL were confirmed with MySQL 8.4.
func TestEvaluateMySQL(t *testing.T) {
	testEvaluate(t, dMySQL, []evalTest{
		// literals
		{"1e3", "1000"},
		{"1.5E-3", "0.0015"},
		{"1e20", "1e20"},
		{"1e15", "1e15"},
		{"1e14", "100000000000000"},
		{"1e-15", "0.000000000000001"},
		{"1e-16", "1e-16"},
		{"-0e0", "-0"},
		{"1e-10 / 3", "0.000000000033333333333333335"},
		{`'a\nb\\c\%'`, "a\nb\\c\\%"},
		{`"double"`, "double"},
		{"'a' 'b' \"c\"", "abc"},
		{"_utf8mb4'abc'", "abc"},
		{"_binary 0xFF", "0xFF"},
		{"X'4142'", "X'4142'"},
		{"0b101", "0b101"},
		{"true", "1"},
		{"FALSE", "0"},
		{"-9223372036854775808", "-9223372036854775808"},
		{"1 # comment", "1"},
		{"1--1", "2"},
		// arithmetic
		{"0.1 + 0.2", "0.3"},
		{"1 / 3", "0.3333"},
		{"1 / 3 * 3", "1 / 3 * 3"}, // the quotient has more digits internally: 1.0000
		{"1.0 / 3", "0.33333"},
		{"6 / 3", "2.0000"},
		{"-2 / 3", "-0.6667"},
		{"7 DIV 2", "3"},
		{"-7 DIV 2", "-3"},
		{"-7 MOD 3", "-1"},
		{"5.5 % 2", "1.5"},
		{"1e0 / 4", "0.25"},
		{"9223372036854775807 + 1", "9223372036854775807 + 1"},
		{"1 / 0", "1 / 0"},
		{"1 + NULL", "<NULL>"},
		{"'1' + 1", "'1' + 1"},
		{"'a' || 'b'", "'a' || 'b'"},
		// bitwise
		{"1 | 2 & 3", "3"},
		{"6 ^ 3", "5"},
		{"1 << 4", "16"},
		{"~0", "18446744073709551615"},
		// functions
		{"CONCAT('a', 1, 2.50)", "a12.50"},
		{"CONCAT('a', NULL)", "<NULL>"},
		{"CONCAT_WS(',', 'a', NULL, 'b')", "a,b"},
		{"UCASE('äb')", "ÄB"},
		{"SUBSTRING('あいうえお', 2, 3)", "いうえ"},
		{"SUBSTR('hello', -3)", "llo"},
		{"SUBSTRING('hello', 0)", ""},
		{"LEFT('hello', 2)", "he"},
		{"REPEAT('ab', 3)", "ababab"},
		{"LPAD('7', 3, '0')", "007"},
		{"LPAD('a', 3, '')", ""},
		{"LENGTH('あい')", "6"},
		{"ROUND(2.345, 2)", "2.35"},
		{"ROUND(-2.5)", "-3"},
		{"ROUND(1234, -2)", "1200"},
		{"TRUNCATE(2.349, 2)", "2.34"},
		{"MOD(10, 3)", "1"},
		{"IFNULL(NULL, 2)", "2"},
		{"MOD(7, 0)", "MOD(7, 0)"},
		{"NOW()", "NOW()"},
	})
}

// The expected values of PostgreSQL were confirmed with PostgreSQL 17.
func TestEvaluatePostgres(t *testing.T) {
	testEvaluate(t, dPostgres, []evalTest{
		// literals
		{"1e3", "1000"},
		{"1.5e-3", "0.0015"},
		{"0x41", "65"},
		{"0b101", "5"},
		{"0o17", "15"},
		{"1_000", "1000"},
		{"$$a'b$$", "a'b"},
		{"$x$q$x$", "q"},
		{`E'a\nb\x41\101\u3042\''`, "a\nbAAあ'"},
		{`'\n'`, `\n`},
		{"'a'\n'b'", "ab"},
		{"'a' 'b'", "'a' 'b'"},
		{"true", "t"},
		{"1 /* a /* nested */ b */ + 1", "2"},
		// arithmetic
		{"1 / 3", "0"},
		{"-7 / 2", "-3"},
		{"1.0 / 3", "0.33333333333333333333"},
		{"10.0 / 4", "2.5000000000000000"},
		{"100 / 3.0", "33.3333333333333333"},
		{"0.001 / 7", "0.00014285714285714286"},
		{"123456.0 / 7", "17636.571428571429"},
		{"1e-10 / 3", "0.0000000000333333333333333333"},
		{"99999 / 0.001", "99999000.000000000000"},
		{"5.5 % 2", "1.5"},
		{"1.0 % 0.3", "0.1"},
		{"12.500 * 1.0", "12.5000"},
		{"2147483648 + 1", "2147483649"},
		{"2147483647 + 1", "2147483647 + 1"},
		{"-2147483648 * 2", "-2147483648 * 2"},
		{"-9223372036854775808 - 1", "-9223372036854775808 - 1"},
		{"9223372036854775808 + 1", "9223372036854775809"},
		{"1 / 0", "1 / 0"},
		{"2 ^ 3 ^ 2", "64"},
		{"-2 ^ 2", "4"},
		{"2 ^ 70", "1.1805916207174113e+21"},
		{"2 ^-1", "2 ^- 1"},
		{"2 ^ 0.5", "2 ^ 0.5"},
		// bitwise
		{"6 # 3", "5"},
		{"1 + 2 | 3", "3"},
		{"1 << 31", "-2147483648"},
		{"1 << 32", "1 << 32"},
		{"3000000000 << 1", "6000000000"},
		{"-8 >> 1", "-4"},
		{"~0", "-1"},
		// concatenation
		{"'a' || 1", "a1"},
		{"'a' || 1.50", "a1.50"},
		{"'a' || true", "atrue"},
		{"'a' || 2 * 3", "a6"},
		{"'a' || NULL", "<NULL>"},
		{"1 || 2", "1 || 2"},
		// functions
		{"concat('a', NULL, 1.50, true)", "a1.50t"},
		{"upper('ab')", "AB"},
		{"upper('äb')", "upper('äb')"},
		{"length('あい')", "2"},
		{"octet_length('あい')", "6"},
		{"length(12.50)", "length(12.50)"},
		{"left('hello', -2)", "hel"},
		{"right('hello', -2)", "llo"},
		{"substring('hello', 0, 3)", "he"},
		{"substring('hello', -1, 3)", "h"},
		{"substr('hello', 3, -2)", "substr('hello', 3, -2)"},
		{"lpad('abc', 2)", "ab"},
		{"rpad('a', 3)", "a  "},
		{"lpad('a', 3, '')", "a"},
		{"btrim('xxaxx', 'x')", "a"},
		{"trim('xxaxx', 'x')", "a"},
		{"round(2.5)", "3"},
		{"round(2.345, 2)", "2.35"},
		{"round(1.5, 3)", "1.500"},
		{"round(1234.5, -2)", "1200"},
		{"round(5, 2)", "5.00"},
		{"trunc(2.789, 2)", "2.78"},
		{"ceil(-1.2)", "-1"},
		{"sign(-1.50)", "-1"},
		{"abs(-1.50)", "1.50"},
		{"abs(-2147483648)", "abs(-2147483648)"},
		{"mod(7, 0.5)", "0.0"},
		{"ifnull(NULL, 2)", "ifnull(NULL, 2)"},
	})
}

// The expected values of SQLite were confirmed with SQLite 3.53.
func TestEvaluateSQLite(t *testing.T) {
	testEvaluate(t, dSQLite, []evalTest{
		// literals
		{"1.5", "1.5"},
		{"2.0", "2.0"},
		{"1e3", "1000.0"},
		{"1e16", "10000000000000000.0"},
		{"1e17", "1.0e+17"},
		{"1e-5", "1.0e-05"},
		{"0.0001", "0.0001"},
		{"0x41", "65"},
		{"0xFFFFFFFFFFFFFFFF", "-1"},
		{"1_000", "1000"},
		{"true", "1"},
		{`"dq"`, `"dq"`},
		{"[x]", "[x]"},
		// arithmetic
		{"1.50 * 2", "3.0"},
		{"7 / 2", "3"},
		{"-7 / 2", "-3"},
		{"10.0 / 4", "2.5"},
		{"0.1 + 0.2", "0.1 + 0.2"}, // SQLite outputs an approximation of 17 digits
		{"5.5 % 2", "1.0"},
		{"-7 % 3", "-1"},
		{"1 / 0", "<NULL>"},
		{"1 % 0", "<NULL>"},
		{"2147483647 + 1", "2147483648"},
		{"1 << 62 << 1", "-9223372036854775808"},
		{"1 << 64", "0"},
		{"1 << -1", "0"},
		{"-1 >> 64", "-1"},
		{"~0", "-1"},
		// concatenation
		{"1 || 2", "12"},
		{"1.5 || 'x'", "1.5x"},
		{"1e20 || ''", "1.0e+20"},
		{"'a' || NULL", "<NULL>"},
		{"2 * 3 || 'a'", "2 * 3 || 'a'"}, // || precedes *
		// functions
		{"concat('a', NULL, 'b')", "ab"},
		{"upper('äb')", "äB"},
		{"length('あい')", "2"},
		{"length(12.50)", "4"},
		{"substr('hello', 0, 3)", "he"},
		{"substr('hello', -3, 2)", "ll"},
		{"substr('hello', 3, -2)", "he"},
		{"substr('hello', -10)", "hello"},
		{"substr('hello', -10, 7)", "he"},
		{"trim('xxaxx', 'x')", "a"},
		{"round(2.345, 2)", "2.35"},
		{"round(-2.5)", "-3.0"},
		{"round(1234, -2)", "1234.0"},
		{"trunc(-2.7)", "-2.0"},
		{"ceil(5)", "5"},
		{"ceil(-0.5)", "0.0"},
		{"sign(-1.5)", "-1"},
		{"mod(7, 3)", "1.0"},
		{"left('a', 1)", "left('a', 1)"},
	})
}

// The expected values of SQL Server were confirmed with SQL Server 2022.
func TestEvaluateMSSQL(t *testing.T) {
	testEvaluate(t, dMSSQL, []evalTest{
		{"[x]", "[x]"},
		{`"x"`, `"x"`},
		{"'a' + 'b'", "ab"},
		{"'a' + 1", "'a' + 1"},
		{"7 / 2", "3"},
		{"1.0 / 3", "1.0 / 3"},
		{"2147483647 + 1", "2147483647 + 1"},
		{"2147483648 + 1", "2147483649"},
		{"6 ^ 3", "5"},
		{"1 + 2 | 4", "7"},
		{"1e3", "1e3"},
		{"TRUE", "TRUE"},
		{"0x41", "0x41"},
		{"CONCAT('a', NULL, 1.50)", "a1.50"},
		{"LEN('ab  ')", "2"},
		{"ROUND(2.345, 2)", "2.350"},
		{"ROUND(2.345, 2, 1)", "2.340"},
		{"ROUND(2.5)", "ROUND(2.5)"},
		{"SIGN(-1.50)", "-1.00"},
		{"SUBSTRING('hello', 0, 3)", "he"},
		{"REPLICATE('ab', 2)", "abab"},
		{"REPLACE('a.b', '.', '-')", "a-b"},
		{"REPLACE('aXa', 'x', '-')", "REPLACE('aXa', 'x', '-')"},
		{"UPPER(N'äb')", "UPPER(N'äb')"},
		// non-ASCII characters of a string other than N'...' are converted to the code page of the collation
		{"N'日本'", "日本"},
		{"'日本'", "'日本'"},
		{"'a' + N'本'", "a本"},
		// the result type of decimal arithmetic
		{"ROUND(9.4, 0)", "9.0"},
		{"ROUND(9.9, 0)", "ROUND(9.9, 0)"}, // overflows numeric(2, 1)
		{"0.1234567890123456789012345678901234567 * 2", "0.1234567890123456789012345678901234567 * 2"},
		{"-2147483648 * 2", "-2147483648 * 2"},
		{"-2147483647 * 2", "-2147483647 * 2"}, // overflows int
		{"ROUND(2.5, 0)", "3.0"},               // not truncated by the preceding ROUND(x, d, 1)
	})
}

// The expected values of Oracle were confirmed with Oracle AI Database 26ai.
func TestEvaluateOracle(t *testing.T) {
	testEvaluate(t, dOracle, []evalTest{
		{"''", "<NULL>"},
		{"1.50", "1.5"},
		{"1e3", "1000"},
		{"1.50 * 2", "3"},
		{"1 / 4", "0.25"},
		{"1 / 3", "1 / 3"},
		{"'a' || NULL || 'b'", "ab"},
		{"0.5 || 'x'", ".5x"},
		{"-0.5 || ''", "-.5"},
		{"NULL || NULL", "<NULL>"},
		{"7 % 2", "7 % 2"},
		{"CONCAT(NULL, 'b')", "b"},
		{"SUBSTR('hello', 0, 2)", "he"},
		{"SUBSTR('hello', -3, 2)", "ll"},
		{"SUBSTR('hello', 9)", "<NULL>"},
		{"TRIM('  ')", "<NULL>"},
		{"REPLACE('abc', 'b')", "ac"},
		{"MOD(-7, 3)", "-1"},
		{"MOD(5, 0)", "5"},
		{"ROUND(2.345, 2)", "2.35"},
		{"TRUNC(2.789, 1)", "2.7"},
		{"NVL(NULL, 'z')", "z"},
		{"LPAD('7', 3, '0')", "007"},
		{"LPAD('あ', 3)", "LPAD('あ', 3)"},
		{"TRUE", "TRUE"},
		{"q'[it's]'", "it's"},
		{"nq'{a}b}' || Q'!x!'", "a}bx"},
		{"CONCAT('a', NULL, 1.50)", "a1.5"},
		{"-(-1)", "1"},
		{"- -1", "- -1"}, // a sign cannot follow a sign
	})
}

// The expected values of CQL were confirmed with Cassandra 5.0.
func TestEvaluateCassandra(t *testing.T) {
	testEvaluate(t, dCassandra, []evalTest{
		{"-007", "-7"},
		{"123456789012345678901234567890", "123456789012345678901234567890"},
		{"1.50", "1.50"}, // a decimal, float or double
		{"1e3", "1e3"},
		{"true", "true"},
		{"$$it's$$", "it's"},
		{"0xCAFE", "0xcafe"},
		{"5B6962DD-3F90-4C93-8F61-EABFA4A803E2", "5b6962dd-3f90-4c93-8f61-eabfa4a803e2"},
		// durations
		{"90m", "1h30m"},
		{"-90m", "-1h30m"},
		{"1y13mo", "2y1mo"},
		{"1d25h", "1d25h"},
		{"1us1000ns", "2us"},
		{"0h", ""},
		{"P2W", "14d"},
		{"P1Y2M3DT4H5M6S", "1y2mo3d4h5m6s"},
		{"P0001-02-03T04:05:06", "1y2mo3d4h5m6s"},
		// The types of the operands are those of the columns.
		{"1 + 2", "1 + 2"},
		{"now()", "now()"},
		{"{'a':1,'b' : [ 1,2 ]}", "{'a': 1, 'b': [1, 2]}"},
		{"1 // comment", "1"},
	})
}

// The expected values of GoogleSQL were confirmed with the Cloud Spanner emulator, and are of CAST(x AS STRING).
func TestEvaluateSpanner(t *testing.T) {
	testEvaluate(t, dSpanner, []evalTest{
		// literals
		{`"dq"`, "dq"},
		{"'''tri'ple'''", "tri'ple"},
		{`r'a\nb'`, `a\nb`},
		{`'a\nb\x41\101あ\U0001F600\?\` + "`'", "a\nbAAあ😀?`"},
		{"'it''s'", "'it' 's'"}, // a quote is not escaped by doubling it
		{"b'\\xffa'", "b'\\xffa'"},
		{"0x1F", "31"},
		{"-9223372036854775808", "-9223372036854775808"},
		{"9223372036854775808", "9223372036854775808"},
		{".5", "0.5"},
		{"1E3", "1000"},
		{"1e-7", "1e-07"},
		{"1e15", "1e+15"},
		{"-0.0", "0"},
		{"NUMERIC '1.50'", "1.5"},
		{"NUMERIC '1.0000000005'", "NUMERIC '1.0000000005'"}, // more than 9 digits after the decimal point
		{"TRUE", "true"},
		{"1 # comment", "1"},
		// arithmetic
		{"7 / 2", "3.5"},
		{"1 / 3", "0.33333333333333331"},
		{"0.1 + 0.2", "0.30000000000000004"},
		{"9223372036854775807 + 1", "9223372036854775807 + 1"},
		{"NUMERIC '1' / 3", "0.333333333"},
		{"NUMERIC '2' / 3", "0.666666667"},
		{"NUMERIC '1.5' + 1.5", "3"},
		{"1 | 6 ^ 3", "5"},
		{"6 ^ 3 & 1", "7"},
		{"1 << 2 + 1", "8"},
		{"-8 >> 1", "9223372036854775804"},
		{"1 << 64", "0"},
		{"7 % 2", "7 % 2"},
		{"'a' || 'b'", "ab"},
		{"'a' || 1", "'a' || 1"},
		{"NULL + 1", "NULL + 1"}, // the type of NULL cannot be inferred
		// functions
		{"CONCAT('a', NULL)", "<NULL>"},
		{"CONCAT('a', 1)", "CONCAT('a', 1)"},
		{"UPPER('äb')", "ÄB"},
		{"BYTE_LENGTH('あい')", "6"},
		{"SUBSTR('hello', 0, 2)", "he"},
		{"SUBSTR('hello', -10, 3)", "hel"},
		{"SUBSTR('hello', 2, -1)", "SUBSTR('hello', 2, -1)"},
		{"REPEAT('ab', -1)", "REPEAT('ab', -1)"},
		{"LPAD('ab', 5, 'xy')", "xyxab"},
		{"LPAD('ab', 5, '')", "LPAD('ab', 5, '')"},
		{"ABS(-9223372036854775808)", "ABS(-9223372036854775808)"},
		{"MOD(-7, 3)", "-1"},
		{"MOD(NUMERIC '7.5', 2)", "1.5"},
		{"DIV(-7, 2)", "-3"},
		{"ROUND(-2.5)", "-3"},
		{"ROUND(2.345, 2)", "ROUND(2.345, 2)"},
		{"ROUND(NUMERIC '2.345', 2)", "2.35"},
		{"CEIL(NUMERIC '1.2')", "2"},
		{"IFNULL(NULL, 'x')", "x"},
	})
}

func TestFormat(t *testing.T) {
	testEvaluate(t, dISO, []evalTest{
		{"NOW( )", "NOW()"},
		{"  date_add( now(),\n\tINTERVAL   1 DAY )", "date_add(now(), INTERVAL 1 DAY)"},
		{"CASE /* c */ WHEN a>1 THEN -1 ELSE+2 END", "CASE WHEN a > 1 THEN -1 ELSE +2 END"},
		{"CAST( x AS CHAR( 10 ) )", "CAST(x AS CHAR(10))"},
		{"x IN(1,2)", "x IN (1, 2)"},
		{"t . c -- comment\n+ 1", "t.c + 1"},
		{"a -- comment\n- -b", "a - -b"},
		{"- - b", "- -b"},
		{"-(-b)", "-(-b)"},
		{"'1'::int", "'1'::int"},
		{"`f`(1)", "`f`(1)"},
		{"NOT(a)", "NOT (a)"},
		{"j->>'$.a'", "j ->> '$.a'"},
	})
	testEvaluate(t, dMySQL, []evalTest{
		{"_utf8mb4'x' COLLATE utf8mb4_bin", "_utf8mb4'x' COLLATE utf8mb4_bin"},
	})
}

func TestParseSelectors(t *testing.T) {
	tests := []struct {
		in   string
		want []Selector
		err  bool
	}{
		{in: ".a", want: []Selector{{"", "a", nil, "a", ""}}},
		{in: " .Id , .Users.Name,.db.t.c ", want: []Selector{
			{"", "id", nil, "Id", ""}, {"users", "name", nil, "Users.Name", ""}, {"db.t", "c", nil, "db.t.c", ""},
		}},
		{in: `."a b".` + "`c``d`" + `, ."x""y", .列`, want: []Selector{
			{"a b", "c`d", nil, `"a b"."c` + "`" + `d"`, ""}, {"", `x"y`, nil, `"x""y"`, ""}, {"", "列", nil, "列", ""},
		}},
		{in: ".1", want: []Selector{{"", "1", nil, "1", ""}}},
		{in: ".a[0], .t.b[ -1 ][2]", want: []Selector{
			{"", "a", []step{{index: 0}}, "a[0]", ""}, {"t", "b", []step{{index: -1}, {index: 2}}, "t.b[-1][2]", ""},
		}},
		{in: `.j["a"][ "b\"c\u00e9<" ][1]`, want: []Selector{
			{"", "j", []step{{key: "a", member: true}, {key: `b"cé<`, member: true}, {index: 1}},
				`j["a"]["b\"cé<"][1]`, ""},
		}},
		{in: `.a AS x, .t.b as "B b", .c[0]As` + "`c``0`" + `,.d as as,.as aS As`, want: []Selector{
			{"", "a", nil, "a", "x"}, {"t", "b", nil, "t.b", "B b"}, {"", "c", []step{{index: 0}}, "c[0]", "c`0"},
			{"", "d", nil, "d", "as"}, {"", "as", nil, "as", "As"},
		}},
		{in: ".a ASx", err: true},
		{in: ".a AS", err: true},
		{in: ".a AS ,.b", err: true},
		{in: `.a AS ""`, err: true},
		{in: ".a AS x y", err: true},
		{in: ".a AS x[0]", err: true},
		{in: ".a AS x.y", err: true},
		{in: "", err: true},
		{in: "a", err: true},
		{in: ".a,", err: true},
		{in: ".a .b", err: true},
		{in: ".", err: true},
		{in: `."a`, err: true},
		{in: ".a[", err: true},
		{in: ".a[x]", err: true},
		{in: ".a[1", err: true},
		{in: ".a[0].b", err: true},
		{in: `.a["x]`, err: true},
		{in: `.a["\q"]`, err: true},
		{in: `.a['x']`, err: true},
	}
	for _, tt := range tests {
		got, err := ParseSelectors(tt.in)
		if tt.err {
			if err == nil {
				t.Errorf("%q: want error, got %v", tt.in, got)
			}
			continue
		}
		if err != nil || fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("%q = %v, %v, want %v", tt.in, got, err, tt.want)
		}
	}
}

func TestPGArrayLiteral(t *testing.T) {
	tests := []struct {
		in   string
		want string // elements joined by |, or "-" if not an array
	}{
		{"{}", ""},
		{" { 1 , 2 } ", "1|2"},
		{`{a, "b c" , NULL, "NULL", d e ,"x\"y", \{z, nUlL, \NULL}`, `a|b c|<NULL>|NULL|d e|x"y|{z|<NULL>|NULL`},
		{"{{1,2},{3,4}}", "{1,2}|{3,4}"},
		{`{a,"b"c}`, "-"},
		{"{a,}", "-"},
		{"{a", "-"},
		{"{a} x", "-"},
		{`{"a":1}`, "-"},
		{"abc", "-"},
	}
	for _, tt := range tests {
		got := "-"
		if a := pgArrayLiteral(tt.in); a != nil {
			var elems []string
			for _, e := range a.elems {
				if e.null {
					elems = append(elems, "<NULL>")
				} else {
					elems = append(elems, e.text)
				}
			}
			got = strings.Join(elems, "|")
		}
		if got != tt.want {
			t.Errorf("%q = %q, want %q", tt.in, got, tt.want)
		}
	}
}
