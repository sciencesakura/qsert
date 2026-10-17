package qsert

import (
	"math"
	"math/big"
)

// unary applies a unary operator.
func (d Dialect) unary(op string, x value) (value, error) {
	if d == dCassandra || d == dSpanner && x.kind == kindNull {
		return value{}, errUnsupported
	}
	if x.kind == kindNull {
		return x, nil
	}
	switch op {
	case "-":
		switch x.kind {
		case kindInt:
			v, err := d.integer(new(big.Int).Neg(x.n.u), x.wide)
			if err == errOverflow && d == dSQLite {
				return d.float(-x.float())
			}
			return v, err
		case kindDecimal:
			return d.dec(x.n.neg())
		case kindFloat:
			return d.float(-x.f)
		}
	case "+":
		if x.isNumber() {
			return x, nil
		}
	case "~":
		return d.bitwise("~", value{}, x)
	}
	return value{}, errUnsupported
}

// binary applies a binary operator.
func (d Dialect) binary(op string, l, r value) (value, error) {
	if d == dCassandra {
		// The types of the operands of CQL are inferred from the columns.
		return value{}, errUnsupported
	}
	if d == dSpanner && (l.kind == kindNull || r.kind == kindNull) {
		// The type of a NULL literal cannot be inferred, which is an error.
		return value{}, errUnsupported
	}
	switch op {
	case opConcat:
		return d.concat(l, r)
	case opBitOr, opBitAnd, opBitXor, opShl, opShr:
		if l.kind == kindNull || r.kind == kindNull {
			return nullValue, nil
		}
		return d.bitwise(op, l, r)
	case opPow:
		return d.power(l, r)
	}
	if l.kind == kindNull || r.kind == kindNull {
		return nullValue, nil
	}
	if op == opAdd && d == dMSSQL && l.kind == kindString && r.kind == kindString {
		return d.str(l.s + r.s), nil
	}
	if !l.isNumber() || !r.isNumber() {
		return value{}, errUnsupported
	}
	if l.kind == kindFloat || r.kind == kindFloat {
		return d.floatArith(op, l.float(), r.float())
	}
	if l.kind == kindInt && r.kind == kindInt {
		return d.intArith(op, l, r)
	}
	if d == dMSSQL {
		return mssqlDecimalArith(op, l, r)
	}
	return d.decimalArith(op, l.n, r.n)
}

// mssqlDecimalArith applies an arithmetic operator to decimal numbers of SQL Server, whose result type is derived from
// the precisions and scales of the operands. It fails if the precision of the result type exceeds 38, in which case
// SQL Server reduces the scale.
func mssqlDecimalArith(op string, l, r value) (value, error) {
	p1, s1 := l.mssqlPrecision(), l.n.scale
	p2, s2 := r.mssqlPrecision(), r.n.scale
	var p int
	switch op {
	case opAdd, opSub:
		p = max(p1-s1, p2-s2) + max(s1, s2) + 1
	case opMul:
		p = p1 + p2 + 1
	default:
		return value{}, errUnsupported
	}
	if p > 38 {
		return value{}, errUnsupported
	}
	v, err := dMSSQL.decimalArith(op, l.n, r.n)
	v.prec = p
	return v, err
}

// divisionByZero returns the result of division by zero: NULL in SQLite, and an error elsewhere (MySQL also raises an
// error in INSERT statements in the default strict mode).
func (d Dialect) divisionByZero() (value, error) {
	if d == dSQLite {
		return nullValue, nil
	}
	return value{}, errUnsupported
}

func (d Dialect) floatArith(op string, x, y float64) (value, error) {
	switch op {
	case opAdd:
		return d.float(x + y)
	case opSub:
		return d.float(x - y)
	case opMul:
		return d.float(x * y)
	case opDiv:
		if y == 0 {
			return d.divisionByZero()
		}
		return d.float(x / y)
	case opMod:
		switch d {
		case dMySQL:
			if y == 0 {
				return d.divisionByZero()
			}
			return d.float(math.Mod(x, y))
		case dSQLite:
			// SQLite casts the operands to integers.
			if math.Abs(x) >= 1<<63 || math.Abs(y) >= 1<<63 {
				return value{}, errUnsupported
			}
			a, b := int64(x), int64(y)
			if b == 0 {
				return nullValue, nil
			}
			if b == -1 {
				return d.float(0)
			}
			return d.float(float64(a % b))
		}
	case opIntDiv:
		if y == 0 {
			return d.divisionByZero()
		}
		z, _ := big.NewFloat(math.Trunc(x / y)).Int(nil)
		return d.integer(z, false)
	}
	return value{}, errUnsupported
}

