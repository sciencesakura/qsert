package main

import (
	"bytes"
	"strings"
	"testing"
)

func runQsert(t *testing.T, input string, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	status = run(args, strings.NewReader(input), &out, &errOut)
	return out.String(), errOut.String(), status
}

// checkRun runs qsert and checks that it succeeds with the output.
func checkRun(t *testing.T, input, want string, args ...string) {
	t.Helper()
	out, errOut, status := runQsert(t, input, args...)
	if status != 0 || errOut != "" {
		t.Errorf("%q: status = %d, stderr = %q", args, status, errOut)
	}
	if out != want {
		t.Errorf("%q:\nstdout = %q\nwant     %q", args, out, want)
	}
}

// selectValue inserts a value into the column v, and returns the record of the values selected from it.
func selectValue(t *testing.T, dialect, value, sel string) string {
	t.Helper()
	out, errOut, status := runQsert(t, "INSERT INTO t (v) VALUES ("+value+");", "-dialect", dialect, "-null", "NULL",
		"-e", sel)
	lines := strings.Split(out, "\n")
	if status != 0 || errOut != "" || len(lines) != 3 {
		t.Errorf("%s %s %s: status = %d, stderr = %q, stdout = %q", dialect, value, sel, status, errOut, out)
		return ""
	}
	return lines[1]
}

