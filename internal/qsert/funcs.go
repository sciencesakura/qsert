package qsert

import (
	"math"
	"math/big"
	"strings"
	"unicode/utf8"
)

// maxResultLen limits the length of a string built by functions such as REPEAT and LPAD.
const maxResultLen = 1 << 24

type function struct {
	minArgs     int
	maxArgs     int  // negative if variadic
	handlesNull bool // if false, the result is NULL when any argument is NULL
	call        func(d Dialect, args []value) (value, error)
}

func fn(minArgs, maxArgs int, call func(Dialect, []value) (value, error)) *function {
	return &function{minArgs, maxArgs, false, call}
}

func nullFn(minArgs, maxArgs int, call func(Dialect, []value) (value, error)) *function {
	return &function{minArgs, maxArgs, true, call}
}

// functions are the deterministic functions that can be evaluated in each dialect.
var functions = [...]map[string]*function{
	dISO: {
		"UPPER":            fn(1, 1, fnUpper),
		"LOWER":            fn(1, 1, fnLower),
		"TRIM":             fn(1, 1, trimFunc(strings.Trim)),
		"CHAR_LENGTH":      fn(1, 1, fnCharLength),
		"CHARACTER_LENGTH": fn(1, 1, fnCharLength),
		"ABS":              fn(1, 1, fnAbs),
		"CEIL":             fn(1, 1, fnCeil),
		"CEILING":          fn(1, 1, fnCeil),
		"FLOOR":            fn(1, 1, fnFloor),
		"MOD":              fn(2, 2, fnMod),
		"COALESCE":         nullFn(2, -1, fnCoalesce),
	},
	dMySQL: {
		"CONCAT":           fn(1, -1, fnConcat),
		"CONCAT_WS":        nullFn(2, -1, fnConcatWS),
		"UPPER":            fn(1, 1, fnUpper),
		"UCASE":            fn(1, 1, fnUpper),
		"LOWER":            fn(1, 1, fnLower),
		"LCASE":            fn(1, 1, fnLower),
		"TRIM":             fn(1, 1, trimFunc(strings.Trim)),
		"LTRIM":            fn(1, 1, trimFunc(strings.TrimLeft)),
		"RTRIM":            fn(1, 1, trimFunc(strings.TrimRight)),
		"REVERSE":          fn(1, 1, fnReverse),
		"REPLACE":          fn(3, 3, fnReplace),
		"SUBSTRING":        fn(2, 3, fnSubstring),
		"SUBSTR":           fn(2, 3, fnSubstring),
		"MID":              fn(3, 3, fnSubstring),
		"LEFT":             fn(2, 2, fnLeft),
		"RIGHT":            fn(2, 2, fnRight),
		"REPEAT":           fn(2, 2, fnRepeat),
		"LPAD":             fn(3, 3, padFunc(true)),
		"RPAD":             fn(3, 3, padFunc(false)),
		"CHAR_LENGTH":      fn(1, 1, fnCharLength),
		"CHARACTER_LENGTH": fn(1, 1, fnCharLength),
		"LENGTH":           fn(1, 1, fnOctetLength),
		"OCTET_LENGTH":     fn(1, 1, fnOctetLength),
		"ABS":              fn(1, 1, fnAbs),
		"SIGN":             fn(1, 1, fnSign),
		"CEIL":             fn(1, 1, fnCeil),
		"CEILING":          fn(1, 1, fnCeil),
		"FLOOR":            fn(1, 1, fnFloor),
		"ROUND":            fn(1, 2, roundFunc(false)),
		"TRUNCATE":         fn(2, 2, roundFunc(true)),
		"MOD":              fn(2, 2, fnMod),
		"COALESCE":         nullFn(1, -1, fnCoalesce),
		"IFNULL":           nullFn(2, 2, fnCoalesce),
	},
	dPostgres: {
		"CONCAT":           nullFn(1, -1, fnConcat),
		"CONCAT_WS":        nullFn(2, -1, fnConcatWS),
		"UPPER":            fn(1, 1, fnUpper),
		"LOWER":            fn(1, 1, fnLower),
		"TRIM":             fn(1, 2, trimFunc(strings.Trim)),
		"BTRIM":            fn(1, 2, trimFunc(strings.Trim)),
		"LTRIM":            fn(1, 2, trimFunc(strings.TrimLeft)),
		"RTRIM":            fn(1, 2, trimFunc(strings.TrimRight)),
		"REVERSE":          fn(1, 1, fnReverse),
		"REPLACE":          fn(3, 3, fnReplace),
		"SUBSTRING":        fn(2, 3, fnSubstring),
		"SUBSTR":           fn(2, 3, fnSubstring),
		"LEFT":             fn(2, 2, fnLeft),
		"RIGHT":            fn(2, 2, fnRight),
		"REPEAT":           fn(2, 2, fnRepeat),
		"LPAD":             fn(2, 3, padFunc(true)),
		"RPAD":             fn(2, 3, padFunc(false)),
		"CHAR_LENGTH":      fn(1, 1, fnCharLength),
		"CHARACTER_LENGTH": fn(1, 1, fnCharLength),
		"LENGTH":           fn(1, 1, fnCharLength),
		"OCTET_LENGTH":     fn(1, 1, fnOctetLength),
		"ABS":              fn(1, 1, fnAbs),
		"SIGN":             fn(1, 1, fnSign),
		"CEIL":             fn(1, 1, fnCeil),
		"CEILING":          fn(1, 1, fnCeil),
		"FLOOR":            fn(1, 1, fnFloor),
		"ROUND":            fn(1, 2, roundFunc(false)),
		"TRUNC":            fn(1, 2, roundFunc(true)),
		"MOD":              fn(2, 2, fnMod),
		"COALESCE":         nullFn(1, -1, fnCoalesce),
	},
	dSQLite: {
		"CONCAT":       nullFn(1, -1, fnConcat),
		"CONCAT_WS":    nullFn(2, -1, fnConcatWS),
		"UPPER":        fn(1, 1, fnUpper),
		"LOWER":        fn(1, 1, fnLower),
		"TRIM":         fn(1, 2, trimFunc(strings.Trim)),
		"LTRIM":        fn(1, 2, trimFunc(strings.TrimLeft)),
		"RTRIM":        fn(1, 2, trimFunc(strings.TrimRight)),
		"REPLACE":      fn(3, 3, fnReplace),
		"SUBSTR":       fn(2, 3, fnSubstring),
		"SUBSTRING":    fn(2, 3, fnSubstring),
		"LENGTH":       fn(1, 1, fnCharLength),
		"OCTET_LENGTH": fn(1, 1, fnOctetLength),
		"ABS":          fn(1, 1, fnAbs),
		"SIGN":         fn(1, 1, fnSign),
		"CEIL":         fn(1, 1, fnCeil),
		"CEILING":      fn(1, 1, fnCeil),
		"FLOOR":        fn(1, 1, fnFloor),
		"ROUND":        fn(1, 2, roundFunc(false)),
		"TRUNC":        fn(1, 1, roundFunc(true)),
		"MOD":          fn(2, 2, fnMod),
		"COALESCE":     nullFn(2, -1, fnCoalesce),
		"IFNULL":       nullFn(2, 2, fnCoalesce),
	},
	dMSSQL: {
		"CONCAT":    nullFn(2, -1, fnConcat),
		"CONCAT_WS": nullFn(3, -1, fnConcatWS),
		"UPPER":     fn(1, 1, fnUpper),
		"LOWER":     fn(1, 1, fnLower),
		"TRIM":      fn(1, 1, trimFunc(strings.Trim)),
		"LTRIM":     fn(1, 1, trimFunc(strings.TrimLeft)),
		"RTRIM":     fn(1, 1, trimFunc(strings.TrimRight)),
		"REVERSE":   fn(1, 1, fnReverse),
		"REPLACE":   fn(3, 3, fnReplace),
		"SUBSTRING": fn(3, 3, fnSubstring),
		"LEFT":      fn(2, 2, fnLeft),
		"RIGHT":     fn(2, 2, fnRight),
		"REPLICATE": fn(2, 2, fnRepeat),
		"LEN":       fn(1, 1, fnLen),
		"ABS":       fn(1, 1, fnAbs),
		"SIGN":      fn(1, 1, fnSign),
		"CEILING":   fn(1, 1, fnCeil),
		"FLOOR":     fn(1, 1, fnFloor),
		"ROUND":     fn(2, 3, roundFunc(false)),
		"COALESCE":  nullFn(2, -1, fnCoalesce),
	},
	dOracle: {
		"CONCAT":   nullFn(2, -1, fnConcat), // more than two arguments since 23ai
		"UPPER":    fn(1, 1, fnUpper),
		"LOWER":    fn(1, 1, fnLower),
		"TRIM":     fn(1, 1, trimFunc(strings.Trim)),
		"LTRIM":    fn(1, 2, trimFunc(strings.TrimLeft)),
		"RTRIM":    fn(1, 2, trimFunc(strings.TrimRight)),
		"REPLACE":  nullFn(2, 3, fnReplace),
		"SUBSTR":   fn(2, 3, fnSubstring),
		"LENGTH":   fn(1, 1, fnCharLength),
		"LPAD":     fn(2, 3, padFunc(true)),
		"RPAD":     fn(2, 3, padFunc(false)),
		"ABS":      fn(1, 1, fnAbs),
		"SIGN":     fn(1, 1, fnSign),
		"CEIL":     fn(1, 1, fnCeil),
		"FLOOR":    fn(1, 1, fnFloor),
		"ROUND":    fn(1, 2, roundFunc(false)),
		"TRUNC":    fn(1, 2, roundFunc(true)),
		"MOD":      fn(2, 2, fnMod),
		"COALESCE": nullFn(2, -1, fnCoalesce),
		"NVL":      nullFn(2, 2, fnCoalesce),
	},
	dCassandra: {},
	dSpanner: {
		"CONCAT":           nullFn(1, -1, fnSpannerConcat),
		"UPPER":            fn(1, 1, fnUpper),
		"LOWER":            fn(1, 1, fnLower),
		"TRIM":             fn(1, 2, trimFunc(strings.Trim)),
		"LTRIM":            fn(1, 2, trimFunc(strings.TrimLeft)),
		"RTRIM":            fn(1, 2, trimFunc(strings.TrimRight)),
		"REVERSE":          fn(1, 1, fnReverse),
		"REPLACE":          fn(3, 3, fnReplace),
		"SUBSTR":           fn(2, 3, fnSubstring),
		"SUBSTRING":        fn(2, 3, fnSubstring),
		"REPEAT":           fn(2, 2, fnRepeat),
		"LPAD":             fn(2, 3, padFunc(true)),
		"RPAD":             fn(2, 3, padFunc(false)),
		"LENGTH":           fn(1, 1, fnCharLength),
		"CHAR_LENGTH":      fn(1, 1, fnCharLength),
		"CHARACTER_LENGTH": fn(1, 1, fnCharLength),
		"BYTE_LENGTH":      fn(1, 1, fnOctetLength),
		"ABS":              fn(1, 1, fnAbs),
		"SIGN":             fn(1, 1, fnSign),
		"CEIL":             fn(1, 1, fnCeil),
		"CEILING":          fn(1, 1, fnCeil),
		"FLOOR":            fn(1, 1, fnFloor),
		"ROUND":            fn(1, 2, roundFunc(false)),
		"TRUNC":            fn(1, 2, roundFunc(true)),
		"MOD":              fn(2, 2, fnMod),
		"DIV":              fn(2, 2, fnDiv),
		"COALESCE":         nullFn(1, -1, fnCoalesce),
		"IFNULL":           nullFn(2, 2, fnCoalesce),
	},
}

