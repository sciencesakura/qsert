package qsert

import (
	"bufio"
	"encoding/csv"
	"io"
	"strings"
)

type table struct {
	name  string
	key   string // lower-cased name
	cols  []string
	index map[string]int // lower-cased column name to index
}

// row is a row of a table. Its values are indexed by the columns of the table, and missing values are empty.
type row struct {
	t      *table
	values []cell
}

// Collector collects rows in the input order, and takes the union of the columns of each table.
type Collector struct {
	null   string // text output for NULL
	tables map[string]*table
	order  []*table
	rows   []row
}

// NewCollector returns an empty Collector that outputs NULL as null.
func NewCollector(null string) *Collector {
	return &Collector{null: null, tables: make(map[string]*table)}
}

func (c *Collector) add(st *insertStmt) {
	name := strings.Join(st.table, ".")
	key := strings.ToLower(name)
	t, ok := c.tables[key]
	if !ok {
		t = &table{name: name, key: key, index: make(map[string]int)}
		c.tables[key] = t
		c.order = append(c.order, t)
	}
	idx := make([]int, len(st.columns))
	for i, col := range st.columns {
		k := strings.ToLower(col)
		j, ok := t.index[k]
		if !ok {
			j = len(t.cols)
			t.cols = append(t.cols, col)
			t.index[k] = j
		}
		idx[i] = j
	}
	for _, r := range st.rows {
		values := make([]cell, len(t.cols))
		for i, cl := range r {
			values[idx[i]] = cl
		}
		c.rows = append(c.rows, row{t, values})
	}
}

// value returns the value of the column at index j. A column that the statement of the row does not have is empty.
func (r row) value(j int) cell {
	if j < len(r.values) {
		return r.values[j]
	}
	return cell{}
}

// WriteCSV writes a CSV for each table, separated by an empty line.
func (c *Collector) WriteCSV(w io.Writer) error {
	cw := newCSVWriter(w)
	for i, t := range c.order {
		if i > 0 {
			cw.emptyLine()
		}
		cw.write(t.cols)
		for _, r := range c.rows {
			if r.t == t {
				rec := make([]string, len(t.cols))
				for j := range rec {
					rec[j] = c.render(r.value(j))
				}
				cw.write(rec)
			}
		}
	}
	return cw.close()
}

func (c *Collector) render(v cell) string {
	if v.null {
		return c.null
	}
	return v.text
}

// WriteSelected writes a single CSV of the selected columns. Only the rows of the tables matched by any of the
// selectors are written. A column that the table of a row does not have, and an element of an array or JSON that does
// not exist, are NULL.
func (c *Collector) WriteSelected(w io.Writer, sels []Selector) error {
	cols := make(map[*table][]int) // indices of the selected columns per matched table, -1 if missing
	for _, t := range c.order {
		var matched bool
		idx := make([]int, len(sels))
		for i, s := range sels {
			j, ok := s.columnOf(t)
			idx[i] = j
			matched = matched || ok || s.table == t.key
		}
		if matched {
			cols[t] = idx
		}
	}
	cw := newCSVWriter(w)
	header := make([]string, len(sels))
	for i, s := range sels {
		header[i] = s.Header()
	}
	cw.write(header)
	for _, r := range c.rows {
		idx, ok := cols[r.t]
		if !ok {
			continue
		}
		rec := make([]string, len(sels))
		for i, j := range idx {
			v, ok := cell{}, false
			if j >= 0 {
				v, ok = r.value(j).index(sels[i].path)
			}
			if !ok {
				v = cell{null: true}
			}
			rec[i] = c.render(v)
		}
		cw.write(rec)
	}
	return cw.close()
}

// Unmatched returns the selectors that match no table.
func (c *Collector) Unmatched(sels []Selector) []Selector {
	var r []Selector
	for _, s := range sels {
		found := false
		for _, t := range c.order {
			if _, ok := s.columnOf(t); ok {
				found = true
				break
			}
		}
		if !found {
			r = append(r, s)
		}
	}
	return r
}

type csvWriter struct {
	bw  *bufio.Writer
	cw  *csv.Writer
	err error
}

func newCSVWriter(w io.Writer) *csvWriter {
	bw := bufio.NewWriter(w)
	return &csvWriter{bw: bw, cw: csv.NewWriter(bw)}
}

func (w *csvWriter) write(rec []string) {
	if w.err != nil {
		return
	}
	// A record consisting of a single empty field must be quoted not to be an empty line.
	if len(rec) == 1 && rec[0] == "" {
		w.cw.Flush()
		_, w.err = w.bw.WriteString("\"\"\n")
		return
	}
	w.err = w.cw.Write(rec)
}

func (w *csvWriter) emptyLine() {
	if w.err != nil {
		return
	}
	w.cw.Flush()
	_, w.err = w.bw.WriteString("\n")
}

func (w *csvWriter) close() error {
	if w.err != nil {
		return w.err
	}
	w.cw.Flush()
	if err := w.cw.Error(); err != nil {
		return err
	}
	return w.bw.Flush()
}