func (d Dialect) intArith(op string, l, r value) (value, error) {
	x, y := l.n.u, r.n.u
	wide := l.wide || r.wide
	var z *big.Int
	switch op {
	case opAdd:
		z = new(big.Int).Add(x, y)
	case opSub:
		z = new(big.Int).Sub(x, y)
	case opMul:
		z = new(big.Int).Mul(x, y)
	case opDiv:
		switch d {
		case dMySQL:
			// MySQL's division always returns a decimal.
			return d.decimalArith(op, l.n, r.n)
		case dISO:
			// The precision and scale of the result are implementation-defined.
			return value{}, errUnsupported
		case dSpanner:
			// The division of INT64 returns FLOAT64.
			if y.Sign() == 0 {
				return d.divisionByZero()
			}
			return d.float(l.float() / r.float())
		}
		if y.Sign() == 0 {
			return d.divisionByZero()
		}
		z = new(big.Int).Quo(x, y)
	case opMod, opIntDiv:
		if y.Sign() == 0 {
			return d.divisionByZero()
		}
		if op == opMod {
			z = new(big.Int).Rem(x, y)
		} else {
			z = new(big.Int).Quo(x, y)
		}
	default:
		return value{}, errUnsupported
	}
	v, err := d.integer(z, wide)
	if err == errOverflow && d == dSQLite {
		// SQLite converts the result to REAL on overflow.
		return d.floatArith(op, l.float(), r.float())
	}
	return v, err
}

func (d Dialect) decimalArith(op string, x, y decimal) (value, error) {
	var z decimal
	switch op {
	case opAdd:
		z = x.add(y)
	case opSub:
		z = x.add(y.neg())
	case opMul:
		z = x.mul(y)
		if d == dMySQL {
			z = z.roundTo(30, false)
		}
	case opDiv:
		if y.isZero() {
			return d.divisionByZero()
		}
		switch d {
		case dMySQL:
			// The scale is increased by div_precision_increment (default 4) for output, and the quotient has more
			// digits internally.
			v, err := d.dec(x.div(y, min(x.scale+4, 30)))
			v.rounded = true
			return v, err
		case dPostgres:
			z = x.div(y, x.pgDivScale(y))
		case dSpanner:
			z = x.div(y, 9)
		case dOracle:
			var ok bool
			if z, ok = x.exactDiv(y, 38); !ok {
				return value{}, errUnsupported
			}
		default:
			// The scale of the result is implementation-defined in standard SQL, and complicated in SQL Server.
			return value{}, errUnsupported
		}
	case opMod:
		if d != dMySQL && d != dPostgres && d != dSpanner {
			return value{}, errUnsupported
		}
		if y.isZero() {
			return d.divisionByZero()
		}
		z = x.mod(y)
	case opIntDiv:
		if y.isZero() {
			return d.divisionByZero()
		}
		return d.integer(x.intDiv(y), false)
	default:
		return value{}, errUnsupported
	}
	return d.dec(z)
}

// concat applies the string concatenation operator ||.
func (d Dialect) concat(l, r value) (value, error) {
	if d == dSpanner {
		// The operands must be strings; the type of a NULL literal cannot be inferred.
		if l.kind != kindString || r.kind != kindString {
			return value{}, errUnsupported
		}
		return d.str(l.s + r.s), nil
	}
	if d == dOracle {
		// Oracle treats NULL as an empty string.
		var s [2]string
		for i, v := range []value{l, r} {
			if v.kind != kindNull {
				var err error
				if s[i], err = d.text(v); err != nil {
					return value{}, err
				}
			}
		}
		return d.str(s[0] + s[1]), nil
	}
	switch {
	case d == dPostgres && l.kind != kindString && r.kind != kindString && l.kind != kindNull && r.kind != kindNull:
		// There is no || operator for two non-string values.
		return value{}, errUnsupported
	case d == dISO && (l.kind != kindString && l.kind != kindNull || r.kind != kindString && r.kind != kindNull):
		return value{}, errUnsupported
	case l.kind == kindNull || r.kind == kindNull:
		return nullValue, nil
	}
	ls, err := d.text(l)
	if err != nil {
		return value{}, err
	}
	rs, err := d.text(r)
	if err != nil {
		return value{}, err
	}
	return d.str(ls + rs), nil
}

