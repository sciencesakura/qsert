// Grammar of INSERT and REPLACE statements.
//
// Values are not parsed here; each value is collected as a balanced sequence of tokens and later evaluated by the
// expression parser (expr.y).

%{
package qsert
%}

%union {
	tok   token
	toks  []token
	names []string
	row   rawRow
	rows  []rawRow
	asgn  assignment
	stmt  *rawInsert
}

%token <tok> INSERT REPLACE INTO VALUES VALUE ROW SET IGNORE LOW_PRIORITY DELAYED HIGH_PRIORITY PARTITION
%token <tok> AS OR ON RETURNING DEFAULT SELECT WITH TABLE IF USING JSON THEN ASSERT_ROWS_MODIFIED UPDATE
%token <tok> IDENT QIDENT DQSTRING ASSIGN OTHER
%token <tok> '(' ')' '[' ']' '{' '}' ',' '.' '='

%type <tok> verb ident word set_word kw_common query_kw atom set_atom anytok
%type <toks> value vtok set_value stok btoks btok
%type <names> qualified_name opt_columns columns column_list
%type <row> row values_list
%type <rows> row_list
%type <asgn> assignment
%type <stmt> head insert_body set_clause

%%

stmt:
	head insert_body opt_trailer
	{
		$2.line = $1.line
		$2.table = $1.table
		insertlex.(*insertScanner).result = $2
	}
|	head query_body
	{
		$1.unsupported = "INSERT ... SELECT"
		insertlex.(*insertScanner).result = $1
	}
|	head JSON value
	{
		// CQL: INSERT INTO t JSON '{"column": value, ...}' [DEFAULT NULL | DEFAULT UNSET] [IF NOT EXISTS] [USING ...]
		$1.json = $3
		insertlex.(*insertScanner).result = $1
	}

head:
	verb opt_or modifiers opt_into qualified_name opt_partition opt_alias
	{
		$$ = &rawInsert{line: $1.line, table: $5}
	}

query_body:
	opt_columns query_kw tokens
|	'(' query_kw tokens
|	columns '(' query_kw tokens

verb:
	INSERT
|	REPLACE

opt_or:
	/* empty */
|	OR word

modifiers:
	/* empty */
|	modifiers LOW_PRIORITY
|	modifiers DELAYED
|	modifiers HIGH_PRIORITY
|	modifiers IGNORE
|	modifiers UPDATE

opt_into:
	/* empty */
|	INTO

opt_partition:
	/* empty */
|	PARTITION '(' btoks ')'

opt_alias:
	/* empty */
|	AS ident

insert_body:
	opt_columns VALUES row_list
	{
		$$ = &rawInsert{columns: $1, rows: $3}
	}
|	opt_columns VALUE row_list
	{
		$$ = &rawInsert{columns: $1, rows: $3}
	}
|	set_clause
|	DEFAULT VALUES
	{
		$$ = &rawInsert{unsupported: "INSERT ... DEFAULT VALUES"}
	}

opt_columns:
	/* empty */
	{
		$$ = nil
	}
|	columns

columns:
	'(' ')'
	{
		$$ = []string{}
	}
|	'(' column_list ')'
	{
		$$ = $2
	}

column_list:
	qualified_name
	{
		$$ = []string{$1[len($1)-1]}
	}
|	column_list ',' qualified_name
	{
		$$ = append($1, $3[len($3)-1])
	}

qualified_name:
	ident
	{
		$$ = []string{$1.val}
	}
|	qualified_name '.' ident
	{
		$$ = append($1, $3.val)
	}

ident:
	IDENT
|	QIDENT
|	DQSTRING
|	VALUE
|	RETURNING
|	JSON

row_list:
	row
	{
		$$ = []rawRow{$1}
	}
|	row_list ',' row
	{
		$$ = append($1, $3)
	}

row:
	opt_row '(' ')'
	{
		$$ = rawRow{line: $2.line}
	}
|	opt_row '(' values_list ')'
	{
		$$ = $3
		$$.line = $2.line
	}

opt_row:
	/* empty */
|	ROW

values_list:
	value
	{
		$$ = rawRow{values: [][]token{$1}}
	}
|	values_list ',' value
	{
		$$ = $1
		$$.values = append($$.values, $3)
	}

value:
	vtok
|	value vtok
	{
		$$ = append($1, $2...)
	}

vtok:
	atom
	{
		$$ = []token{$1}
	}
|	'(' btoks ')'
	{
		$$ = wrap($1, $2, $3)
	}
|	'[' btoks ']'
	{
		$$ = wrap($1, $2, $3)
	}
|	'{' btoks '}'
	{
		$$ = wrap($1, $2, $3)
	}

set_clause:
	SET assignment
	{
		$$ = &rawInsert{columns: []string{$2.column}, rows: []rawRow{{line: $1.line, values: [][]token{$2.value}}}}
	}
|	set_clause ',' assignment
	{
		$$ = $1
		$$.columns = append($$.columns, $3.column)
		$$.rows[0].values = append($$.rows[0].values, $3.value)
	}

assignment:
	qualified_name '=' set_value
	{
		$$ = assignment{column: $1[len($1)-1], value: $3}
	}
|	qualified_name ASSIGN set_value
	{
		$$ = assignment{column: $1[len($1)-1], value: $3}
	}

// A value in a SET clause ends before a following ON, AS or RETURNING clause.
set_value:
	stok
|	set_value stok
	{
		$$ = append($1, $2...)
	}

stok:
	set_atom
	{
		$$ = []token{$1}
	}
|	'(' btoks ')'
	{
		$$ = wrap($1, $2, $3)
	}
|	'[' btoks ']'
	{
		$$ = wrap($1, $2, $3)
	}
|	'{' btoks '}'
	{
		$$ = wrap($1, $2, $3)
	}

btoks:
	/* empty */
	{
		$$ = nil
	}
|	btoks btok
	{
		$$ = append($1, $2...)
	}

btok:
	vtok
|	','
	{
		$$ = []token{$1}
	}

opt_trailer:
	/* empty */
|	AS tokens
|	ON tokens
|	RETURNING tokens
|	IF tokens
|	USING tokens
|	THEN tokens
|	ASSERT_ROWS_MODIFIED tokens

tokens:
	/* empty */
|	tokens anytok

anytok:
	atom
|	'('
|	')'
|	'['
|	']'
|	'{'
|	'}'
|	','

atom:
	word
|	QIDENT
|	DQSTRING
|	ASSIGN
|	OTHER
|	'.'
|	'='

set_atom:
	set_word
|	QIDENT
|	DQSTRING
|	ASSIGN
|	OTHER
|	'.'
|	'='

word:
	set_word
|	ON
|	AS
|	RETURNING
|	IF
|	USING
|	JSON
|	THEN
|	ASSERT_ROWS_MODIFIED
|	UPDATE

set_word:
	IDENT
|	kw_common

kw_common:
	INSERT
|	REPLACE
|	INTO
|	VALUES
|	VALUE
|	ROW
|	SET
|	IGNORE
|	LOW_PRIORITY
|	DELAYED
|	HIGH_PRIORITY
|	PARTITION
|	OR
|	DEFAULT
|	query_kw

query_kw:
	SELECT
|	WITH
|	TABLE

%%
