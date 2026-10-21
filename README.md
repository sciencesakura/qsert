# qsert: SELECT from INSERT.

Query your INSERT statements, no database required.

![](https://github.com/sciencesakura/qsert/actions/workflows/check.yaml/badge.svg)

qsert reads SQL `insert` statements, such as those in a dump file or a migration script, and prints their values as
CSV. Expressions in the values are evaluated according to the dialect of the DBMS you choose. With `-e`, you can pick
specific columns, array elements and JSON members, like a tiny `select` over your `insert`s.

- Supports PostgreSQL, MySQL, SQLite, Oracle, SQL Server, Cassandra and Spanner.
- Evaluates literals, operators and common functions according to the dialect of the DBMS you choose. An expression
  it can't evaluate reliably is printed as written, never guessed.
- Selects elements of arrays and members of JSON by index or key.
- Ignores statements other than `insert` and `replace`, so a whole dump can be fed in as is.

## Example

```sql
-- dump.sql
create table users (id int, name varchar(50), profile json);
insert into users (id, name, profile) values
  (1, 'Alice', '{"age": 30, "tags": ["admin", "dev"]}'),
  (2, concat('B', 'ob'), json_object('age', 25, 'tags', json_array('dev'))),
  (3, 'Carol', null);
insert into orders (id, user_id, total) values (10, 1, 19.99 * 2), (11, 2, now());
```

Convert every table to CSV. Tables are separated by an empty line:

```console
$ qsert -dialect mysql dump.sql
id,name,profile
1,Alice,"{""age"": 30, ""tags"": [""admin"", ""dev""]}"
2,Bob,"json_object('age', 25, 'tags', json_array('dev'))"
3,Carol,

id,user_id,total
10,1,39.98
11,2,now()
```

`now()` depends on when it runs, so it is printed as written rather than evaluated.

Select columns, JSON members and array elements:

```console
$ qsert -dialect mysql -e '.users.id, .name, .profile[age], .profile[tags][0]' dump.sql
users.id,name,profile[age],profile[tags][0]
1,Alice,30,admin
2,Bob,25,dev
3,Carol,,
```

## Usage

```shell
qsert [options] [file ...]
```

qsert reads the given files, or the standard input if no file or `-` is given, and writes CSV to the standard output.

### Options

| Option                       | Description                                                                                                  |
|------------------------------|--------------------------------------------------------------------------------------------------------------|
| `-dialect name`, `-i name`   | SQL dialect: `iso` (default), `postgres`, `mysql`, `sqlite`, `oracle`, `mssql`, `cassandra`, `spanner`.      |
| `-e selectors`               | Comma-separated selectors of the columns to output, such as `'.id, .users.email, .tags[0], .doc[key]'`.      |
| `-null text`                 | Text to output for NULL (default: empty).                                                                    |
| `-no-backslash-escapes`      | With `-dialect mysql`, treat backslashes in string literals as ordinary characters (`NO_BACKSLASH_ESCAPES`). |
| `-h`                         | Show the help.                                                                                               |

### Selectors

| Selector                            | Selects                                                                             |
|-------------------------------------|-------------------------------------------------------------------------------------|
| `.column`                           | The column of any table.                                                            |
| `.table.column`, `.db.table.column` | The qualified column name.                                                          |
| `."column name"`                    | A column whose name needs quoting.                                                  |
| `.column[0]`, `.column[-1]`         | An element of an array, list, set or tuple (0-based; negative counts from the end). |
| `.column[key]`, `.column["key"]`    | A member of a JSON object, or of a map or user-defined type.                        |
| `.column as name`                   | Assign an alias to a column.                                                        |

Selectors can be chained, as in `.column[items][0][id]`. A selector that matches nothing outputs NULL.

A key in brackets is the text without the surrounding spaces, as in `.column[first name]`. Quote it as a JSON string
if it contains `]`, starts with `"` or has surrounding spaces, as in `.column["a]b"]`. An integer selects an element
of an array, or the member of that name of an object, such as the key `1` of a `map<int, text>`.

### Exit status

`0` on success, `1` if any statement or file could not be processed (the rest is still output), and `2` for invalid
options.

## Installation

```sh
go install github.com/sciencesakura/qsert/cmd/qsert@latest
```

## Building

Requires Go 1.26 or later.

```sh
git clone https://github.com/sciencesakura/qsert.git
cd qsert
make            # regenerate parsers and build ./qsert
make lint       # run golangci-lint
make test       # run tests
```

## License

qsert is licensed under the [GNU General Public License v3.0](LICENSE).
