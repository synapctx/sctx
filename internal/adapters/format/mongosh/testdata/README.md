Captured against a `mongo:8` server with the `mongosh` shipped in the `mongo:8`
image over a user-defined docker network (collection `users`, 2000 documents).
`find240` = `find({},{tags:0}).limit(240).toArray()` default shell output and
`--json=relaxed`; `findone`, `find20` = default output; `*.stderr` = failing
runs (exit 1).
