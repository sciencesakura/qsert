package qsert

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// errUnsupported indicates that an expression cannot be evaluated.
var errUnsupported = errors.New("unsupported expression")

// errOverflow indicates that an integer result is out of the range of its type.
var errOverflow = errors.New("integer out of range")

type valueKind int

const (
	kindNull valueKind = iota
	kindString
	kindInt     // exact integer (scale 0) of an integer type
	kindDecimal // exact decimal number
	kindFloat   // approximate number
	kindBool
	kindBinary   // hexadecimal or bit-value literal, output as written
	kindVerbatim // a literal output as written, such as a UUID and a decimal number of CQL
)

type value struct {
	kind valueKind
	s    string  // kindString; source text for kindBinary
	n    decimal // kindInt and kindDecimal
	f    float64
	b    bool
	wide bool // PostgreSQL: bigint rather than integer
	prec int  // SQL Server: the precision of the decimal type, or 0 if it is the number of digits
	// rounded reports that the value is rounded for output and has more digits internally, as the quotient of MySQL's
	// division. It cannot be an operand, whose result would depend on the internal digits.
	rounded bool
}

var nullValue = value{kind: kindNull}

func (v value) isNumber() bool {
	return v.kind == kindInt || v.kind == kindDecimal || v.kind == kindFloat
}

func (v value) float() float64 {
	if v.kind == kindFloat {
		return v.f
	}
	return v.n.float()
}

// str returns a string value. Oracle treats an empty string as NULL.
func (d Dialect) str(s string) value {
	if s == "" && d == dOracle {
		return nullValue
	}
	return value{kind: kindString, s: s}
}

// integer returns an integer value, or errOverflow if it is out of the range of the integer type.
func (d Dialect) integer(z *big.Int, wide bool) (value, error) {
	switch d {
	case dMySQL, dSQLite, dSpanner:
		if !z.IsInt64() {
			return value{}, errOverflow
		}
	case dPostgres:
		if !z.IsInt64() || !wide && !isInt32(z) {
			return value{}, errOverflow
		}
	case dMSSQL:
		if !isInt32(z) {
			return value{}, errOverflow
		}
	case dOracle:
		return d.dec(decimal{z, 0})
	}
	return value{kind: kindInt, n: decimal{z, 0}, wide: wide}, nil
}

func isInt32(z *big.Int) bool {
	return z.IsInt64() && math.MinInt32 <= z.Int64() && z.Int64() <= math.MaxInt32
}

// dec returns an exact decimal value, normalized and checked against the maximum precision of the dialect.
func (d Dialect) dec(x decimal) (value, error) {
	switch d {
	case dMySQL:
		if x.digits() > 65 {
			return value{}, errUnsupported
		}
	case dMSSQL:
		if max(x.digits(), x.scale) > 38 {
			return value{}, errUnsupported
		}
	case dOracle:
		// Oracle's NUMBER has no scale; 1.50 is 1.5.
		if x = x.normalize(); x.digits() > 38 {
			return value{}, errUnsupported
		}
	case dSpanner:
		// NUMERIC has 29 digits before the decimal point and 9 after it, to which the results are rounded.
		if x = x.roundTo(9, false).normalize(); x.digits()-x.scale > 29 {
			return value{}, errUnsupported
		}
	case dSQLite:
		return d.float(x.float())
	}
	return value{kind: kindDecimal, n: x}, nil
}

// float returns an approximate number. Only the dialects whose floating-point output is known are supported.
func (d Dialect) float(f float64) (value, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return value{}, errUnsupported
	}
	switch d {
	case dMySQL, dPostgres, dSQLite, dSpanner:
		return value{kind: kindFloat, f: f}, nil
	}
	return value{}, errUnsupported
}

// render returns the text to be output for a non-NULL value.
func (d Dialect) render(v value) (string, error) {
	switch v.kind {
	case kindString, kindBinary, kindVerbatim:
		return v.s, nil
	case kindInt, kindDecimal:
		return v.n.String(), nil
	case kindFloat:
		switch d {
		case dMySQL:
			return mysqlFloat(v.f), nil
		case dPostgres:
			return pgFloat(v.f), nil
		case dSQLite:
			return sqliteFloat(v.f)
		case dSpanner:
			return spannerFloat(v.f), nil
		}
	case kindBool:
		switch d {
		case dPostgres:
			return boolText(v.b, "t", "f"), nil
		case dISO:
			return boolText(v.b, "TRUE", "FALSE"), nil
		case dCassandra, dSpanner:
			return boolText(v.b, "true", "false"), nil
		}
	}
	return "", errUnsupported
}

