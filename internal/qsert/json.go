package qsert

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

// object is a JSON object built by a function such as JSON_OBJECT, whose members can be selected by their names.
type object struct {
	members map[string]cell
}

// jsonText is a string that may be JSON text, and the dialect that interprets it.
type jsonText struct {
	s string
	d Dialect
}

// duplicateMember reports which of the members with the same name the dialect selects: the last one if last is true,
// otherwise the first one. ok is false if it is not defined.
func (d Dialect) duplicateMember() (last, ok bool) {
	switch d {
	case dMySQL, dPostgres:
		return true, true
	case dSQLite, dMSSQL, dOracle, dSpanner:
		return false, true
	}
	return false, false
}

// isJSONType reports whether a type name is a JSON type.
func (d Dialect) isJSONType(typ []token) bool {
	return len(typ) == 1 && (typ[0].isWord("JSON") || d == dPostgres && typ[0].isWord("JSONB"))
}

// jsonTyped returns the JSON that an expression of a JSON type represents, which is a string cast to a JSON type such
// as '{"a":1}'::jsonb and CAST('[1]' AS JSON), a string passed to the JSON function of standard SQL, Oracle and
// SQLite, to JSON_QUERY of SQL Server or to PARSE_JSON of GoogleSQL, a JSON literal of GoogleSQL such as
// JSON '{"a":1}', or a JSON constructor such as JSON_OBJECT. The returned cell has only the JSON elements.
func jsonTyped(toks []token, d Dialect) (cell, bool) {
	if inner, typ, ok := stripCast(toks); ok {
		if !d.isJSONType(typ) {
			return cell{}, false
		}
		return jsonSource(inner, d)
	}
	if d == dSpanner && len(toks) == 2 && toks[0].isWord("JSON") && toks[1].kind == tkString {
		return cell{json: &jsonText{toks[1].val, d}}, true
	}
	if args, ok := enclosed(toks, 1, "("); ok && toks[0].kind == tkIdent {
		name := strings.ToUpper(toks[0].text)
		if name == "JSON" && (d == dISO || d == dOracle || d == dSQLite) || name == "JSON_QUERY" && d == dMSSQL ||
			name == "PARSE_JSON" && d == dSpanner {
			return jsonSource(args, d)
		}
		if f, ok := jsonConstructors[d][name]; ok {
			return f.build(args, d)
		}
	}
	return cell{}, false
}

// jsonSource returns the JSON of an expression that is converted to a JSON type.
func jsonSource(toks []token, d Dialect) (cell, bool) {
	if j, ok := jsonTyped(toks, d); ok {
		return j, true
	}
	v, err := evaluate(toks, d)
	if err != nil || v.kind != kindString {
		return cell{}, false
	}
	return cell{json: &jsonText{v.s, d}}, true
}

// jsonConstructor is a function that builds a JSON object or array from its arguments.
type jsonConstructor struct {
	object   bool
	standard bool // the syntax of standard SQL: name VALUE value, name : value, ON NULL, RETURNING and so on
}

var (
	jsonObject       = jsonConstructor{object: true, standard: true}
	jsonArray        = jsonConstructor{standard: true}
	jsonPairs        = jsonConstructor{object: true} // alternating names and values: JSON_OBJECT('a', 1)
	jsonValues       = jsonConstructor{}
	standardJSON     = map[string]jsonConstructor{"JSON_OBJECT": jsonObject, "JSON_ARRAY": jsonArray}
	jsonConstructors = [...]map[string]jsonConstructor{
		dISO:   standardJSON,
		dMySQL: {"JSON_OBJECT": jsonPairs, "JSON_ARRAY": jsonValues},
		dPostgres: {
			"JSON_OBJECT":        jsonObject,
			"JSON_ARRAY":         jsonArray,
			"JSON_BUILD_OBJECT":  jsonPairs,
			"JSONB_BUILD_OBJECT": jsonPairs,
			"JSON_BUILD_ARRAY":   jsonValues,
			"JSONB_BUILD_ARRAY":  jsonValues,
		},
		dSQLite:    {"JSON_OBJECT": jsonPairs, "JSON_ARRAY": jsonValues, "JSONB_OBJECT": jsonPairs, "JSONB_ARRAY": jsonValues},
		dMSSQL:     standardJSON,
		dOracle:    standardJSON,
		dCassandra: {},
		dSpanner:   {"JSON_OBJECT": jsonPairs, "JSON_ARRAY": jsonValues},
	}
)

var errUndetermined = errors.New("undetermined")

// jsonOptions are the clauses that follow the arguments of a JSON constructor of standard SQL.
type jsonOptions struct {
	absentOnNull bool // omit NULL values
	unique       bool // fail on duplicate names
}

