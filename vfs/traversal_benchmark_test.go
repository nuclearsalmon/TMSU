package vfs

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/oniony/TMSU/storage"
)

func traversalFixture(tb testing.TB, fileCount, directTags, valueCount, implicationDepth int) *FuseVfs {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "tmsu.db")
	if err := storage.CreateAt(path); err != nil {
		tb.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	tx, err := db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = tx.Rollback() })
	for id := 1; id <= directTags+1+implicationDepth; id++ {
		name := "common"
		switch {
		case id == 2:
			name = "ai:something"
		case id >= 3 && id <= directTags+1:
			name = fmt.Sprintf("tag%03d", id-3)
		case id > directTags+1:
			name = fmt.Sprintf("implied%03d", id-directTags-2)
		}
		if _, err := tx.Exec("INSERT INTO tag(id,name) VALUES (?,?)", id, name); err != nil {
			tb.Fatal(err)
		}
	}
	for id := 1; id <= valueCount; id++ {
		if _, err := tx.Exec("INSERT INTO value(id,name) VALUES (?,?)", id, fmt.Sprintf("value:%03d", id-1)); err != nil {
			tb.Fatal(err)
		}
	}
	for id := 0; id < implicationDepth; id++ {
		source := 1
		if id > 0 {
			source = directTags + 1 + id
		}
		implied := directTags + 2 + id
		if _, err := tx.Exec("INSERT INTO implication VALUES (?,0,?,0)", source, implied); err != nil {
			tb.Fatal(err)
		}
	}
	for id := 1; id <= fileCount; id++ {
		if _, err := tx.Exec("INSERT INTO file(id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (?, '/synthetic', ?, '', '2020-01-01', 0, 0)", id, fmt.Sprintf("file%05d.bin", id)); err != nil {
			tb.Fatal(err)
		}
		valueId := (id-1)%valueCount + 1
		if _, err := tx.Exec("INSERT INTO file_tag VALUES (?,1,?)", id, valueId); err != nil {
			tb.Fatal(err)
		}
		if id%2 == 0 {
			if _, err := tx.Exec("INSERT INTO file_tag VALUES (?,2,0)", id); err != nil {
				tb.Fatal(err)
			}
		}
		for tagId := 3; tagId <= directTags+1; tagId++ {
			if _, err := tx.Exec("INSERT INTO file_tag VALUES (?,?,0)", id, tagId); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	if err := db.Close(); err != nil {
		tb.Fatal(err)
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := store.Close(); err != nil {
			tb.Error(err)
		}
	})
	return &FuseVfs{store: store, mountPath: filepath.Join(tb.TempDir(), "mount")}
}

func walkTagTree(tb testing.TB, vfs *FuseVfs, path string) int {
	tb.Helper()
	entries, status := vfs.OpenDir(path, nil)
	if status != fuse.OK {
		tb.Fatalf("OpenDir(%q): %v", path, status)
	}
	count := len(entries)
	for _, entry := range entries {
		if entry.Mode&fuse.S_IFDIR != 0 {
			count += walkTagTree(tb, vfs, path+"/"+entry.Name)
		}
	}
	return count
}

func TestVFSDirectoryTreeSelections(t *testing.T) {
	vfs := traversalFixture(t, 4, 2, 2, 1)
	for _, tc := range []struct {
		path string
		want int
	}{
		{"tags/" + escapeTag("ai:something") + "/files", 2},
		{"tags/common/=" + escape("value:000") + "/files", 2},
		{"tags/common/implied000/files", 4},
		{"tags/common/common/files", 4},
		{"tags/common/tag000/files", 4},
	} {
		entries, status := vfs.OpenDir(tc.path, nil)
		if status != fuse.OK {
			t.Fatalf("OpenDir(%q): %v", tc.path, status)
		}
		if len(entries) != tc.want {
			t.Errorf("OpenDir(%q) returned %d links, want %d", tc.path, len(entries), tc.want)
		}
	}
	if count := walkTagTree(t, vfs, tagsDir); count == 0 {
		t.Fatal("full tag walk returned no entries")
	}
}

func BenchmarkVFSListingsAndWalk(b *testing.B) {
	fixtures := []struct {
		name                string
		files, tags, values int
		implications        int
	}{
		{"sparse", 128, 2, 2, 0},
		{"broad", 1024, 4, 8, 1},
		{"implied", 512, 3, 4, 2},
	}
	for _, tc := range fixtures {
		b.Run(tc.name, func(b *testing.B) {
			vfs := traversalFixture(b, tc.files, tc.tags, tc.values, tc.implications)
			walkVfs := traversalFixture(b, 1, tc.tags, 2, tc.implications)
			deepPath := "tags/common"
			for id := 0; id < tc.tags-1; id++ {
				deepPath += fmt.Sprintf("/tag%03d", id)
			}
			cases := []struct {
				name string
				path string
			}{
				{"directory-tags-and-values", "tags/common"},
				{"file-links-all", "tags/common/files"},
				{"file-links-half", "tags/" + escapeTag("ai:something") + "/files"},
				{"file-links-selected-depth", deepPath + "/files"},
				{"file-links-repeated-tag", "tags/common/common/files"},
			}
			for _, path := range cases {
				b.Run(path.name, func(b *testing.B) {
					var entries int
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						listed, status := vfs.OpenDir(path.path, nil)
						if status != fuse.OK {
							b.Fatalf("OpenDir(%q): %v", path.path, status)
						}
						entries = len(listed)
					}
					b.ReportMetric(float64(entries), "entries/op")
				})
			}
			b.Run("full-walk", func(b *testing.B) {
				var entries int
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					entries = walkTagTree(b, walkVfs, tagsDir)
				}
				b.ReportMetric(float64(entries), "entries/op")
			})
		})
	}
}