func boolText(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

// text converts a non-NULL value to a string as the dialect does implicitly or by a cast to a string type.
func (d Dialect) text(v value) (string, error) {
	switch v.kind {
	case kindString:
		return v.s, nil
	case kindInt, kindDecimal:
		if d == dOracle {
			// TO_CHAR omits the zero before the decimal point: .5
			s := v.n.String()
			if strings.HasPrefix(s, "0.") || strings.HasPrefix(s, "-0.") {
				s = strings.Replace(s, "0.", ".", 1)
			}
			return s, nil
		}
		return v.n.String(), nil
	case kindFloat:
		return d.render(v)
	case kindBool:
		if d == dPostgres {
			return strconv.FormatBool(v.b), nil
		}
	}
	return "", errUnsupported
}

// stringArg converts an argument of a string function to a string. PostgreSQL and standard SQL do not convert
// numbers to strings implicitly.
func (d Dialect) stringArg(v value) (string, error) {
	if v.kind != kindString && (d == dPostgres || d == dISO || d == dSpanner) {
		return "", errUnsupported
	}
	return d.text(v)
}

// intArg converts an integer argument of a function to int64.
func (d Dialect) intArg(v value) (int64, error) {
	var z *big.Int
	switch {
	case v.kind == kindInt:
		z = v.n.u
	case v.kind == kindDecimal && d == dMySQL:
		z = v.n.roundTo(0, false).u
	case v.kind == kindDecimal && d == dOracle:
		z = v.n.roundTo(0, true).u
	case v.kind == kindFloat && d == dMySQL:
		z, _ = big.NewFloat(math.Round(v.f)).Int(nil)
	case v.kind == kindFloat && d == dSQLite:
		z, _ = big.NewFloat(math.Trunc(v.f)).Int(nil)
	default:
		return 0, errUnsupported
	}
	if !z.IsInt64() {
		return 0, errUnsupported
	}
	return z.Int64(), nil
}

// mysqlFloat formats a floating-point number in the shortest representation, like MySQL: in fixed notation if the
// decimal exponent is from -15 to 14, and otherwise in scientific notation such as 1e15 and 1.5e-16.
func mysqlFloat(f float64) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0"
		}
		return "0"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	neg, digits, exp := floatDigits(s)
	if -15 <= exp && exp <= 14 {
		return layoutFloat(neg, digits, exp, false, false)
	}
	mant, _, _ := strings.Cut(s, "e")
	return mant + "e" + strconv.Itoa(exp)
}

// pgFloat formats a double precision number in the shortest representation, like PostgreSQL.
func pgFloat(f float64) string {
	if f == 0 {
		return "0"
	}
	neg, digits, exp := floatDigits(strconv.FormatFloat(f, 'e', -1, 64))
	return layoutFloat(neg, digits, exp, exp < -4 || exp >= 15, false)
}

// sqliteFloat formats a REAL like SQLite. SQLite uses 15 significant digits if they represent the number exactly,
// and otherwise an approximation of 17 digits, which is not reproduced here.
func sqliteFloat(f float64) (string, error) {
	if f == 0 {
		f = 0 // -0.0 is output as 0.0
	}
	s := strconv.FormatFloat(f, 'e', 14, 64)
	if g, _ := strconv.ParseFloat(s, 64); g != f {
		return "", errUnsupported
	}
	neg, digits, exp := floatDigits(s)
	return layoutFloat(neg, digits, exp, exp < -4 || exp >= 17, true), nil
}

// floatDigits splits a number formatted with the 'e' verb into its sign, significant digits without trailing zeros,
// and decimal exponent.
func floatDigits(s string) (neg bool, digits string, exp int) {
	neg = strings.HasPrefix(s, "-")
	mant, e, _ := strings.Cut(strings.TrimPrefix(s, "-"), "e")
	exp, _ = strconv.Atoi(e)
	digits = strings.TrimRight(strings.Replace(mant, ".", "", 1), "0")
	if digits == "" {
		digits = "0"
	}
	return neg, digits, exp
}