// build returns the JSON object or array built from the arguments. It fails if the arguments are invalid, or the
// result cannot be determined.
func (f jsonConstructor) build(toks []token, d Dialect) (cell, bool) {
	var opts jsonOptions
	if f.standard {
		var ok bool
		if toks, opts, ok = f.options(toks, d); !ok {
			return cell{}, false
		}
	}
	args, ok := splitList(toks)
	if !ok {
		return cell{}, false
	}
	if !f.object {
		a := &array{}
		for _, arg := range args {
			c, ok := opts.value(f.argValue(arg, d))
			if !ok {
				return cell{}, false
			}
			if !c.null || !opts.absentOnNull {
				a.elems = append(a.elems, c)
			}
		}
		return cell{array: a}, true
	}
	var names, values [][]token
	if f.standard {
		for _, arg := range args {
			name, value, ok := standardMember(arg, d)
			if !ok {
				return cell{}, false
			}
			names, values = append(names, name), append(values, value)
		}
	} else {
		if len(args)%2 != 0 {
			return cell{}, false
		}
		for i := 0; i < len(args); i += 2 {
			names, values = append(names, args[i]), append(values, args[i+1])
		}
	}
	o := &object{members: make(map[string]cell)}
	for i, name := range names {
		key, ok := memberName(name, d)
		if !ok {
			return cell{}, false
		}
		c, ok := opts.value(f.argValue(values[i], d))
		if !ok {
			return cell{}, false
		}
		if c.null && opts.absentOnNull {
			continue
		}
		if _, dup := o.members[key]; dup {
			last, ok := d.duplicateMember()
			if opts.unique || !ok {
				return cell{}, false
			}
			if !last {
				continue
			}
		}
		o.members[key] = c
	}
	return cell{object: o}, true
}

// value converts an argument value. An undetermined value is the formatted expression, unless it may be NULL to be
// omitted.
func (o jsonOptions) value(a argValue) (cell, bool) {
	c, err := jsonValue(a)
	return c, err == nil || err == errUndetermined && !o.absentOnNull
}

// options removes and parses the clauses following the last argument of a constructor of standard SQL, such as
// ABSENT ON NULL, WITH UNIQUE KEYS and RETURNING jsonb, which may be in any order.
func (f jsonConstructor) options(toks []token, d Dialect) ([]token, jsonOptions, bool) {
	// The defaults are NULL ON NULL for JSON_OBJECT, and ABSENT ON NULL for JSON_ARRAY.
	opts := jsonOptions{absentOnNull: !f.object}
	start := findTopLevel(toks, func(i int) bool { return clauseLen(toks[i:], d) > 0 })
	if start < 0 {
		return toks, opts, true
	}
	clauses := toks[start:]
	toks = toks[:start]
	if len(toks) > 0 && toks[len(toks)-1].isOp(",") {
		return nil, opts, false
	}
	for len(clauses) > 0 {
		n := clauseLen(clauses, d)
		switch c := clauses[0]; {
		case n == 0:
			return nil, opts, false
		case c.isWord("RETURNING"):
			// The type of the result does not matter, since a string is regarded as JSON text.
			if n = findTopLevel(clauses[1:], func(i int) bool { return clauseLen(clauses[1+i:], d) > 0 }) + 1; n == 0 {
				n = len(clauses)
			}
		case c.isWord("NULL") || c.isWord("ABSENT"):
			opts.absentOnNull = c.isWord("ABSENT")
		case c.isWord("WITH") || c.isWord("WITHOUT"):
			if !f.object {
				return nil, opts, false
			}
			opts.unique = c.isWord("WITH")
		}
		clauses = clauses[n:]
	}
	return toks, opts, true
}

// clauseLen returns the number of the leading tokens of a clause of a JSON constructor, or 0 if toks does not begin
// with a clause. The type following RETURNING is not included.
func clauseLen(toks []token, d Dialect) int {
	n := len(toks)
	switch t := toks[0]; {
	case n >= 3 && (t.isWord("NULL") || t.isWord("ABSENT")) && toks[1].isWord("ON") && toks[2].isWord("NULL"):
		return 3
	case n >= 2 && (t.isWord("WITH") || t.isWord("WITHOUT")) && toks[1].isWord("UNIQUE") && d != dMSSQL:
		if n >= 3 && toks[2].isWord("KEYS") {
			return 3
		}
		return 2
	case n >= 2 && t.isWord("RETURNING"):
		return 1
	case t.isWord("STRICT") && d == dOracle:
		return 1
	}
	return 0
}

// standardMember splits a member of JSON_OBJECT of standard SQL: [KEY] name VALUE value, or name : value.
func standardMember(toks []token, d Dialect) (name, value []token, ok bool) {
	if len(toks) > 0 && toks[0].isWord("KEY") && (d == dISO || d == dOracle) {
		toks = toks[1:]
	}
	i := findTopLevel(toks, func(i int) bool { return toks[i].isOp(":") || toks[i].isWord("VALUE") && d != dMSSQL })
	if i <= 0 || i == len(toks)-1 {
		return nil, nil, false
	}
	return toks[:i], toks[i+1:], true
}

// memberName evaluates the name of a member. It must be a string, or a number in MySQL, PostgreSQL and SQL Server.
func memberName(toks []token, d Dialect) (string, bool) {
	v, err := evaluate(toks, d)
	if err != nil {
		return "", false
	}
	switch {
	case v.kind == kindString:
		return v.s, true
	case v.kind != kindNull && (d == dMySQL || d == dPostgres || d == dMSSQL):
		s, err := d.text(v)
		return s, err == nil
	}
	return "", false
}

