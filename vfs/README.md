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

# Portable tag and value names

The VFS escapes Windows-forbidden filename characters before returning tag and
value directory names. A zero-width marker plus a visually similar Unicode
character represents each escaped character; the marker itself is doubled, so
literal marker sequences remain distinct. Trailing dots and spaces, control
characters, and Windows device names are also escaped. Tag names that conflict
with the `files` or `README.md` VFS entries, or begin with `=`, receive a
reserved-name prefix.

The database names are unchanged. Ordinary names keep their existing paths;
names containing `:`, other Windows-forbidden characters, a trailing dot or
space, or a literal escape sequence use their escaped spelling. Legacy
unescaped spellings remain accepted when they are unambiguous. Special names
and device names require their escaped path. Existing scripts or bookmarks
using a changed spelling should use the displayed VFS name.

# Recursive traversal

Tag permutations remain available so clients can browse the same files under
any selected-tag order. A generated listing omits tags already selected in its
path, while manually constructed repeated-tag paths remain valid. A full walk
can therefore still grow combinatorially with co-occurring tags; the SQL
deadline limits one database operation, not a whole walk. No traversal-depth
or full-walk bound is enforced.

# Virtual file operations

Deleting a file link through a tag path removes that path's explicit tag/value
association. It never removes the physical file; if that was the last explicit
association, its database file record is removed. Implied associations and
query-view links cannot be deleted this way.

Copying a file link adds every tag/value association selected by the destination
tag path and retains all existing associations. Moving a file link adds the
destination path's associations and removes source-path associations that are
not also selected at the destination. Move requires those removed source
associations to be explicit, and the link name must stay the same. Both
operations keep the original physical file. Since Delete only untags, these
operations do not use a recycle directory.

These callback semantics do not enable writes by themselves. The NixOS tag
shares and client CIFS mount remain read-only until the SMB/Dolphin path is
validated separately.
