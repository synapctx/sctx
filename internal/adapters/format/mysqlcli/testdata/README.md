Captured with the `mysql:8.4` client against a `mysql:8.4` server over a
user-defined docker network (customers 200 rows, orders 2000 rows). Stdout is
not a terminal, so the default is tab-separated batch output; `table*` = `-t`,
`vert*` = `-E`, `N` = `-B -N`. `*.exit` holds the real exit status; every
stderr starts with the client's password-on-command-line notice. The client IP
in the auth failure was rewritten to 192.0.2.7.