// bitwise applies a bitwise operator. For the unary operator ~, l is ignored.
func (d Dialect) bitwise(op string, l, r value) (value, error) {
	if d == dMySQL {
		return mysqlBitwise(op, l, r)
	}
	if r.kind != kindInt || op != "~" && l.kind != kindInt {
		return value{}, errUnsupported
	}
	y := r.n.u.Int64()
	if op == "~" {
		return d.integer(big.NewInt(^y), r.wide)
	}
	x := l.n.u.Int64()
	wide := l.wide || r.wide
	var z int64
	switch op {
	case opBitOr:
		z = x | y
	case opBitAnd:
		z = x & y
	case opBitXor:
		z = x ^ y
	case opShl, opShr:
		switch d {
		case dSpanner:
			// The shifts are logical, and a count of 64 or more results in 0.
			if y < 0 {
				return value{}, errUnsupported
			}
			if y < 64 && op == opShl {
				z = int64(uint64(x) << y)
			} else if y < 64 {
				z = int64(uint64(x) >> y)
			}
		case dPostgres:
			// The shift count must be an integer less than the width of the shifted value.
			bits := int64(32)
			if l.wide {
				bits = 64
			}
			if r.wide || y < 0 || y >= bits {
				return value{}, errUnsupported
			}
			if op == opShl {
				z = x << y
				if !l.wide {
					z = int64(int32(z))
				}
			} else {
				z = x >> y
			}
			wide = l.wide
		case dSQLite:
			z = sqliteShift(op, x, y)
		default:
			return value{}, errUnsupported
		}
	}
	return d.integer(big.NewInt(z), wide)
}

// sqliteShift shifts as SQLite does: a negative count shifts in the opposite direction.
func sqliteShift(op string, x, y int64) int64 {
	if y < 0 {
		if op == opShl {
			op = opShr
		} else {
			op = opShl
		}
		y = min(-max(y, -64), 64)
	}
	if y >= 64 {
		if x >= 0 || op == opShl {
			return 0
		}
		return -1
	}
	if op == opShl {
		return int64(uint64(x) << y)
	}
	return x >> y
}

var (
	minInt64  = big.NewInt(math.MinInt64)
	maxUint64 = new(big.Int).SetUint64(math.MaxUint64)
)

// mysqlBitwise applies a bitwise operator to 64-bit unsigned integers, as MySQL does.
func mysqlBitwise(op string, l, r value) (value, error) {
	y, err := toUint64(r)
	if err != nil {
		return value{}, err
	}
	var x uint64
	if op != "~" {
		if x, err = toUint64(l); err != nil {
			return value{}, err
		}
	}
	var z uint64
	switch op {
	case "~":
		z = ^y
	case opBitOr:
		z = x | y
	case opBitAnd:
		z = x & y
	case opBitXor:
		z = x ^ y
	case opShl:
		if y < 64 {
			z = x << y
		}
	case opShr:
		if y < 64 {
			z = x >> y
		}
	}
	return value{kind: kindInt, n: decimal{new(big.Int).SetUint64(z), 0}}, nil
}

func toUint64(v value) (uint64, error) {
	if v.kind != kindInt && v.kind != kindDecimal {
		return 0, errUnsupported
	}
	u := v.n.roundTo(0, false).u
	if u.Cmp(minInt64) < 0 || u.Cmp(maxUint64) > 0 {
		return 0, errUnsupported
	}
	if u.Sign() < 0 {
		return uint64(u.Int64()), nil
	}
	return u.Uint64(), nil
}

// power applies PostgreSQL's ^ operator. Only integer powers whose result is exactly representable as a double
// precision number are evaluated.
func (d Dialect) power(l, r value) (value, error) {
	if l.kind == kindNull || r.kind == kindNull {
		return nullValue, nil
	}
	x, ok1 := integral(l)
	y, ok2 := integral(r)
	if !ok1 || !ok2 || y.Sign() < 0 || y.Cmp(big.NewInt(1100)) > 0 {
		return value{}, errUnsupported
	}
	z := new(big.Int).Exp(x, y, nil)
	f, acc := new(big.Float).SetInt(z).Float64()
	if acc != big.Exact {
		return value{}, errUnsupported
	}
	return d.float(f)
}

// integral returns the value of an integer or of a floating-point number with an integral value.
func integral(v value) (*big.Int, bool) {
	switch {
	case v.kind == kindInt:
		return v.n.u, true
	case v.kind == kindFloat && v.f == math.Trunc(v.f):
		z, _ := big.NewFloat(v.f).Int(nil)
		return z, true
	}
	return nil, false
}
