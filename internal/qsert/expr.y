// Grammar of value expressions that qsert can evaluate.
//
// Only the constructs that can be evaluated are defined here. Any other construct (comparison and logical operators,
// CASE, CAST, subqueries, ...) is a syntax error, which makes the expression to be output as it is.
//
// Binary operators other than + and - are given generic precedence levels OP1 (lowest) to OP7 (highest), to which the
// scanner maps the operators of each dialect. OP5 has the same precedence as + and -.

%{
package qsert
%}

%union {
	tok   token
	node  exprNode
	nodes []exprNode
}

%token <tok> NUMBER STRING HEX BIT OCTAL LITERAL TYPED NULL TRUE FALSE NAME INTRODUCER ILLEGAL
%token <tok> OP1 OP2 OP3 OP4 OP5 OP6 OP7
%token <tok> '(' ')' ',' '+' '-' '~'

%type <node> expr primary literal strings
%type <nodes> args

%left OP1
%left OP2
%left OP3
%left OP4
%left '+' '-' OP5
%left OP6
%left OP7
%right UNARY

%%

top:
	expr
	{
		exprlex.(*exprScanner).result = $1
	}

expr:
	expr OP1 expr
	{
		$$ = &binaryNode{$2.val, $1, $3}
	}
|	expr OP2 expr
	{
		$$ = &binaryNode{$2.val, $1, $3}
	}
|	expr OP3 expr
	{
		$$ = &binaryNode{$2.val, $1, $3}
	}
|	expr OP4 expr
	{
		$$ = &binaryNode{$2.val, $1, $3}
	}
|	expr '+' expr
	{
		$$ = &binaryNode{opAdd, $1, $3}
	}
|	expr '-' expr
	{
		$$ = &binaryNode{opSub, $1, $3}
	}
|	expr OP5 expr
	{
		$$ = &binaryNode{$2.val, $1, $3}
	}
|	expr OP6 expr
	{
		$$ = &binaryNode{$2.val, $1, $3}
	}
|	expr OP7 expr
	{
		$$ = &binaryNode{$2.val, $1, $3}
	}
|	'-' expr %prec UNARY
	{
		$$ = &unaryNode{"-", $2}
	}
|	'+' expr %prec UNARY
	{
		$$ = &unaryNode{"+", $2}
	}
|	'~' expr %prec UNARY
	{
		$$ = &unaryNode{"~", $2}
	}
|	primary

primary:
	literal
|	'(' expr ')'
	{
		$$ = &parenNode{$2}
	}
|	NAME '(' ')'
	{
		$$ = &callNode{name: $1.text}
	}
|	NAME '(' args ')'
	{
		$$ = &callNode{name: $1.text, args: $3}
	}

args:
	expr
	{
		$$ = []exprNode{$1}
	}
|	args ',' expr
	{
		$$ = append($1, $3)
	}

literal:
	NUMBER
	{
		$$ = &numberNode{text: $1.text}
	}
|	HEX
	{
		$$ = &radixNode{$1.text, 16}
	}
|	BIT
	{
		$$ = &radixNode{$1.text, 2}
	}
|	OCTAL
	{
		$$ = &radixNode{$1.text, 8}
	}
|	strings
|	INTRODUCER strings
	{
		$$ = $2
	}
|	INTRODUCER HEX
	{
		$$ = &radixNode{$2.text, 16}
	}
|	INTRODUCER BIT
	{
		$$ = &radixNode{$2.text, 2}
	}
|	TYPED STRING
	{
		$$ = &typedNode{$1.text, $2.val}
	}
|	LITERAL
	{
		$$ = &literalNode{$1.text}
	}
|	NULL
	{
		$$ = &valueNode{nullValue}
	}
|	TRUE
	{
		$$ = &boolNode{true}
	}
|	FALSE
	{
		$$ = &boolNode{false}
	}

// Adjacent string literals are concatenated where the dialect allows (the scanner returns ILLEGAL otherwise).
strings:
	STRING
	{
		$$ = &stringNode{$1.val}
	}
|	strings STRING
	{
		$$ = &stringNode{$1.(*stringNode).s + $2.val}
	}

%%