// layoutFloat formats significant digits d.ddd × 10^exp in scientific (d.ddde+XX) or fixed notation.
// If frac is true, at least one fractional digit is written.
func layoutFloat(neg bool, digits string, exp int, sci, frac bool) string {
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if sci {
		b.WriteString(digits[:1])
		if len(digits) > 1 || frac {
			b.WriteByte('.')
			b.WriteString(digits[1:])
			if len(digits) == 1 {
				b.WriteByte('0')
			}
		}
		b.WriteByte('e')
		if exp < 0 {
			b.WriteByte('-')
			exp = -exp
		} else {
			b.WriteByte('+')
		}
		if exp < 10 {
			b.WriteByte('0')
		}
		b.WriteString(strconv.Itoa(exp))
		return b.String()
	}
	switch {
	case exp < 0:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -exp-1))
		b.WriteString(digits)
	case exp+1 >= len(digits):
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", exp+1-len(digits)))
		if frac {
			b.WriteString(".0")
		}
	default:
		b.WriteString(digits[:exp+1])
		b.WriteByte('.')
		b.WriteString(digits[exp+1:])
	}
	return b.String()
}

// decimal is an exact decimal number u × 10^-scale.
type decimal struct {
	u     *big.Int
	scale int
}

func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

func decimalFromInt(n int64) decimal { return decimal{big.NewInt(n), 0} }

// parseDecimal parses an unsigned decimal literal such as 123, 1.50, .5 or 5.
func parseDecimal(s string) (decimal, bool) {
	intPart, frac, _ := strings.Cut(s, ".")
	u, ok := new(big.Int).SetString(intPart+frac, 10)
	if !ok {
		return decimal{}, false
	}
	return decimal{u, len(frac)}, true
}

func (x decimal) String() string {
	s := new(big.Int).Abs(x.u).String()
	if x.scale > 0 {
		if len(s) <= x.scale {
			s = strings.Repeat("0", x.scale-len(s)+1) + s
		}
		s = s[:len(s)-x.scale] + "." + s[len(s)-x.scale:]
	}
	if x.u.Sign() < 0 {
		s = "-" + s
	}
	return s
}

// digits returns the number of digits of the unscaled value.
func (x decimal) digits() int { return len(new(big.Int).Abs(x.u).String()) }

func (x decimal) isZero() bool { return x.u.Sign() == 0 }

func (x decimal) float() float64 {
	f, _ := new(big.Rat).SetFrac(x.u, pow10(x.scale)).Float64()
	return f
}

// normalize removes trailing zeros after the decimal point.
func (x decimal) normalize() decimal {
	u, s := new(big.Int).Set(x.u), x.scale
	ten, r := big.NewInt(10), new(big.Int)
	for s > 0 {
		q, _ := new(big.Int).QuoRem(u, ten, r)
		if r.Sign() != 0 {
			break
		}
		u, s = q, s-1
	}
	return decimal{u, s}
}

func (x decimal) rescale(scale int) decimal {
	if scale <= x.scale {
		return x
	}
	return decimal{new(big.Int).Mul(x.u, pow10(scale-x.scale)), scale}
}

// roundTo rounds (half away from zero) or truncates x to the given number of digits after the decimal point.
// A negative scale rounds digits to the left of the decimal point.
func (x decimal) roundTo(scale int, truncate bool) decimal {
	if scale >= x.scale {
		return x
	}
	var q *big.Int
	if truncate {
		q = new(big.Int).Quo(x.u, pow10(x.scale-scale))
	} else {
		q = divRound(x.u, pow10(x.scale-scale))
	}
	if scale < 0 {
		return decimal{q.Mul(q, pow10(-scale)), 0}
	}
	return decimal{q, scale}
}

// divRound returns x/y rounded half away from zero.
func divRound(x, y *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(x, y, new(big.Int))
	r.Abs(r).Lsh(r, 1)
	if r.Cmp(new(big.Int).Abs(y)) >= 0 {
		q.Add(q, big.NewInt(int64(x.Sign()*y.Sign())))
	}
	return q
}

func (x decimal) neg() decimal { return decimal{new(big.Int).Neg(x.u), x.scale} }

func (x decimal) abs() decimal { return decimal{new(big.Int).Abs(x.u), x.scale} }

