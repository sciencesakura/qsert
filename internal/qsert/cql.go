package qsert

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Literals of CQL that consist of characters of several tokens of SQL.
var cqlLiterals = []*regexp.Regexp{
	// UUID: 123e4567-e89b-12d3-a456-426614174000
	regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`),
	// duration: 1h30m, 2w, 12ms, 3µs
	regexp.MustCompile(`^(?i)([0-9]+(y|mo|w|d|h|ms|m|s|us|µs|ns))+`),
	// duration in the alternative format of ISO 8601: P0001-02-03T04:05:06
	regexp.MustCompile(`^P[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}`),
	// duration in the format of ISO 8601: P1Y2M3DT4H5M6S, P2W
	regexp.MustCompile(`^P([0-9]+W|([0-9]+Y)?([0-9]+M)?([0-9]+D)?(T([0-9]+H)?([0-9]+M)?([0-9]+S)?)?)`),
}

// cqlLiteralLen returns the length of the UUID or duration literal at the beginning of s, or 0 if there is none.
func cqlLiteralLen(s string) int {
	for _, re := range cqlLiterals {
		m := re.FindString(s)
		if strings.ContainsAny(m, "0123456789") && (len(m) == len(s) || !isIdentChar(s[len(m)])) {
			return len(m)
		}
	}
	return 0
}

// cqlCollection returns the elements of a collection literal of CQL: a list [...], a set {...}, a map {k: v, ...},
// a user-defined type {field: v, ...} or a tuple (v, ...). Lists, sets and tuples are arrays, and maps and
// user-defined types are objects. A set is ordered as Cassandra stores it, and its elements cannot be selected by an
// index if the order cannot be determined.
func cqlCollection(toks []token) (cell, bool) {
	if items, ok := enclosed(toks, 0, "["); ok {
		a, ok := cqlElements(items)
		return cell{array: a}, ok
	}
	if items, ok := enclosed(toks, 0, "("); ok {
		// A tuple has more than one element; (v) is a parenthesized expression.
		if findTopLevel(items, func(i int) bool { return items[i].isOp(",") }) < 0 {
			return cell{}, false
		}
		a, ok := cqlElements(items)
		return cell{array: a}, ok
	}
	items, ok := enclosed(toks, 0, "{")
	if !ok {
		return cell{}, false
	}
	if len(items) == 0 {
		return cell{array: &array{}}, true // an empty set or map
	}
	if findTopLevel(items, func(i int) bool { return items[i].isOp(":") }) < 0 {
		a, ok := cqlSet(items)
		return cell{array: a}, ok
	}
	o, ok := cqlMap(items)
	return cell{object: o}, ok
}

func cqlElements(toks []token) (*array, bool) {
	a, _, ok := cqlValues(toks)
	return a, ok
}

// cqlValues returns the elements of a comma-separated list and their values, which are null if not evaluated.
func cqlValues(toks []token) (*array, []value, bool) {
	items, ok := splitList(toks)
	if !ok {
		return nil, nil, false
	}
	a := &array{}
	vals := make([]value, len(items))
	for i, item := range items {
		if v, _, ok := evalValue(item, dCassandra); ok {
			vals[i] = v
		}
		a.elems = append(a.elems, newCell(item, dCassandra, true))
	}
	return a, vals, true
}

// cqlMap returns the members of a map or a user-defined type literal. The name of a field of a user-defined type is
// an identifier, which is case-insensitive unless quoted, and the key of a map is the text of its value.
func cqlMap(toks []token) (*object, bool) {
	items, ok := splitList(toks)
	if !ok {
		return nil, false
	}
	o := &object{members: make(map[string]cell)}
	for _, item := range items {
		i := findTopLevel(item, func(i int) bool { return item[i].isOp(":") })
		if i <= 0 || i == len(item)-1 {
			return nil, false
		}
		var key string
		switch k := item[:i]; {
		case len(k) == 1 && k[0].kind == tkIdent:
			key = strings.ToLower(k[0].text)
		case len(k) == 1 && k[0].kind == tkDQString:
			key = k[0].val
		default:
			v, c, ok := evalValue(k, dCassandra)
			if !ok || v.kind == kindNull {
				return nil, false
			}
			key = c.text
		}
		// Cassandra stores the first or the last of the duplicate keys inconsistently.
		if _, dup := o.members[key]; dup {
			return nil, false
		}
		o.members[key] = newCell(item[i+1:], dCassandra, true)
	}
	return o, true
}

// cqlSet returns the elements of a set literal sorted without duplicates as Cassandra stores them. If they are not all
// numbers or all strings, whose order is known, the set is unordered.
func cqlSet(toks []token) (*array, bool) {
	a, vals, ok := cqlValues(toks)
	if !ok {
		return nil, false
	}
	// The elements are ordered by the comparator of their type, which is known for numbers, strings (of UTF-8
	// bytes), blobs and booleans.
	type sortKey struct {
		class int // 1: number, 2: string or blob, 3: boolean
		num   *big.Rat
		bytes string
	}
	keys := make([]sortKey, len(vals))
	for i, v := range vals {
		k := &keys[i]
		switch v.kind {
		case kindInt:
			k.class, k.num = 1, new(big.Rat).SetFrac(v.n.u, pow10(v.n.scale))
		case kindVerbatim:
			// decimal and floating-point literals
			if r, ok := new(big.Rat).SetString(v.s); ok {
				k.class, k.num = 1, r
			}
		case kindString:
			k.class, k.bytes = 2, v.s
		case kindBinary:
			if b, err := hex.DecodeString(strings.TrimPrefix(v.s, "0x")); err == nil && strings.HasPrefix(v.s, "0x") {
				k.class, k.bytes = 2, string(b)
			}
		case kindBool:
			k.class, k.bytes = 3, boolText(v.b, "1", "0")
		}
		if k.class == 0 || k.class != keys[0].class {
			a.unordered = true
			return a, true
		}
	}
	cmp := func(i, j int) int {
		if keys[i].num != nil {
			return keys[i].num.Cmp(keys[j].num)
		}
		return strings.Compare(keys[i].bytes, keys[j].bytes)
	}
	idx := make([]int, len(vals))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(x, y int) bool { return cmp(idx[x], idx[y]) < 0 })
	set := &array{}
	for n, i := range idx {
		switch {
		case n == 0 || cmp(idx[n-1], i) != 0:
			set.elems = append(set.elems, a.elems[i])
		case a.elems[idx[n-1]].text != a.elems[i].text:
			// It is unknown which of the equal elements written differently, such as 1.50 and 1.5, is stored.
			a.unordered = true
			return a, true
		}
	}
	return set, true
}

// cqlJSONInsert returns the row of INSERT ... JSON of CQL, whose columns are the names of the members of the JSON
// object. A name is case-insensitive unless quoted with double quotes.
func cqlJSONInsert(raw *rawInsert) (*insertStmt, error) {
	t := raw.json[0]
	if t.kind != tkString {
		return nil, &syntaxError{t.line, "INSERT ... JSON with a value other than a string literal is not supported"}
	}
	invalid := &syntaxError{t.line, "invalid JSON object"}
	if !json.Valid([]byte(t.val)) {
		return nil, invalid
	}
	dec := json.NewDecoder(strings.NewReader(t.val))
	if d, err := dec.Token(); err != nil || d != json.Delim('{') {
		return nil, invalid
	}
	st := &insertStmt{table: raw.table, rows: [][]cell{nil}}
	j := &jsonText{d: dCassandra}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, invalid
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, invalid
		}
		name := k.(string)
		if len(name) >= 2 && strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) {
			name = strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
		}
		st.columns = append(st.columns, name)
		st.rows[0] = append(st.rows[0], j.cell(v))
	}
	if err := checkDuplicateColumns(st.columns, raw.line); err != nil {
		return nil, err
	}
	return st, nil
}

// cqlStatements splits the statements in a batch of CQL, BEGIN [UNLOGGED | COUNTER] BATCH [USING TIMESTAMP n] ...
// APPLY BATCH, in which statements need not be terminated by semicolons.
func cqlStatements(toks []token) [][]token {
	if len(toks) > 0 && toks[0].isWord("BEGIN") {
		for i := 1; i < len(toks) && i <= 2; i++ {
			if toks[i].isWord("BATCH") {
				toks = toks[i+1:]
				if len(toks) >= 3 && toks[0].isWord("USING") && toks[1].isWord("TIMESTAMP") {
					toks = toks[3:]
				}
				break
			}
		}
	}
	var stmts [][]token
	for len(toks) > 0 {
		i := findTopLevel(toks, func(i int) bool {
			t := toks[i]
			return i > 0 && (t.isWord("INSERT") || t.isWord("UPDATE") || t.isWord("DELETE") || t.isWord("APPLY"))
		})
		if i < 0 {
			i = len(toks)
		}
		stmts = append(stmts, toks[:i])
		toks = toks[i:]
	}
	return stmts
}

// literalNode is a UUID or duration literal of CQL.
type literalNode struct{ text string }

// eval returns the literal as Cassandra outputs it: a UUID in lower case, and a duration normalized such as 1h30m
// for 90m.
func (n *literalNode) eval(Dialect) (value, error) {
	if cqlLiterals[0].MatchString(n.text) {
		return value{kind: kindVerbatim, s: strings.ToLower(n.text)}, nil
	}
	if d, ok := parseCQLDuration(n.text); ok {
		return value{kind: kindVerbatim, s: d.String()}, nil
	}
	return value{kind: kindVerbatim, s: n.text}, nil
}

// cqlDuration is a duration of CQL, which consists of months, days and nanoseconds.
type cqlDuration struct {
	neg                 bool
	months, days, nanos int64
}

var (
	cqlDurationUnit = regexp.MustCompile(`(?i)([0-9]+)(y|mo|w|d|h|ms|m|s|us|µs|ns)`)
	cqlDurationISO  = regexp.MustCompile(`^P(?:([0-9]+)Y)?(?:([0-9]+)M)?(?:([0-9]+)D)?(?:T(?:([0-9]+)H)?(?:([0-9]+)M)?(?:([0-9]+)S)?)?$`)
	cqlDurationAlt  = regexp.MustCompile(`^P([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})$`)
)

// parseCQLDuration parses a duration literal: 1h30m, P1Y2M3DT4H5M6S, P2W or P0001-02-03T04:05:06.
func parseCQLDuration(s string) (cqlDuration, bool) {
	var d cqlDuration
	add := func(n string, unit string) bool {
		v, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return false
		}
		mul := map[string]int64{
			"y": 12, "mo": 1, "w": 7, "d": 1, "h": 3600e9, "m": 60e9, "s": 1e9, "ms": 1e6, "us": 1e3, "µs": 1e3, "ns": 1,
		}[strings.ToLower(unit)]
		var p *int64
		switch strings.ToLower(unit) {
		case "y", "mo":
			p = &d.months
		case "w", "d":
			p = &d.days
		default:
			p = &d.nanos
		}
		if v > (math.MaxInt64-*p)/mul {
			return false
		}
		*p += v * mul
		return true
	}
	switch {
	case strings.HasPrefix(s, "P") && strings.HasSuffix(s, "W") && len(s) > 2:
		return d, add(s[1:len(s)-1], "w") && d.valid()
	case cqlDurationAlt.MatchString(s):
		m := cqlDurationAlt.FindStringSubmatch(s)
		units := []string{"y", "mo", "d", "h", "m", "s"}
		for i, u := range units {
			if !add(m[i+1], u) {
				return d, false
			}
		}
		return d, d.valid()
	case cqlDurationISO.MatchString(s) && s != "P" && !strings.HasSuffix(s, "T"):
		m := cqlDurationISO.FindStringSubmatch(s)
		units := []string{"y", "mo", "d", "h", "m", "s"}
		for i, u := range units {
			if m[i+1] != "" && !add(m[i+1], u) {
				return d, false
			}
		}
		return d, d.valid()
	}
	if cqlDurationUnit.ReplaceAllString(s, "") != "" {
		return d, false
	}
	for _, m := range cqlDurationUnit.FindAllStringSubmatch(s, -1) {
		if !add(m[1], m[2]) {
			return d, false
		}
	}
	return d, d.valid()
}

// valid reports whether the months and days fit in 32-bit integers as Cassandra stores them.
func (d cqlDuration) valid() bool { return d.months <= math.MaxInt32 && d.days <= math.MaxInt32 }

// String formats a duration as Cassandra does, in which a zero duration is empty.
func (d cqlDuration) String() string {
	var b strings.Builder
	if d.neg {
		b.WriteByte('-')
	}
	part := func(n int64, unit string) {
		if n != 0 {
			b.WriteString(strconv.FormatInt(n, 10))
			b.WriteString(unit)
		}
	}
	part(d.months/12, "y")
	part(d.months%12, "mo")
	part(d.days, "d")
	n := d.nanos
	for _, u := range []struct {
		unit string
		ns   int64
	}{{"h", 3600e9}, {"m", 60e9}, {"s", 1e9}, {"ms", 1e6}, {"us", 1e3}, {"ns", 1}} {
		part(n/u.ns, u.unit)
		n %= u.ns
	}
	return b.String()
}