func intValue(d Dialect, n int64) (value, error) { return d.integer(big.NewInt(n), false) }

// fnConcat implements CONCAT, which returns NULL if any argument is NULL in MySQL, ignores NULL in PostgreSQL, SQLite
// and SQL Server, and treats NULL as an empty string in Oracle.
func fnConcat(d Dialect, args []value) (value, error) {
	parts, err := d.concatArgs(args)
	if err != nil {
		return value{}, err
	}
	return d.str(strings.Join(parts, "")), nil
}

func fnConcatWS(d Dialect, args []value) (value, error) {
	if args[0].kind == kindNull {
		if d == dMSSQL {
			return value{}, errUnsupported
		}
		return nullValue, nil
	}
	sep, err := d.text(args[0])
	if err != nil {
		return value{}, err
	}
	parts, err := d.concatArgs(args[1:])
	if err != nil {
		return value{}, err
	}
	return d.str(strings.Join(parts, sep)), nil
}

// concatArgs converts the non-NULL arguments of CONCAT and CONCAT_WS to strings. PostgreSQL converts them with the
// output functions, in which true is t.
func (d Dialect) concatArgs(args []value) ([]string, error) {
	var parts []string
	for _, a := range args {
		if a.kind == kindNull {
			continue
		}
		var s string
		var err error
		if d == dPostgres {
			s, err = d.render(a)
		} else {
			s, err = d.text(a)
		}
		if err != nil {
			return nil, err
		}
		parts = append(parts, s)
	}
	return parts, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func caseFunc(d Dialect, args []value, unicode func(string) string, ascii func(byte) byte) (value, error) {
	s, err := d.stringArg(args[0])
	if err != nil {
		return value{}, err
	}
	switch {
	case d.unicodeCaseMapping() || isASCII(s):
		return d.str(unicode(s)), nil
	case d == dSQLite:
		b := []byte(s)
		for i, c := range b {
			b[i] = ascii(c)
		}
		return d.str(string(b)), nil
	}
	return value{}, errUnsupported
}

func fnUpper(d Dialect, args []value) (value, error) {
	return caseFunc(d, args, strings.ToUpper, func(c byte) byte {
		if 'a' <= c && c <= 'z' {
			return c - 'a' + 'A'
		}
		return c
	})
}

func fnLower(d Dialect, args []value) (value, error) {
	return caseFunc(d, args, strings.ToLower, func(c byte) byte {
		if 'A' <= c && c <= 'Z' {
			return c - 'A' + 'a'
		}
		return c
	})
}

// trimFunc returns a function removing spaces, or the characters in the second argument, from a string.
func trimFunc(trim func(string, string) string) func(Dialect, []value) (value, error) {
	return func(d Dialect, args []value) (value, error) {
		s, err := d.stringArg(args[0])
		if err != nil {
			return value{}, err
		}
		chars := " "
		if len(args) == 2 {
			if chars, err = d.stringArg(args[1]); err != nil {
				return value{}, err
			}
		}
		return d.str(trim(s, chars)), nil
	}
}

func fnReverse(d Dialect, args []value) (value, error) {
	s, err := d.stringArg(args[0])
	if err != nil {
		return value{}, err
	}
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return d.str(string(r)), nil
}

func fnReplace(d Dialect, args []value) (value, error) {
	var s [3]string
	for i, a := range args {
		if a.kind == kindNull {
			// Only Oracle handles NULL by itself: REPLACE(s, NULL) is s and REPLACE(s, x, NULL) removes x.
			if i == 0 {
				return nullValue, nil
			}
			continue
		}
		var err error
		if s[i], err = d.stringArg(a); err != nil {
			return value{}, err
		}
	}
	if s[1] == "" {
		return d.str(s[0]), nil
	}
	if d == dMSSQL && strings.ToUpper(s[1]) != strings.ToLower(s[1]) {
		// The comparison depends on the collation, which is usually case-insensitive.
		return value{}, errUnsupported
	}
	return d.str(strings.ReplaceAll(s[0], s[1], s[2])), nil
}

func fnSubstring(d Dialect, args []value) (value, error) {
	s, err := d.stringArg(args[0])
	if err != nil {
		return value{}, err
	}
	pos, err := d.intArg(args[1])
	if err != nil {
		return value{}, err
	}
	length := int64(math.MaxInt64)
	if len(args) == 3 {
		if length, err = d.intArg(args[2]); err != nil {
			return value{}, err
		}
	}
	r := []rune(s)
	n := int64(len(r))
	var start, end int64 // 0-based, end exclusive
	switch d {
	case dMySQL:
		switch {
		case pos > 0:
			start = pos - 1
		case pos < 0:
			start = n + pos
		default:
			return d.str(""), nil
		}
		if start < 0 || length <= 0 {
			return d.str(""), nil
		}
		end = start + min(length, n)
	case dPostgres, dMSSQL:
		// Characters at the positions [pos, pos+length) are returned, where the positions may be outside the string.
		if length < 0 {
			return value{}, errUnsupported
		}
		start = pos - 1
		end = start + min(length, n+1)
		if length == math.MaxInt64 {
			end = n
		}
	case dSQLite:
		start, end = sqliteSubstr(n, pos, length, len(args) == 3)
	case dSpanner:
		// A position of 0 is 1, and a negative position counts from the end.
		if length < 0 {
			return value{}, errUnsupported
		}
		switch {
		case pos > 0:
			start = pos - 1
		case pos < 0:
			start = max(n+pos, 0)
		}
		end = start + min(length, n)
	case dOracle:
		switch {
		case pos > 0:
			start = pos - 1
		case pos < 0:
			start = n + pos
		}
		if start < 0 || length < 1 {
			return nullValue, nil
		}
		end = start + min(length, n)
	}
	start, end = max(start, 0), min(end, n)
	if start >= end {
		return d.str(""), nil
	}
	return d.str(string(r[start:end])), nil
}

// sqliteSubstr returns the range of characters returned by SQLite's substr, ported from its implementation.
func sqliteSubstr(n, p1, p2 int64, hasLength bool) (start, end int64) {
	const limit = 1 << 40 // large enough not to overflow, like SQLITE_LIMIT_LENGTH
	negP2 := false
	if !hasLength {
		p2 = limit
	} else if p2 < 0 {
		p2, negP2 = -max(p2, -limit), true
	}
	p1, p2 = max(-limit, min(p1, limit)), min(p2, limit)
	switch {
	case p1 < 0:
		p1 += n
		if p1 < 0 {
			p2 = max(p2+p1, 0)
			p1 = 0
		}
	case p1 > 0:
		p1--
	case p2 > 0:
		p2--
	}
	if negP2 {
		p1 -= p2
		if p1 < 0 {
			p2 += p1
			p1 = 0
		}
	}
	return p1, p1 + p2
}

func fnLeft(d Dialect, args []value) (value, error) {
	s, k, err := stringAndInt(d, args)
	if err != nil {
		return value{}, err
	}
	r := []rune(s)
	n := int64(len(r))
	if k < 0 {
		if d != dPostgres {
			return mssqlNegative(d)
		}
		k = n + k // all but the last |k| characters
	}
	return d.str(string(r[:max(0, min(k, n))])), nil
}

func fnRight(d Dialect, args []value) (value, error) {
	s, k, err := stringAndInt(d, args)
	if err != nil {
		return value{}, err
	}
	r := []rune(s)
	n := int64(len(r))
	if k < 0 {
		if d != dPostgres {
			return mssqlNegative(d)
		}
		k = n + k // all but the first |k| characters
	}
	return d.str(string(r[n-max(0, min(k, n)):])), nil
}

// mssqlNegative returns the result of LEFT or RIGHT with a negative length: an error in SQL Server and an empty
// string in MySQL.
func mssqlNegative(d Dialect) (value, error) {
	if d == dMSSQL {
		return value{}, errUnsupported
	}
	return d.str(""), nil
}

func stringAndInt(d Dialect, args []value) (string, int64, error) {
	s, err := d.stringArg(args[0])
	if err != nil {
		return "", 0, err
	}
	n, err := d.intArg(args[1])
	return s, n, err
}

// fnRepeat implements REPEAT and SQL Server's REPLICATE.
func fnRepeat(d Dialect, args []value) (value, error) {
	s, n, err := stringAndInt(d, args)
	if err != nil {
		return value{}, err
	}
	switch {
	case n < 0 && d == dMSSQL:
		return nullValue, nil
	case n < 0 && d == dSpanner:
		return value{}, errUnsupported
	}
	if n <= 0 || s == "" {
		return d.str(""), nil
	}
	if n > maxResultLen/int64(len(s)) {
		return value{}, errUnsupported
	}
	return d.str(strings.Repeat(s, int(n))), nil
}

func padFunc(left bool) func(Dialect, []value) (value, error) {
	return func(d Dialect, args []value) (value, error) {
		s, n, err := stringAndInt(d, args)
		if err != nil {
			return value{}, err
		}
		pad := " "
		if len(args) == 3 {
			if pad, err = d.stringArg(args[2]); err != nil {
				return value{}, err
			}
		}
		if d == dOracle && !isASCII(s+pad) {
			// Oracle pads to the display width, in which a wide character is counted as two.
			return value{}, errUnsupported
		}
		switch {
		case (n < 0 || pad == "") && d == dSpanner:
			return value{}, errUnsupported
		case n < 0 && d == dPostgres:
			n = 0
		case n < 0 || n == 0 && d == dOracle:
			return nullValue, nil
		}
		r := []rune(s)
		if n <= int64(len(r)) {
			return d.str(string(r[:n])), nil
		}
		if pad == "" {
			// PostgreSQL returns the string as it is, and MySQL returns an empty string.
			if d == dPostgres {
				return d.str(s), nil
			}
			return d.str(""), nil
		}
		if n > maxResultLen {
			return value{}, errUnsupported
		}
		p := []rune(strings.Repeat(pad, int(n)/utf8.RuneCountInString(pad)+1))[:n-int64(len(r))]
		if left {
			return d.str(string(p) + s), nil
		}
		return d.str(s + string(p)), nil
	}
}

func lengthValue(d Dialect, args []value, length func(string) int) (value, error) {
	s, err := d.stringArg(args[0])
	if err != nil {
		return value{}, err
	}
	return intValue(d, int64(length(s)))
}

func fnCharLength(d Dialect, args []value) (value, error) {
	return lengthValue(d, args, utf8.RuneCountInString)
}

func fnOctetLength(d Dialect, args []value) (value, error) {
	return lengthValue(d, args, func(s string) int { return len(s) })
}

// fnLen implements SQL Server's LEN, which excludes trailing spaces.
func fnLen(d Dialect, args []value) (value, error) {
	return lengthValue(d, args, func(s string) int { return utf8.RuneCountInString(strings.TrimRight(s, " ")) })
}

func fnAbs(d Dialect, args []value) (value, error) {
	switch a := args[0]; a.kind {
	case kindInt:
		return d.integer(new(big.Int).Abs(a.n.u), a.wide)
	case kindDecimal:
		return d.dec(a.n.abs())
	case kindFloat:
		return d.float(math.Abs(a.f))
	}
	return value{}, errUnsupported
}

func fnSign(d Dialect, args []value) (value, error) {
	var sign int
	switch a := args[0]; a.kind {
	case kindInt, kindDecimal:
		sign = a.n.u.Sign()
		if a.kind == kindDecimal && d == dMSSQL {
			// The result has the type of the argument: SIGN(-1.50) is -1.00.
			return d.dec(decimalFromInt(int64(sign)).rescale(a.n.scale))
		}
	case kindFloat:
		switch {
		case a.f > 0:
			sign = 1
		case a.f < 0:
			sign = -1
		}
	default:
		return value{}, errUnsupported
	}
	return intValue(d, int64(sign))
}

func ceilFloor(d Dialect, args []value, dir int, f func(float64) float64) (value, error) {
	switch a := args[0]; a.kind {
	case kindInt:
		if d == dSpanner {
			// INT64 is converted to FLOAT64.
			return d.float(f(a.float()))
		}
		return a, nil
	case kindDecimal:
		z := a.n.toInt(dir)
		if d == dMySQL {
			return d.integer(z, false)
		}
		return d.dec(decimal{z, 0})
	case kindFloat:
		return d.float(f(a.f))
	}
	return value{}, errUnsupported
}

func fnCeil(d Dialect, args []value) (value, error) { return ceilFloor(d, args, 1, math.Ceil) }

func fnFloor(d Dialect, args []value) (value, error) { return ceilFloor(d, args, -1, math.Floor) }

// roundFunc implements ROUND, and TRUNCATE or TRUNC if truncateFunc is true.
func roundFunc(truncateFunc bool) func(Dialect, []value) (value, error) {
	return func(d Dialect, args []value) (value, error) {
		truncate := truncateFunc
		var places int64
		if len(args) >= 2 {
			var err error
			if places, err = d.intArg(args[1]); err != nil {
				return value{}, err
			}
			places = max(-100, min(places, 100))
		}
		if len(args) == 3 { // SQL Server: ROUND(x, d, 1) truncates
			f, err := d.intArg(args[2])
			if err != nil {
				return value{}, err
			}
			truncate = f != 0
		}
		x := args[0]
		switch d {
		case dSQLite:
			return sqliteRound(d, x, places, truncate)
		case dSpanner:
			return spannerRound(x, places, len(args) == 2, truncate)
		}
		switch x.kind {
		case kindInt, kindDecimal:
			z := x.n.roundTo(int(places), truncate)
			switch d {
			case dPostgres:
				// The scale of the result is the specified number of places, if any: ROUND(1.5, 3) is 1.500.
				if len(args) == 2 || x.kind == kindDecimal {
					return d.dec(z.rescale(int(places)))
				}
			case dMSSQL:
				// The type of the argument is kept: ROUND(2.345, 2) is 2.350, and ROUND(9.9, 0) overflows.
				z = z.rescale(x.n.scale)
				if x.kind == kindDecimal && z.digits() > x.mssqlPrecision() {
					return value{}, errUnsupported
				}
				if x.kind == kindDecimal {
					v, err := d.dec(z)
					v.prec = x.prec
					return v, err
				}
			}
			if x.kind == kindInt {
				return d.integer(z.u, x.wide)
			}
			return d.dec(z)
		case kindFloat:
			switch {
			case d == dMySQL:
				p := math.Pow(10, float64(places))
				if truncate {
					return d.float(math.Trunc(x.f*p) / p)
				}
				return d.float(math.RoundToEven(x.f*p) / p)
			case d == dPostgres && len(args) == 1:
				if truncate {
					return d.float(math.Trunc(x.f))
				}
				return d.float(math.RoundToEven(x.f))
			}
		}
		return value{}, errUnsupported
	}
}

// sqliteRound implements SQLite's round and trunc, which return a REAL except for trunc of an integer.
func sqliteRound(d Dialect, x value, places int64, truncate bool) (value, error) {
	if !x.isNumber() {
		return value{}, errUnsupported
	}
	if truncate {
		if x.kind == kindInt {
			return x, nil
		}
		return d.float(math.Trunc(x.f))
	}
	f := x.float()
	places = max(0, min(places, 30))
	switch {
	case math.Abs(f) > 4503599627370496:
		return d.float(f)
	case places == 0:
		return d.float(float64(int64(f + math.Copysign(0.5, f))))
	}
	// SQLite formats the number with the given number of decimal places and converts it back.
	exact := new(big.Rat)
	new(big.Float).SetFloat64(f).Rat(exact)
	z := new(big.Rat).Mul(exact, new(big.Rat).SetInt(pow10(int(places))))
	q := divRound(z.Num(), z.Denom())
	r, _ := new(big.Rat).SetFrac(q, pow10(int(places))).Float64()
	return d.float(r)
}

func fnMod(d Dialect, args []value) (value, error) {
	x, y := args[0], args[1]
	switch d {
	case dSQLite:
		// SQLite's mod is a math function that returns a REAL.
		if !x.isNumber() || !y.isNumber() {
			return value{}, errUnsupported
		}
		if y.float() == 0 {
			return nullValue, nil
		}
		return d.float(math.Mod(x.float(), y.float()))
	case dOracle:
		if x.kind != kindDecimal || y.kind != kindDecimal {
			return value{}, errUnsupported
		}
		if y.n.isZero() {
			return x, nil
		}
		return d.dec(x.n.mod(y.n))
	case dISO:
		if x.kind != kindInt || y.kind != kindInt || y.n.isZero() {
			return value{}, errUnsupported
		}
	}
	return d.binary(opMod, x, y)
}

func fnCoalesce(_ Dialect, args []value) (value, error) {
	for _, a := range args {
		if a.kind != kindNull {
			return a, nil
		}
	}
	return nullValue, nil
}
