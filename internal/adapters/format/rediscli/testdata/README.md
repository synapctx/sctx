Captured with the `redis:7` client against a `redis:7` server over a
user-defined docker network (2000 `user:N` strings, 2000 `h:N` hashes, a
300-item list `q`). stdout is not a terminal, so replies are raw (no `1)`
numbering). `*.exit` holds the real status: redis-cli exits 0 on a Redis error
reply (`wrongtype`, `unknown`, `args`, `auth`) and 1 only when it cannot
connect (`refused*`). Client IPs were rewritten to 192.0.2.9.