func TestRun(t *testing.T) {
	tests := []struct {
		name, input, want string
		args              []string
	}{
		{
			name:  "column list",
			input: "INSERT INTO t (a, b) VALUES (1, 'x'), (2, 'y, \"z\"');",
			want:  "a,b\n1,x\n2,\"y, \"\"z\"\"\"\n",
		},
		{
			name:  "ordinal columns",
			input: "INSERT INTO t VALUES (1, 'x');\ninsert t value (2, 'y')",
			want:  "1,2\n1,x\n2,y\n",
		},
		{
			name: "union of columns and grouping by table",
			input: "INSERT INTO a (x) VALUES (1);\n" +
				"INSERT INTO b (p) VALUES ('b');\n" +
				"INSERT INTO A (y, X) VALUES (3, 2);\n",
			want: "x,y\n1,\n2,3\n\np\nb\n",
		},
		{
			name:  "NULL text",
			input: "INSERT INTO t (a, b) VALUES (NULL, '');",
			args:  []string{"-null", `\N`},
			want:  "a,b\n\\N,\n",
		},
		{
			name:  "single empty field",
			input: "INSERT INTO t (a) VALUES ('');",
			want:  "a\n\"\"\n",
		},
		{
			name:  "MySQL backslash escapes",
			input: `INSERT INTO t (a, b) VALUES ('it\'s', "C:\\path");`,
			args:  []string{"-dialect", "mysql"},
			want:  "a,b\nit's,C:\\path\n",
		},
		{
			name:  "MySQL NO_BACKSLASH_ESCAPES",
			input: `INSERT INTO t (a) VALUES ('C:\path\n');`,
			args:  []string{"-dialect", "MySQL", "-no-backslash-escapes"},
			want:  "a\nC:\\path\\n\n",
		},
		{
			name: "MySQL syntax",
			input: "/*!40101 SET NAMES utf8mb4 */;\n" +
				"LOCK TABLES `t` WRITE;\n" +
				"INSERT /*!IGNORE*/ LOW_PRIORITY INTO `db`.`t` PARTITION (p0) (`a`) VALUES ROW(1), ROW(2)\n" +
				"  AS new ON DUPLICATE KEY UPDATE a = new.a;\n" +
				"REPLACE DELAYED t SET a = 3, `b` := 'x';\n" +
				"UNLOCK TABLES;",
			args: []string{"-dialect", "mysql"},
			want: "a\n1\n2\n\na,b\n3,x\n",
		},
		{
			name:  "PostgreSQL syntax",
			input: `INSERT INTO "s"."t" AS x ("a", b) VALUES ($$it's$$, E'\t') RETURNING *;`,
			args:  []string{"-dialect", "postgres"},
			want:  "a,b\nit's,\"\t\"\n",
		},
		{
			name:  "SQLite syntax",
			input: "INSERT OR REPLACE INTO [u] ([b], `c`) VALUES (0x10, 1.0)",
			args:  []string{"-dialect", "sqlite"},
			want:  "b,c\n16,1.0\n",
		},
		{
			name:  "SQL Server syntax",
			input: "INSERT INTO [dbo].[t] ([a]) VALUES (N'x' + N'y')",
			args:  []string{"-dialect", "mssql"},
			want:  "a\nxy\n",
		},
		{
			// Statements of T-SQL need not be terminated by semicolons, and GO separates batches.
			name: "SQL Server statements",
			input: "SET IDENTITY_INSERT [t] ON\nGO\n" +
				"INSERT [t] ([a]) VALUES (1)\n" +
				"INSERT [t] ([a]) VALUES (2), (3) SELECT 1 AS go\n" +
				"go 2\n" +
				"GRANT SELECT, INSERT ON t TO u INSERT INTO t (a) VALUES (4)\n" +
				"CREATE TRIGGER tr ON t AFTER INSERT AS INSERT INTO log VALUES (0)\nGO\n" +
				"INSERT t (a) VALUES (5) PRINT 1",
			args: []string{"-dialect", "mssql"},
			want: "a\n1\n2\n3\n4\n5\n",
		},
		{
			name: "DELIMITER",
			input: "DELIMITER ;;\n" +
				"CREATE TRIGGER tr AFTER INSERT ON t FOR EACH ROW BEGIN INSERT INTO log VALUES (NEW.id); END;;\n" +
				"DELIMITER ;\n" +
				"INSERT INTO t VALUES (1);",
			args: []string{"-dialect", "mysql"},
			want: "1\n1\n",
		},
		{
			name:  "keywords as names and values",
			input: "INSERT INTO value (value, returning) VALUES (CAST(1 AS CHAR), 'on');",
			want:  "value,returning\nCAST(1 AS CHAR),on\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { checkRun(t, tt.input, tt.want, tt.args...) })
	}
}

func TestRunErrors(t *testing.T) {
	input := "INSERT INTO t (a) VALUES (1);\n" +
		"INSERT INTO t (a, b) VALUES (2);\n" +
		"INSERT INTO t (a, A) VALUES (3, 4);\n" +
		"INSERT INTO t VALUES (1), (2, 3);\n" +
		"INSERT INTO t (a) SELECT 1;\n" +
		"INSERT INTO t (a) VALUES (4) (5);\n" +
		"INSERT INTO t (a) VALUES (6);\n" +
		"INSERT INTO t (a) VALUES ('unterminated);\n"
	out, errOut, status := runQsert(t, input)
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
	if want := "a\n1\n6\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	wantErr := "qsert: <stdin>:2: column count doesn't match value count at row 1\n" +
		"qsert: <stdin>:3: duplicate column \"A\"\n" +
		"qsert: <stdin>:4: column count doesn't match value count at row 2\n" +
		"qsert: <stdin>:5: INSERT ... SELECT is not supported\n" +
		"qsert: <stdin>:6: syntax error near \"(\"\n" +
		"qsert: <stdin>:8: unterminated quoted string\n"
	if errOut != wantErr {
		t.Errorf("stderr = %q, want %q", errOut, wantErr)
	}
}

func TestRunUnknownDialect(t *testing.T) {
	_, errOut, status := runQsert(t, "", "-dialect", "db2")
	if status != 2 || !strings.Contains(errOut, `unknown dialect "db2"`) {
		t.Errorf("status = %d, stderr = %q", status, errOut)
	}
}

func TestRunDialectShorthand(t *testing.T) {
	checkRun(t, `INSERT INTO t (s) VALUES ('a\'b');`, "s\na'b\n", "-i", "mysql")
}

func TestRunSelect(t *testing.T) {
	input := "INSERT INTO users (id, name, email) VALUES (1, 'Alice', 'a@example.com'), (2, 'Bob', NULL);\n" +
		"INSERT INTO items (id, title) VALUES (10, 'Pen');\n" +
		"INSERT INTO logs (msg) VALUES ('hello');\n" +
		"INSERT INTO users (id, name) VALUES (3, 'Carol');\n" +
		"INSERT INTO db.users VALUES ('Dave');\n"
	tests := []struct {
		sel, want, wantErr string
	}{
		{
			sel:  ".id, .name, .users.email",
			want: "id,name,users.email\n1,Alice,a@example.com\n2,Bob,NULL\n10,NULL,NULL\n3,Carol,\n",
		},
		{
			sel:  ".title, .ID",
			want: "title,ID\nNULL,1\nNULL,2\nPen,10\nNULL,3\n",
		},
		{
			sel:     ".db.users.1, .logs.nope",
			want:    "db.users.1,logs.nope\nNULL,NULL\nDave,NULL\n",
			wantErr: "qsert: warning: no table has the column .logs.nope\n",
		},
		{
			sel:     `.users.id AS user_id, .name as "Full, Name", .id AS id, .nope AS x`,
			want:    "user_id,\"Full, Name\",id,x\n1,Alice,1,NULL\n2,Bob,2,NULL\nNULL,NULL,10,NULL\n3,Carol,3,NULL\n",
			wantErr: "qsert: warning: no table has the column .nope\n",
		},
	}
	for _, tt := range tests {
		out, errOut, status := runQsert(t, input, "-null", "NULL", "-e", tt.sel)
		if status != 0 || errOut != tt.wantErr {
			t.Errorf("%s: status = %d, stderr = %q", tt.sel, status, errOut)
		}
		if out != tt.want {
			t.Errorf("%s: stdout = %q, want %q", tt.sel, out, tt.want)
		}
	}
	if _, _, status := runQsert(t, input, "-e", "id"); status != 2 {
		t.Errorf("invalid selector: status = %d, want 2", status)
	}
}

// The expected array elements of PostgreSQL were confirmed with PostgreSQL 17.
func TestRunSelectArray(t *testing.T) {
	input := "INSERT INTO t (id, tags, m) VALUES\n" +
		"  (1, ARRAY[ 'a' , 'b' || 'c' /* x */, NULL, NOW() ], ARRAY[[1, 2], [3, 4 + 1]]),\n" +
		`  (2, '{x, "y z", NULL, \{w}', '{{1,2},{3,4}}'),` + "\n" +
		"  (3, ARRAY[1,2]::bigint[], CAST('{5,6}' AS int[])),\n" +
		`  (4, '{"a":1}'::jsonb, ARRAY[]::int[]),` + "\n" +
		"  (5, NULL, 7);\n"
	tests := []struct {
		dialect, sel, want string
	}{
		{
			dialect: "postgres",
			sel:     ".id, .tags",
			want: "id,tags\n" +
				"1,\"ARRAY['a', 'b' || 'c', NULL, NOW()]\"\n" +
				"2,\"{x, \"\"y z\"\", NULL, \\{w}\"\n" +
				"3,\"ARRAY[1, 2]::bigint[]\"\n" +
				"4,\"'{\"\"a\"\":1}'::jsonb\"\n" +
				"5,NULL\n",
		},
		{
			dialect: "postgres",
			sel:     ".tags[0], .tags[1], .tags[2], .tags[-1], .tags[9], .m[1][1], .m[-1], .id[0]",
			want: "tags[0],tags[1],tags[2],tags[-1],tags[9],m[1][1],m[-1],id[0]\n" +
				"a,bc,NULL,NOW(),NULL,5,\"[3, 4 + 1]\",NULL\n" +
				"x,y z,NULL,{w,NULL,4,\"{3,4}\",NULL\n" +
				"1,2,NULL,2,NULL,NULL,6,NULL\n" +
				"NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL\n" +
				"NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL\n",
		},
		{
			// '{...}' is a string in standard SQL.
			dialect: "iso",
			sel:     ".tags[1], .m[1][0]",
			want:    "tags[1],m[1][0]\nbc,3\nNULL,NULL\n2,NULL\nNULL,NULL\nNULL,NULL\n",
		},
		{
			// MySQL has no array types.
			dialect: "mysql",
			sel:     ".tags[0]",
			want:    "tags[0]\nNULL\nNULL\nNULL\nNULL\nNULL\n",
		},
	}
	for _, tt := range tests {
		checkRun(t, input, tt.want, "-dialect", tt.dialect, "-null", "NULL", "-e", tt.sel)
	}
}

func TestRunSelectJSON(t *testing.T) {
	input := "INSERT INTO t (id, doc) VALUES\n" +
		`  (1, '{"name": "Alice", "tags": ["x", "y"], "addr": {"zip": "100" , "city": null}, "n": 1.50, "ok": true}'),` +
		"\n" +
		`  (2, CAST('{"name": "Bob", "name": "Robert", "a b": [{"id": 7}]}' AS JSON)),` + "\n" +
		`  (3, '[10, 20, 30]'),` + "\n" +
		"  (4, 'not json'),\n" +
		"  (5, NULL),\n" +
		`  (6, '{"name": ' || '"carol"}');` + "\n"
	tests := []struct {
		dialect, sel, want string
	}{
		{
			dialect: "iso",
			sel:     `.id, .doc[name], .doc[ tags ][1], .doc[tags][-2], .doc[addr], .doc[addr][city]`,
			want: `id,doc[name],doc[tags][1],doc[tags][-2],doc[addr],doc[addr][city]` +
				"\n" +
				"1,Alice,y,x,\"{\"\"zip\"\": \"\"100\"\" , \"\"city\"\": null}\",NULL\n" +
				"2,NULL,NULL,NULL,NULL,NULL\n" +
				"3,NULL,NULL,NULL,NULL,NULL\n" +
				"4,NULL,NULL,NULL,NULL,NULL\n" +
				"5,NULL,NULL,NULL,NULL,NULL\n" +
				"6,carol,NULL,NULL,NULL,NULL\n",
		},
		{
			dialect: "iso",
			sel:     `.doc["n"], .doc["ok"], .doc["a b"][0]["id"], .doc[2], .doc["tags"]["x"], .id["x"]`,
			want: `doc[n],doc[ok],doc[a b][0][id],doc[2],doc[tags][x],id[x]` +
				"\n" +
				"1.50,true,NULL,NULL,NULL,NULL\n" +
				"NULL,NULL,7,NULL,NULL,NULL\n" +
				"NULL,NULL,NULL,30,NULL,NULL\n" +
				"NULL,NULL,NULL,NULL,NULL,NULL\n" +
				"NULL,NULL,NULL,NULL,NULL,NULL\n" +
				"NULL,NULL,NULL,NULL,NULL,NULL\n",
		},
	}
	for _, tt := range tests {
		checkRun(t, input, tt.want, "-dialect", tt.dialect, "-null", "NULL", "-e", tt.sel)
	}

	// The member of duplicate names is the last one in MySQL and PostgreSQL, the first one in SQLite and SQL Server,
	// and undefined in the others.
	for dialect, want := range map[string]string{"mysql": "2", "postgres": "2", "sqlite": "1", "mssql": "1", "iso": "NULL"} {
		if got := selectValue(t, dialect, `'{"a": 1, "a": 2}'`, `.v["a"]`); got != want {
			t.Errorf("duplicate names with %s = %q, want %q", dialect, got, want)
		}
	}

	// An integer selects an element of an array, or the member of that name of an object, whether quoted or not.
	for _, tt := range []struct{ value, sel, want string }{
		{`'{"0": "z", "01": "y", "-1": "x"}'`, ".v[0]", "z"},
		{`'{"0": "z", "01": "y", "-1": "x"}'`, `.v["01"]`, "y"},
		{`'{"0": "z", "01": "y", "-1": "x"}'`, ".v[-1]", "x"},
		{`'{"0": "z", "01": "y", "-1": "x"}'`, ".v[1]", "NULL"},
		{`'[5, 6]'`, ".v[01]", "6"},
		{`'[5, 6]'`, `.v["-1"]`, "6"},
		{`'[5, 6]'`, ".v[x]", "NULL"},
		{`'{"a b": {"c]": 1}}'`, `.v[ a b ]["c]"]`, "1"},
	} {
		if got := selectValue(t, "iso", tt.value, tt.sel); got != tt.want {
			t.Errorf("%s %s = %q, want %q", tt.value, tt.sel, got, tt.want)
		}
	}

	pg := "INSERT INTO t (a, b, c, d) VALUES (" +
		`'{"k": [1, {"v": "w"}]}'::jsonb, ` +
		`'{"k": 1}'::text, ` +
		`ARRAY['{"k": 2}'::json, '{"k": 3}'], ` +
		`'{"{\"k\": 4}", NULL}'::jsonb[]);` + "\n"
	checkRun(t, pg, `a,a[k][1][v],b[k],c[1][k],d[0][k],d[1][k]`+"\n"+
		`"'{""k"": [1, {""v"": ""w""}]}'::jsonb",w,NULL,3,4,NULL`+"\n",
		"-dialect", "postgres", "-null", "NULL", "-e", `.a, .a["k"][1]["v"], .b["k"], .c[1]["k"], .d[0]["k"], .d[1]["k"]`)
}

// The expected values of PostgreSQL and SQLite were confirmed with PostgreSQL 17 and SQLite 3.53.
func TestRunSelectJSONConstructor(t *testing.T) {
	tests := []struct {
		dialect, value, sel, want string
	}{
		// PostgreSQL
		{
			"postgres", `JSON_OBJECT('a': 1, 'b' VALUE NULL, 'c' VALUE true, 'd': 1.50, 'e': 'x' || 'y')`,
			`.v["a"], .v["b"], .v["c"], .v["d"], .v["e"], .v["z"]`, "1,NULL,true,1.50,xy,NULL",
		},
		{"postgres", `JSON_OBJECT('a': NULL, 'b': 2 ABSENT ON NULL)`, `.v["a"], .v["b"]`, "NULL,2"},
		{"postgres", `JSON_ARRAY(1, NULL, 'x')`, `.v[0], .v[1], .v[2]`, "1,x,NULL"},
		{"postgres", `JSON_ARRAY(1, NULL, 'x' NULL ON NULL)`, `.v[0], .v[1], .v[2]`, "1,NULL,x"},
		{"postgres", `JSON_ARRAY(1, NOW(), 'x' NULL ON NULL)`, `.v[1], .v[2]`, "NOW(),x"},
		// NOW() might be omitted as NULL.
		{"postgres", `JSON_ARRAY(1, NOW(), 'x')`, `.v[0]`, "NULL"},
		{
			"postgres", `json_build_array(1, NULL, true, '{"x":1}', '{"x":1}'::json, ARRAY[2, 3])`,
			`.v[1], .v[2], .v[3], .v[3]["x"], .v[4]["x"], .v[5][-1]`, `NULL,true,"{""x"":1}",NULL,1,3`,
		},
		{
			"postgres", `JSON_OBJECT('a': '{"x":[5]}' FORMAT JSON, 'b': '{"x":1}'::jsonb, 'c': '{"x":1}')`,
			`.v["a"]["x"][0], .v["b"]["x"], .v["c"]["x"]`, "5,1,NULL",
		},
		{
			"postgres", `json_build_object('a', 1, 'a', 2, 3, json_build_array(4, json_build_object('k', 'v')))`,
			`.v["a"], .v["3"][1]["k"], .v["3"][-1]`, `2,v,"json_build_object('k', 'v')"`,
		},
		{"postgres", `JSON_OBJECT('a': 1, 'a': 2 WITH UNIQUE KEYS)`, `.v["a"]`, "NULL"},
		{"postgres", `JSON_OBJECT('a': 1, 'a': 2 WITHOUT UNIQUE RETURNING jsonb)`, `.v["a"]`, "2"},
		{"postgres", `JSON_OBJECT(1 VALUE 2, true: 3)`, `.v["1"], .v["true"]`, "2,3"},
		{"postgres", `JSON_OBJECT('a': '[1,2]'::json)::jsonb`, `.v["a"][1]`, "2"},
		{"postgres", `json_build_object('a')`, `.v["a"]`, "NULL"},
		// SQLite
		{
			"sqlite", `json_object('a', 1, 'a', 2, 'b', NULL, 'c', 1.50, 'd', true, 'e', json('[1,{"f":"g"}]'), ` +
				`'h', '[1]', 'i', json_array(1, 2.5, 'x'))`,
			`.v["a"], .v["b"], .v["c"], .v["d"], .v["e"][1]["f"], .v["h"][0], .v["h"], .v["i"][-1]`,
			"1,NULL,1.5,1,g,NULL,[1],x",
		},
		{"sqlite", `json_object(1, 2)`, `.v["1"]`, "NULL"},
		{"sqlite", `json_object('a', x'00')`, `.v["a"]`, "NULL"},
		// MySQL
		{
			"mysql", `JSON_OBJECT('a', 1, 'b', TRUE, 'a', 3, 'c', JSON_ARRAY(1, NULL, CAST('{"x":9}' AS JSON)))`,
			`.v["a"], .v["b"], .v["c"][1], .v["c"][-1]["x"]`, "3,true,NULL,9",
		},
		{"mysql", `JSON_OBJECT('a': 1)`, `.v["a"]`, "NULL"},
		// MySQL stores numbers in JSON text as integers or doubles, and the quotient of a division has more digits.
		{
			"mysql", `'[1.50, 1e0, 1e14, 1e-7, -0.0, 12345678901234567890, 123456789012345678901234567890, -0]'`,
			`.v[0], .v[1], .v[2], .v[3], .v[4], .v[5], .v[6], .v[7]`,
			"1.5,1.0,100000000000000.0,0.0000001,-0.0,12345678901234567890,123456789012345678901234567890,0",
		},
		{"mysql", `JSON_ARRAY(1e0, -0e0, 1e15, 1.0, 1 / 3)`, `.v[0], .v[1], .v[2], .v[3], .v[4]`, "1.0,-0.0,1e15,1.0,1 / 3"},
		// SQL Server has neither VALUE nor FORMAT JSON.
		{"mssql", `JSON_OBJECT('a': 1, 'b': NULL)`, `.v["a"], .v["b"]`, "1,NULL"},
		{"mssql", `JSON_OBJECT('a' VALUE 1)`, `.v["a"]`, "NULL"},
		{"mssql", `JSON_ARRAY(1, '[2]' FORMAT JSON)`, `.v[0]`, "NULL"},
		// Oracle
		{
			"oracle", `JSON_OBJECT(KEY 'a' VALUE 1, 'c' VALUE JSON_ARRAY(1, NULL NULL ON NULL), 'd' VALUE '' ` +
				`ABSENT ON NULL RETURNING CLOB)`,
			`.v["a"], .v["c"][1], .v["c"][0], .v["d"]`, "1,NULL,1,NULL",
		},
		// Standard SQL
		{"iso", `JSON_OBJECT('a': 1, 'a': 2)`, `.v["a"]`, "NULL"},
		{"iso", `JSON_OBJECT('a': '', 'b': JSON_ARRAY('[1]' FORMAT JSON))`, `.v["a"], .v["b"][0][0]`, ",1"},
	}
	for _, tt := range tests {
		if got := selectValue(t, tt.dialect, tt.value, tt.sel); got != tt.want {
			t.Errorf("%s %s %s = %q, want %q", tt.dialect, tt.value, tt.sel, got, tt.want)
		}
	}
}

// The expected elements of CQL collections were confirmed with Cassandra 5.0.
func TestRunCassandra(t *testing.T) {
	input := "// CQL\n" +
		"INSERT INTO ks.u (k, emails, tags, prefs, addr, pt, si, sd, su, sb, mi)\n" +
		"VALUES (1, ['a@x.com', 'b@x.com'], {'z', 'a', 'B', 'é', 'a'}, {'theme': 'dark', 'theme': 'light', 'lang': 'ja'},\n" +
		`  {Street: 'Main', "Zip": '100', nested: {'k': [1, 2]}}, (1, 'x', [3]), {3, -1, 2, 2}, {1.5, 1, 1e0},` + "\n" +
		"  {5b6962dd-3f90-4c93-8f61-eabfa4a803e2, 1b6962dd-3f90-4c93-8f61-eabfa4a803e2}, {0xff, 0x01, 0x0100}, {2: 'b'})\n" +
		"IF NOT EXISTS USING TTL 86400;\n" +
		`INSERT INTO ks.u JSON '{"k": 2, "emails": ["c@x.com"], "prefs": {"k": "v"}, "\"Quoted\"": null}' DEFAULT UNSET;` + "\n" +
		"BEGIN UNLOGGED BATCH USING TIMESTAMP 1\n" +
		"  INSERT INTO ks.u (k, si) VALUES (3, {30, 10, 20})\n" +
		"  UPDATE ks.u SET si = {} WHERE k = 1;\n" +
		"  INSERT INTO ks.u (k, si) VALUES (4, {})\n" +
		"APPLY BATCH;\n"
	tests := []struct {
		sel, want string
	}{
		{
			// A column that a statement does not have is empty.
			sel: ".k, .tags, .prefs, .Quoted",
			want: "k,tags,prefs,Quoted\n" +
				"1,\"{'z', 'a', 'B', 'é', 'a'}\",\"{'theme': 'dark', 'theme': 'light', 'lang': 'ja'}\",\n" +
				"2,,\"{\"\"k\"\": \"\"v\"\"}\",NULL\n" +
				"3,,,\n" +
				"4,,,\n",
		},
		{
			// Sets are sorted without duplicates, and which of the duplicate keys of a map is stored is unknown.
			sel: `.emails[-1], .tags[0], .tags[-1], .prefs["theme"], .addr["street"], .addr["Zip"], .addr["zip"], ` +
				`.addr["nested"]["k"][1], .pt[2][0], .si[0], .si[-1], .sb[1], .mi[2]`,
			want: `emails[-1],tags[0],tags[-1],prefs[theme],addr[street],addr[Zip],addr[zip],` +
				`addr[nested][k][1],pt[2][0],si[0],si[-1],sb[1],mi[2]` + "\n" +
				"b@x.com,B,é,NULL,Main,100,NULL,2,3,-1,3,0x0100,b\n" +
				"c@x.com,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL\n" +
				"NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,10,30,NULL,NULL\n" +
				"NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL\n",
		},
		{
			// The order of UUIDs, and which of 1 and 1e0 is stored, are unknown.
			sel:  ".su[0], .sd[0], .prefs[\"k\"]",
			want: "su[0],sd[0],prefs[k]\nNULL,NULL,NULL\nNULL,NULL,v\nNULL,NULL,NULL\nNULL,NULL,NULL\n",
		},
	}
	for _, tt := range tests {
		checkRun(t, input, tt.want, "-dialect", "cassandra", "-null", "NULL", "-e", tt.sel)
	}
}

// The expected values of Spanner were confirmed with the Cloud Spanner emulator.
func TestRunSpanner(t *testing.T) {
	input := "# GoogleSQL\n" +
		"INSERT INTO T (K, `S`, J, A, AS1, AF)\n" +
		`VALUES (1, 'it\'s "q"', JSON '{"b": [1, 2.50, {"c": true}], "a": "x", "a": "y"}', [3, 1, 2],` + "\n" +
		`  ARRAY<STRING>['x', NULL, """tri'ple"""], ARRAY[0.1 + 0.2, 1e20]),` + "\n" +
		"  (2, r'raw\\n', PARSE_JSON('[10, 1e2]'), [], NULL, [CAST(1 AS FLOAT64)])\n" +
		"THEN RETURN K;\n" +
		"INSERT OR UPDATE INTO T (K, S, J) VALUES (3, CONCAT('a', 'b', \"c\"), JSON_OBJECT('k', [1, 2], 'k', 3))\n" +
		"  ASSERT_ROWS_MODIFIED 1;\n" +
		"INSERT UPDATE T (K) VALUES (4) ON CONFLICT (K) DO UPDATE SET S = excluded.S;\n"
	checkRun(t, input, "K,S,AS1,AF\n"+
		"1,\"it's \"\"q\"\"\",\"ARRAY<STRING>['x', NULL, \"\"\"\"\"\"tri'ple\"\"\"\"\"\"]\",\"ARRAY[0.1 + 0.2, 1e20]\"\n"+
		"2,raw\\n,NULL,[CAST(1 AS FLOAT64)]\n"+
		"3,abc,,\n"+
		"4,,,\n",
		"-dialect", "spanner", "-null", "NULL", "-e", ".K, .S, .AS1, .AF")
	// The first of the duplicate names of a JSON object is selected.
	checkRun(t, input, `J[a],J[b][1],J[b][2][c],J[1],J[k][1],A[0],AS1[1],AS1[2],AF[0],AF[1]`+"\n"+
		"x,2.5,true,NULL,NULL,3,NULL,tri'ple,0.30000000000000004,1e+20\n"+
		"NULL,NULL,NULL,100.0,NULL,NULL,NULL,NULL,CAST(1 AS FLOAT64),NULL\n"+
		"NULL,NULL,NULL,NULL,2,NULL,NULL,NULL,NULL,NULL\n"+
		"NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL\n",
		"-dialect", "spanner", "-null", "NULL", "-e",
		`.J["a"], .J["b"][1], .J["b"][2]["c"], .J[1], .J["k"][1], .A[0], .AS1[1], .AS1[2], .AF[0], .AF[1]`)
}
