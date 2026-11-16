# Traversal benchmarks

Run from the repository root with Go, a C compiler and the module dependencies available:

```sh
go test ./benchmark ./vfs
go test ./benchmark ./vfs -run '^$' -bench '^(BenchmarkTraversalNames|BenchmarkVFSListingsAndWalk)$' -benchmem -benchtime=500ms -count=3
```

Fixtures use disposable SQLite databases and synthetic metadata paths. They do
not open media files, use an existing TMSU database, or mount FUSE. Fixture
creation is outside timed sections. The storage benchmark measures
`Storage.VFSNames`; the VFS benchmark measures tag/value directory listings,
file-link listings at several match and path depths, repeated-tag paths, and a
full recursive tree walk. `entries/op` reports how much each listing or walk
returned. These are warm-cache, single-process measurements; SQL counts, cold
cache behavior, CIFS, Samba, and Dolphin are outside the benchmark.

The VFS listing matrix varies file counts (128, 512, 1,024), co-occurring tags
(2–4), value counts (2, 4, 8), and implication depth (0–2). Every fixture also
has a tag matching half its files. Full walks use separate one-file fixtures
with the same tag and implication variants, keeping traversal growth measurable
without turning the scheduled benchmark into a multi-gigabyte run. The storage
fixture is fixed at 1,024 files, six tags, three values, and a two-hop
implication; it covers broad and nested tag queries, restricted values,
implications, and empty results.

Compare repeated runs on the same machine, Go/compiler, SQLite version, and
system load. Go reports CPU and OS/architecture in benchmark output. Save the
output for comparison, for example:

```sh
go test ./benchmark ./vfs -run '^$' -bench '^(BenchmarkTraversalNames|BenchmarkVFSListingsAndWalk)$' -benchmem -benchtime=500ms -count=3 > before.txt
# Repeat after a change into after.txt, then compare with benchstat.
```

Scheduled CI runs these benchmarks on Ubuntu with Go 1.23 and publishes the
raw output as an informational artifact. The local baseline recorded below is
from the listed host and toolchain; do not compare its timings directly with
CI or other machines.

## Local baseline

The local baseline records the fork revision, Go and SQLite versions, hardware,
fixture sizes, time/op, and allocations/op from the run below. It uses Go 1.26
on the local host; CI uses Go 1.23, so compare CI results only with other runs
from the same environment.

Baseline captured 2026-11-16 from base revision
`308594cda04d4988b65203777e0a8088fc95dae8` with the working-tree changes under
review: Linux/amd64, Intel Core i7-6700K @ 4.00 GHz, Go 1.26.10, GCC 15.3.0,
SQLite 3.35.4 (`go-sqlite3` v1.14.7). Values below are medians of three runs;
times are shown with allocation sizes and counts. The full walk has one
synthetic file and isolates path growth, while listing cases use the file
counts shown.

| Workload | Fixture | Entries/op | Time/op | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: |
| Storage broad tag query | 1,024 files, 6 tags, 3 values | — | 1.36 ms | 6,434 | 73 |
| Storage nested tag query | 1,024 files, 6 tags, 3 values | — | 2.44 ms | 12,228 | 83 |
| Storage restricted values | 1,024 files, 6 tags, 3 values | — | 1.05 ms | 6,251 | 61 |
| Directory tags and values | 128 files, 2 direct tags, 2 values | 5 | 1.25 ms | 12,438 | 197 |
| File links, all matches | 1,024 files, 4 direct tags, 8 values | 1,024 | 8.63 ms | 1,394,047 | 37,580 |
| File links, half matches | 1,024 files, 4 direct tags, 8 values | 512 | 4.66 ms | 703,730 | 18,869 |
| File links at selected depth | 1,024 files, 4 direct tags, 8 values | 1,024 | 11.31 ms | 1,407,779 | 37,727 |
| Full walk, sparse | 1 file, 2 direct tags, no implications | 23 | 10.82 ms | 216,596 | 3,503 |
| Full walk, broad | 1 file, 4 direct tags, 1 implication | 1,760 | 2.29 s | 36,893,152 | 444,485 |
| Full walk, implied | 1 file, 3 direct tags, 2 implications | 1,760 | 2.23 s | 37,031,936 | 450,060 |
