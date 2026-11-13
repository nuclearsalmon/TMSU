# VFS SQL timeout

VFS transactions use a shared 30-second SQL context deadline. Set
`TMSU_VFS_SQL_TIMEOUT` in the environment of the mounting process to a positive
Go duration such as `5s` or `2m` to change it; invalid values prevent mounting.
Non-VFS storage operations retain their existing behavior. When cancellation
is observed before commit, the operation returns an I/O error and the transaction
is rolled back (possibly by `database/sql` itself). Other unsuccessful operations
also attempt rollback. If commit has already started when the deadline expires,
it may still succeed; a successful commit is reported as success, not rolled
back or reported as a timeout.

This is not a hard wall-clock limit on a filesystem operation. Filesystem I/O,
SQLite lock waits or driver calls that do not promptly honor cancellation, and
transaction cleanup can exceed the deadline. The deadline starts when the VFS
transaction begins, not necessarily at the start of the FUSE request.
