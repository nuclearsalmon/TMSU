# Traversal storage benchmark

Run from the repository root with Go, a C compiler and the module dependencies available:

```sh
go test ./benchmark -count=1
go test ./benchmark -run '^$' -bench '^BenchmarkTraversalNames$' -benchmem -benchtime=1s -count=5
```

Compare repeated runs on the same machine, Go/compiler and SQLite dependency versions, ideally using `benchstat` on saved outputs. Record the revision, CPU, OS and toolchain; avoid comparing numbers from different hardware or under different system load. Go's benchmark output includes CPU and OS/architecture. For example:

```sh
go test ./benchmark -run '^$' -bench '^BenchmarkTraversalNames$' -benchmem -benchtime=1s -count=5 > before.txt
# Repeat after the change into after.txt, then: benchstat before.txt after.txt
```

Each fixture is a fresh temporary database containing 1,024 synthetic file records, six tags, three values and a two-hop implication. No recorded file path is opened and no existing TMSU database is used. Common matches all files, even matches half, and quarter matches one quarter; genre values differ by parity. Tests check nested intersections, parent-restricted values, implications and an empty result. They compare the ordered results of the storage API, not the order of FUSE directory entries.

Each benchmark operation begins a transaction, calls `Storage.VFSNames`, consumes its full result, and rolls back. Setup and one warm-up call are excluded. `broad-tags` discovers tags for all files; `nested-tags` uses a three-tag intersection; `restricted-values` discovers genre values for the even subset. These are warm-cache, single-threaded storage-query measurements; they do not include FUSE, directory entry construction, filesystem walking, file-result enumeration, cold-cache behavior, or a full recursive traversal. Database size, tag fan-out and match distributions are intentionally fixed, so extrapolation to other datasets requires additional workloads. Sorting of results is already part of the query being measured; this does not implement canonical traversal ordering.
