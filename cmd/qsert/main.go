// Command qsert converts SQL INSERT statements into CSV.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sciencesakura/qsert/internal/qsert"
)

const usage = `Usage: qsert [options] [file ...]

Read INSERT and REPLACE statements from the files (or the standard input if no file
or "-" is given) and print their values as CSV to the standard output.

The header consists of the column names, or 1-based ordinals if the column list is
omitted. Rows are grouped by table, and the CSVs of the tables are separated by an
empty line. The header of each table is the union of the columns of its statements.

With -e, a single CSV of the selected columns is output instead, such as
  -e '.id, .name, .users.email, ."column name"'
where .column selects the column of any table, and .table.column (or .db.table.column)
selects the column of the table. The rows of the tables matched by any selector are
output in the input order, and the columns that the table of a row does not have are NULL.
The header of a column is the selector without the leading dot, or the name given by AS
such as .users.email AS mail or .name AS "Full Name".
An element of an array (ARRAY[...], '{...}' with -dialect postgres, [...] with -dialect spanner,
or a list, set or tuple with -dialect cassandra) is selected by 0-based indices such as
.column[0] or .column[-1][2], and is NULL if it does not exist. The elements of a set are
in the order that Cassandra stores them.
A member of JSON (a string literal, possibly cast to JSON or JSONB, a JSON literal such as
JSON '{...}' of GoogleSQL, or built by a function such as JSON_OBJECT and JSON_ARRAY), or of
a map or a user-defined type of CQL, is selected by its name or key such as .column["name"]
or .column["items"][0]["id"]. A JSON string is output as its content, an object or an array
in a string as JSON, an object or an array built by a function or a literal as the
expression, and null as NULL.

SQL is tokenized and expressions in values are evaluated according to the dialect given
by -dialect. If an expression cannot be evaluated, for example because it contains an
unsupported function or operator or its result depends on the configuration of the
DBMS, the expression itself is output with comments removed and whitespace normalized.
With -dialect iso, only what standard SQL defines is evaluated.

Options:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("qsert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var d qsert.Dialect
	parseDialect := func(s string) error {
		var ok bool
		if d, ok = qsert.ParseDialect(s); !ok {
			return fmt.Errorf("unknown dialect %q", s)
		}
		return nil
	}
	fs.Func("dialect", "SQL dialect `name`: "+strings.Join(qsert.DialectNames[:], ", ")+" (default iso)", parseDialect)
	fs.Func("i", "shorthand for -dialect `name`", parseDialect)
	var sels []qsert.Selector
	fs.Func("e", "comma-separated `selectors` of the columns to output, such as '.column, .table.column'",
		func(s string) (err error) {
			sels, err = qsert.ParseSelectors(s)
			return err
		})
	null := fs.String("null", "", "text to output for NULL")
	noBackslash := fs.Bool("no-backslash-escapes", false,
		"with -dialect mysql, treat backslashes in string literals as ordinary characters (NO_BACKSLASH_ESCAPES)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	status := 0
	c := qsert.NewCollector(*null)
	for _, name := range files {
		src, err := readInput(name, stdin)
		if err != nil {
			fmt.Fprintf(stderr, "qsert: %v\n", err)
			status = 1
			continue
		}
		if name == "-" {
			name = "<stdin>"
		}
		for _, err := range qsert.Convert(string(src), d, !*noBackslash, c) {
			fmt.Fprintf(stderr, "qsert: %s:%v\n", name, err)
			status = 1
		}
	}
	write := c.WriteCSV
	if sels != nil {
		write = func(w io.Writer) error { return c.WriteSelected(w, sels) }
		for _, s := range c.Unmatched(sels) {
			fmt.Fprintf(stderr, "qsert: warning: no table has the column .%s\n", s.Text())
		}
	}
	if err := write(stdout); err != nil {
		fmt.Fprintf(stderr, "qsert: %v\n", err)
		return 1
	}
	return status
}

func readInput(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(name)
}
