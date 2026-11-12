//go:build !windows

package vfs

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/oniony/TMSU/storage"
)

func TestVFSBrowsingAndQueryErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tag(id,name) VALUES (1,?), (2,?), (3,?), (4,?)", `genre\kind`, "other", "implied", "chain")
	exec("INSERT INTO value(id,name) VALUES (1,?), (2,?), (3,?)", `rock\pop`, "implicit-value", "jazz")
	exec("INSERT INTO implication VALUES (1,0,3,2), (3,2,4,0)")
	for i := 1; i <= 10000; i++ {
		exec("INSERT INTO file(id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (?, '/', ?, '', '2020-01-01', 0, 0)", i, fmt.Sprintf("file%d", i))
		exec("INSERT INTO file_tag VALUES (?,1,1)", i)
	}
	exec("INSERT INTO file_tag VALUES (1,2,0)")
	exec("INSERT INTO file_tag VALUES (2,1,3)")
	exec("INSERT INTO tag(id,name) VALUES (10005,?)", "slash/name")
	exec("INSERT INTO file_tag VALUES (1,10005,0)")
	for i := 5; i <= 10004; i++ {
		exec("INSERT INTO tag(id,name) VALUES (?,?)", i, fmt.Sprintf("tag%04d", i))
		exec("INSERT INTO file_tag VALUES (1,?,0)", i)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v := FuseVfs{store: store}
	list := func(path string) map[string]bool {
		t.Helper()
		entries, status := v.OpenDir(path, nil)
		if status != fuse.OK {
			t.Fatalf("OpenDir(%q): %v", path, status)
		}
		names := make(map[string]bool)
		for _, entry := range entries {
			names[entry.Name] = true
		}
		return names
	}
	tag := escape(`genre\kind`)
	value := "=" + escape(`rock\pop`)
	names := list("tags/" + tag)
	for _, name := range []string{value, "=jazz", "other", "implied", "chain", "tag10004", escape("slash/name"), "files"} {
		if !names[name] {
			t.Errorf("missing %q", name)
		}
	}
	if names[tag] {
		t.Error("selected escaped valued tag recurs")
	}
	if got := len(names); got != 10007 {
		t.Errorf("got %d entries, want 10007", got)
	}
	names = list("tags/" + tag + "/" + value)
	if names[tag] || !names["other"] || !names["implied"] {
		t.Errorf("bad valued traversal: selected=%v other=%v implied=%v", names[tag], names["other"], names["implied"])
	}
	names = list("tags/other/" + tag)
	if !names[value] || names["=jazz"] {
		t.Error("values are not restricted to parent-matching files")
	}
	names = list("tags/" + escape("slash/name"))
	if names[escape("slash/name")] {
		t.Error("selected slash-escaped tag recurs")
	}
	names = list("tags/implied")
	if !names["=implicit-value"] || !names["chain"] {
		t.Error("implied value or transitive tag missing")
	}
	if _, status := v.OpenDir("queries/other", nil); status != fuse.OK {
		t.Fatalf("valid query: %v", status)
	}
	if _, status := v.OpenDir("queries/other and", nil); status != fuse.ENOENT {
		t.Fatalf("invalid query: %v", status)
	}
	if _, err := db.Exec("DROP TABLE file_tag"); err != nil {
		t.Fatal(err)
	}
	if _, status := v.OpenDir("queries/other", nil); status != fuse.EIO {
		t.Fatalf("failed query: %v", status)
	}
	if _, status := v.OpenDir("tags/other", nil); status != fuse.EIO {
		t.Fatalf("failed tag lookup: %v", status)
	}
	if _, status := v.OpenDir("tags", nil); status != fuse.OK {
		t.Fatalf("VFS did not survive query error: %v", status)
	}
}
