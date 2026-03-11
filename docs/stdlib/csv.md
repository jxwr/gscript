# csv

The `csv` library provides functions for parsing and encoding CSV data.

## Functions

### csv.parse(str [, opts]) -> table

Parse a CSV string into a table of rows. Each row is a table of string values with 1-based integer keys.

Options table:
- `sep` (string) -- field separator (default `","`)
- `comment` (string) -- comment character (lines starting with this are skipped)
- `trimSpace` (bool) -- trim leading space from fields
- `lazyQuotes` (bool) -- allow lazy quoting

```
data := "a,b,c\n1,2,3\n4,5,6"
rows := csv.parse(data)
-- rows[1][1] == "a", rows[1][2] == "b", rows[1][3] == "c"
-- rows[2][1] == "1", rows[2][2] == "2", rows[2][3] == "3"

-- Tab-separated:
rows := csv.parse(data, {sep: "\t"})
```

### csv.parseWithHeaders(str [, opts]) -> table

Parse a CSV string where the first row contains headers. Returns a table of rows where each row is a table with header-name keys.

```
data := "name,age,city\nAlice,30,NYC\nBob,25,LA"
rows := csv.parseWithHeaders(data)
-- rows[1].name == "Alice"
-- rows[1].age == "30"
-- rows[2].name == "Bob"
```

### csv.encode(rows [, opts]) -> string

Encode a table of rows (each row is an array table of strings) into a CSV string.

Options table:
- `sep` (string) -- field separator (default `","`)

```
rows := {
    {"a", "b", "c"},
    {"1", "2", "3"}
}
result := csv.encode(rows)
-- result == "a,b,c\n1,2,3\n"
```

### csv.encodeWithHeaders(rows, headers [, opts]) -> string

Encode a table of row-tables (with header-name keys) into a CSV string, prepending a header row.

```
rows := {
    {name: "Alice", age: "30"},
    {name: "Bob", age: "25"}
}
headers := {"name", "age"}
result := csv.encodeWithHeaders(rows, headers)
-- result == "name,age\nAlice,30\nBob,25\n"
```