// argValue is an argument value of a JSON constructor.
type argValue struct {
	toks       []token
	d          Dialect
	formatJSON bool // the value is JSON text: value FORMAT JSON of standard SQL
}

func (f jsonConstructor) argValue(toks []token, d Dialect) argValue {
	n := len(toks)
	if f.standard && d != dMSSQL && n > 2 && toks[n-2].isWord("FORMAT") && toks[n-1].isWord("JSON") {
		return argValue{toks[:n-2], d, true}
	}
	return argValue{toks, d, false}
}

// jsonValue converts an argument value of a JSON constructor to a cell. A string is a JSON string unless it is of a
// JSON type, and an array is a JSON array. It returns errUndetermined with the formatted expression if the value
// cannot be evaluated.
func jsonValue(a argValue) (cell, error) {
	v, c, ok := evalValue(a.toks, a.d)
	if c.array = arrayOf(a.toks, a.d, false); c.array != nil {
		return c, nil
	}
	if j, isJSON := jsonTyped(a.toks, a.d); isJSON {
		c.setElements(j)
		return c, nil
	}
	switch {
	case (a.d == dMySQL || a.d == dOracle) && len(a.toks) == 1 && (a.toks[0].isWord("TRUE") || a.toks[0].isWord("FALSE")):
		// The boolean literals are JSON booleans, though they are integers in MySQL and need Oracle 23ai.
		c = cell{text: strings.ToLower(a.toks[0].text)}
	case !ok || v.rounded:
		return cell{text: formatTokens(a.toks)}, errUndetermined
	case v.kind == kindNull:
	case a.formatJSON:
		if v.kind != kindString {
			return cell{}, errUnsupported
		}
		c.json = &jsonText{v.s, a.d}
	case v.kind == kindBool:
		c.text = strconv.FormatBool(v.b)
	case v.kind == kindFloat && a.d == dMySQL:
		c.text = mysqlJSONDouble(v.f)
	case v.kind == kindFloat && a.d == dSpanner:
		c.text = spannerJSONDouble(v.f)
	case v.kind == kindBinary:
		return cell{}, errUnsupported
	}
	return c, nil
}

// step returns the member of a JSON object, or the element of a JSON array, that a path step selects. It fails if
// the text is not a JSON object or array respectively, or the member or the element does not exist.
func (j *jsonText) step(s step) (cell, bool) {
	if !json.Valid([]byte(j.s)) {
		return cell{}, false
	}
	if s.member {
		raw, ok := j.member(s.key)
		if !ok {
			return cell{}, false
		}
		return j.cell(raw), true
	}
	var a []json.RawMessage
	if json.Unmarshal([]byte(j.s), &a) != nil || a == nil {
		return cell{}, false
	}
	i, ok := s.position(len(a))
	if !ok {
		return cell{}, false
	}
	return j.cell(a[i]), true
}

// member returns the value of the member of a valid JSON object.
func (j *jsonText) member(key string) (json.RawMessage, bool) {
	dec := json.NewDecoder(strings.NewReader(j.s))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var found json.RawMessage
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, false
		}
		if t != key {
			continue
		}
		if found != nil {
			last, ok := j.d.duplicateMember()
			if !ok {
				return nil, false
			}
			if !last {
				continue
			}
		}
		found = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return found, found != nil
}

// cell converts a JSON value to a cell: null is NULL, a string is its content, and the others are their text.
func (j *jsonText) cell(raw json.RawMessage) cell {
	raw = bytes.TrimSpace(raw)
	switch {
	case string(raw) == "null":
		return cell{null: true}
	case len(raw) > 0 && raw[0] == '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return cell{text: s}
		}
	}
	s := string(raw)
	if len(raw) > 0 && (raw[0] == '-' || isDigit(raw[0])) {
		switch j.d {
		case dMySQL:
			s = mysqlJSONNumber(s)
		case dSpanner:
			s = spannerJSONNumber(s)
		}
	}
	return cell{text: s, json: &jsonText{s, j.d}}
}

// mysqlJSONNumber returns the text of a number in JSON text as MySQL stores it: an integer is a signed or unsigned
// 64-bit integer if it fits, and the others are doubles. A double of more than 15 significant digits is returned as
// it is, because MySQL's parser does not always round it correctly.
func mysqlJSONNumber(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			return strconv.FormatUint(n, 10)
		}
	}
	mant, _, _ := strings.Cut(strings.ToLower(s), "e")
	digits := strings.TrimLeft(strings.Map(func(r rune) rune {
		if '0' <= r && r <= '9' {
			return r
		}
		return -1
	}, mant), "0")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || len(digits) > 15 {
		return s
	}
	return mysqlJSONDouble(f)
}

// mysqlJSONDouble formats a double in JSON like MySQL, which writes a fractional part for an integral value: 1.0.
func mysqlJSONDouble(f float64) string {
	s := mysqlFloat(f)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// jsonQuote returns a string as a JSON string literal.
func jsonQuote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
