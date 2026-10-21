package qsert

// cell is a value of a column in a row.
type cell struct {
	text   string
	null   bool
	array  *array    // non-nil if the value is an array, including a JSON array built by a function
	object *object   // non-nil if the value is a JSON object built by a function
	json   *jsonText // text that may be JSON if the value is a string, possibly of a JSON type
}

// evalCell evaluates a value expression. If it cannot be evaluated, the formatted expression is used as the value.
func evalCell(toks []token, d Dialect) cell { return newCell(toks, d, false) }

// newCell evaluates a value expression, or an element of an array constructor if nested is true. A string may be
// JSON text, whose members can be selected.
func newCell(toks []token, d Dialect, nested bool) cell {
	v, c, ok := evalValue(toks, d)
	if e, isComposite := elementsOf(toks, d, nested); isComposite {
		c.setElements(e)
	} else if ok && v.kind == kindString {
		c.json = &jsonText{v.s, d}
	}
	return c
}

// elementsOf returns the elements of a value expression of a composite type: an array, JSON, or a collection of CQL.
func elementsOf(toks []token, d Dialect, nested bool) (cell, bool) {
	if d == dCassandra {
		return cqlCollection(toks)
	}
	if a := arrayOf(toks, d, nested); a != nil {
		return cell{array: a}, true
	}
	return jsonTyped(toks, d)
}

// evalValue evaluates a value expression. If it cannot be evaluated, ok is false and the cell is the formatted
// expression.
func evalValue(toks []token, d Dialect) (v value, c cell, ok bool) {
	v, err := evaluate(toks, d)
	if err == nil && v.kind == kindNull {
		return v, cell{null: true}, true
	}
	if err == nil {
		if s, err := d.render(v); err == nil {
			return v, cell{text: s}, true
		}
	}
	return value{}, cell{text: formatTokens(toks)}, false
}

// setElements sets the elements of a composite value represented by e, which consists only of them.
func (c *cell) setElements(e cell) {
	c.array, c.object, c.json = e.array, e.object, e.json
}

// step is a step of a path into a value: a name of an object member or, if the name is an integer, also a 0-based
// index of an array element, where a negative index counts from the end.
type step struct {
	key      string
	index    int
	hasIndex bool
}

func (s step) String() string {
	if isBareKey(s.key) {
		return "[" + s.key + "]"
	}
	return "[" + jsonQuote(s.key) + "]"
}

// position returns the position of the element that the step selects in an array of length n.
func (s step) position(n int) (int, bool) {
	i := s.index
	if i < 0 {
		i += n
	}
	return i, s.hasIndex && 0 <= i && i < n
}

// index returns the element of a cell at a path. It fails if the cell is not an array or JSON, or the element does
// not exist. An array element is selected by an integer, and an object member by a name.
func (c cell) index(path []step) (cell, bool) {
	for _, s := range path {
		var ok bool
		switch {
		case c.array != nil:
			var i int
			i, ok = s.position(len(c.array.elems))
			if ok = ok && !c.array.unordered; ok {
				c = c.array.elems[i]
			}
		case c.object != nil:
			c, ok = c.object.members[s.key]
		case c.json != nil:
			c, ok = c.json.step(s)
		}
		if !ok {
			return cell{}, false
		}
	}
	return c, true
}