func (x decimal) add(y decimal) decimal {
	s := max(x.scale, y.scale)
	return decimal{new(big.Int).Add(x.rescale(s).u, y.rescale(s).u), s}
}

func (x decimal) mul(y decimal) decimal {
	return decimal{new(big.Int).Mul(x.u, y.u), x.scale + y.scale}
}

// div divides x by a non-zero y and rounds the quotient to the given scale.
func (x decimal) div(y decimal, scale int) decimal {
	n := new(big.Int).Mul(x.u, pow10(y.scale+scale))
	m := new(big.Int).Mul(y.u, pow10(x.scale))
	return decimal{divRound(n, m), scale}
}

// pgDivScale returns the scale of the quotient of numeric division in PostgreSQL (select_div_scale), which gives at
// least 16 significant digits.
func (x decimal) pgDivScale(y decimal) int {
	w1, f1 := x.pgLeadingDigit()
	w2, f2 := y.pgLeadingDigit()
	q := w1 - w2
	if f1 <= f2 {
		q--
	}
	return min(max(16-q*4, x.scale, y.scale, 0), 1000)
}

// pgLeadingDigit returns the weight and value of the first non-zero digit of x in base 10000, as PostgreSQL stores
// numeric values.
func (x decimal) pgLeadingDigit() (weight int, digit int64) {
	if x.isZero() {
		return 0, 0
	}
	u := new(big.Int).Abs(x.u)
	e := len(u.String()) - 1 - x.scale // exponent of the most significant decimal digit
	weight = e / 4
	if e < 0 && e%4 != 0 {
		weight--
	}
	if k := x.scale + 4*weight; k >= 0 {
		u.Quo(u, pow10(k))
	} else {
		u.Mul(u, pow10(-k))
	}
	return weight, u.Int64()
}

// exactDiv divides x by a non-zero y exactly. It fails if the quotient does not terminate within maxDigits digits.
func (x decimal) exactDiv(y decimal, maxDigits int) (decimal, bool) {
	n := new(big.Int).Mul(x.u, pow10(y.scale))
	m := new(big.Int).Mul(y.u, pow10(x.scale))
	g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(n), new(big.Int).Abs(m))
	n.Quo(n, g)
	m.Quo(m, g)
	// The quotient terminates if the denominator has no prime factors other than 2 and 5.
	rest, twos, fives := new(big.Int).Abs(m), 0, 0
	for _, p := range []struct {
		n     int64
		count *int
	}{{2, &twos}, {5, &fives}} {
		r := new(big.Int)
		for {
			q, _ := new(big.Int).QuoRem(rest, big.NewInt(p.n), r)
			if r.Sign() != 0 {
				break
			}
			rest = q
			*p.count++
		}
	}
	if rest.Cmp(big.NewInt(1)) != 0 {
		return decimal{}, false
	}
	scale := max(twos, fives)
	q := decimal{n.Mul(n, pow10(scale)).Quo(n, m), scale}.normalize()
	if q.digits() > maxDigits {
		return decimal{}, false
	}
	return q, true
}

// intDiv returns the integer quotient of x divided by a non-zero y, truncated toward zero.
func (x decimal) intDiv(y decimal) *big.Int {
	n := new(big.Int).Mul(x.u, pow10(y.scale))
	m := new(big.Int).Mul(y.u, pow10(x.scale))
	return n.Quo(n, m)
}

// mod returns the remainder of x divided by a non-zero y, which has the sign of x.
func (x decimal) mod(y decimal) decimal {
	s := max(x.scale, y.scale)
	return decimal{new(big.Int).Rem(x.rescale(s).u, y.rescale(s).u), s}
}

// toInt rounds x toward negative infinity (dir < 0) or positive infinity (dir > 0).
func (x decimal) toInt(dir int) *big.Int {
	q, r := new(big.Int).QuoRem(x.u, pow10(x.scale), new(big.Int))
	if r.Sign() == dir {
		q.Add(q, big.NewInt(int64(dir)))
	}
	return q
}

// mssqlPrecision returns the precision of the decimal type of a number in SQL Server. An int is converted to
// decimal(10, 0), and a literal has the precision of its digits.
func (v value) mssqlPrecision() int {
	switch {
	case v.kind == kindInt:
		return 10
	case v.prec > 0:
		return v.prec
	}
	return max(v.n.digits(), v.n.scale)
}
