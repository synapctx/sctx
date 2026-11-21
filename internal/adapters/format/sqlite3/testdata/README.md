Captured with the macOS `sqlite3` 3.54.0 shell against a file database
(customers 200 rows, orders 2000 rows). `box`, `table`, `md`, `column` and `csv`
are `-box`, `-table`, `-markdown`, `-column -header` and `-csv -header` of 230
orders rows; `list*` is the default list mode; `line*` is `-line`; `json`,
`tables`, `schema*`, `pragma` are shapes that must be declined. The failing
runs (`syntax`, `missing`, `nodb`) exit 1; the database path in `nodb` was
rewritten to /work/proj/x.db.
