//go:build !windows

package vfs

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/oniony/TMSU/entities"
	"github.com/oniony/TMSU/storage"
)

func TestPortableNameEscaping(t *testing.T) {
	names := []string{
		"ai:something", "slash/name", `back\\slash`, "literal\u200B\u2236",
		`*?"<>|`, "trailing. ", "CON", "con.txt", "COM1", "LPT³", "control\u0001",
	}
	for _, name := range names {
		escaped := escape(name)
		if got := unescape(escaped); got != name {
			t.Errorf("round trip %q: got %q", name, got)
		}
		if strings.ContainsAny(escaped, `/\\:*?"<>|`) || strings.HasSuffix(escaped, ".") || strings.HasSuffix(escaped, " ") {
			t.Errorf("escaped name is not portable: %q -> %q", name, escaped)
		}
	}
	tagNames := []string{"files", "Files", "README.md", "=value", "CON", "LPT³", "ai:something", "ai\u2236something"}
	seen := make(map[string]string)
	for _, name := range tagNames {
		escaped := escapeTag(name)
		if got := unescapeTag(escaped); got != name {
			t.Errorf("tag round trip %q: got %q", name, got)
		}
		if previous, ok := seen[escaped]; ok {
			t.Errorf("%q and %q collide as %q", previous, name, escaped)
		}
		seen[escaped] = name
	}
}

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
	exec("INSERT INTO implication VALUES (1,1,3,2), (1,3,3,2), (3,2,4,0), (4,0,1,0)")
	for i := 1; i <= 10000; i++ {
		exec("INSERT INTO file(id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (?, '/', ?, '', '2020-01-01', 0, 0)", i, fmt.Sprintf("file%d", i))
		exec("INSERT INTO file_tag VALUES (?,1,1)", i)
	}
	exec("INSERT INTO file(id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (10001, '/', 'unmatched', '', '2020-01-01', 0, 0)")
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
	for _, path := range []string{"queries/implied", "queries/implied=implicit-value", "tags/implied/files"} {
		members := list(path)
		if len(members) != 10000 || !members["file1.1"] || !members["file2.2"] || members["unmatched.10001"] {
			t.Errorf("cyclic implication %q: count=%d file1=%v file2=%v", path, len(members), members["file1.1"], members["file2.2"])
		}
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

func TestVFSUnlinkPathMembership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"INSERT INTO tag(id,name) VALUES (1,'a'), (2,'b'), (3,'implied'), (4,'absent')",
		"INSERT INTO value(id,name) VALUES (1,'red'), (2,'blue')",
		"INSERT INTO file(id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (1,'/','example.txt','','2020-01-01',0,0)",
		"INSERT INTO file_tag VALUES (1,1,0), (1,2,1)",
		"INSERT INTO implication VALUES (1,0,3,0)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v := FuseVfs{store: store}
	for _, tc := range []struct {
		path string
		want fuse.Status
	}{
		{"queries/a/example.1.txt", fuse.EPERM},
		{"tags/a/example.1.txt", fuse.EPERM},
		{"tags/absent/a/files/example.1.txt", fuse.ENOENT},
		{"tags/missing/a/files/example.1.txt", fuse.ENOENT},
		{"tags/b/=blue/a/files/example.1.txt", fuse.ENOENT},
		{"tags/a/files/wrong.1.txt", fuse.ENOENT},
		{"tags/a/files/example.1.other", fuse.ENOENT},
		{"tags/a/files/example.2.txt", fuse.ENOENT},
		{"tags/a/a/files/example.1.txt", fuse.ENOENT},
		{"tags/a/=red/=blue/files/example.1.txt", fuse.ENOENT},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := v.Unlink(tc.path, nil); got != tc.want {
				t.Fatalf("Unlink: got %v, want %v", got, tc.want)
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM file_tag").Scan(&count); err != nil || count != 2 {
				t.Fatalf("file tags changed: count=%d err=%v", count, err)
			}
		})
	}
	// Implied and unqualified valued tags cannot be removed at these paths.
	if got := v.Unlink("tags/b/files/example.1.txt", nil); got != fuse.EPERM {
		t.Fatalf("unlink unqualified valued tag: %v", got)
	}
	if got := v.Unlink("tags/implied/files/example.1.txt", nil); got != fuse.EPERM {
		t.Fatalf("unlink implied tag: %v", got)
	}
	if got := v.Unlink("tags/implied/b/=red/files/example.1.txt", nil); got != fuse.OK {
		t.Fatalf("unlink valued tag through implied parent: %v", got)
	}
	if got := v.Rename("tags/implied/files/example.1.txt", "tags/absent/files/example.1.txt", nil); got != fuse.EPERM {
		t.Fatalf("move implied association: %v", got)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM file_tag WHERE file_id=1 AND tag_id=2").Scan(&count); err != nil || count != 0 {
		t.Fatalf("valued tag remains: count=%d err=%v", count, err)
	}
	if got := v.Unlink("tags/a/files/example.1.txt", nil); got != fuse.OK {
		t.Fatalf("unlink explicit tag: %v", got)
	}
}

func TestVFSLinkRenameAndUnlinkRetainFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".tmsu", "db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.CreateAt(dbPath); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "example.txt")
	if err := os.WriteFile(filePath, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err := store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tagA, err := store.AddTag(tx, "a")
	if err != nil {
		t.Fatal(err)
	}
	tagB, err := store.AddTag(tx, "b")
	if err != nil {
		t.Fatal(err)
	}
	tagC, err := store.AddTag(tx, "c")
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.AddFile(tx, filePath, "", info.ModTime(), info.Size(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFileTag(tx, file.Id, tagA.Id, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	v := FuseVfs{store: store}
	linkName := v.getLinkName(file)
	assertFileTag := func(tagId entities.TagId, want bool) {
		t.Helper()
		tx, err := store.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := store.FileTagExists(tx, file.Id, tagId, 0, true)
		if err != nil || got != want {
			t.Fatalf("tag %v: got %v, want %v (error %v)", tagId, got, want, err)
		}
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TRIGGER fail_file_tag BEFORE INSERT ON file_tag BEGIN SELECT RAISE(ABORT, 'injected'); END"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if status := v.Link("tags/a/files/"+linkName, "tags/b/files/"+linkName, nil); status != fuse.EIO {
		t.Fatalf("failed Link: %v", status)
	}
	assertFileTag(tagA.Id, true)
	assertFileTag(tagB.Id, false)
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TRIGGER fail_file_tag"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if status := v.Link("tags/a/files/"+linkName, "tags/b/files/"+linkName, nil); status != fuse.OK {
		t.Fatalf("Link: %v", status)
	}
	assertFileTag(tagA.Id, true)
	assertFileTag(tagB.Id, true)
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TRIGGER fail_file_tag_delete BEFORE DELETE ON file_tag BEGIN SELECT RAISE(ABORT, 'injected'); END"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if status := v.Rename("tags/a/files/"+linkName, "tags/c/files/"+linkName, nil); status != fuse.EIO {
		t.Fatalf("failed Rename: %v", status)
	}
	assertFileTag(tagA.Id, true)
	assertFileTag(tagC.Id, false)
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TRIGGER fail_file_tag_delete"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if status := v.Rename("tags/a/files/"+linkName, "tags/c/files/"+linkName, nil); status != fuse.OK {
		t.Fatalf("Rename: %v", status)
	}
	assertFileTag(tagA.Id, false)
	assertFileTag(tagB.Id, true)
	assertFileTag(tagC.Id, true)
	if status := v.Unlink("tags/c/files/"+linkName, nil); status != fuse.OK {
		t.Fatalf("Unlink: %v", status)
	}
	assertFileTag(tagC.Id, false)
	assertFileTag(tagB.Id, true)
	if status := v.Unlink("tags/b/files/"+linkName, nil); status != fuse.OK {
		t.Fatalf("final Unlink: %v", status)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("physical file was removed: %v", err)
	}
	tx, err = store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	count, err := store.FileCount(tx)
	tx.Rollback()
	if err != nil || count != 0 {
		t.Fatalf("untagged file remains in the database: count=%d error=%v", count, err)
	}
}

func TestVFSMalformedPathsAndLinkMembership(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".tmsu", "db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.CreateAt(dbPath); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "example.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err := store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tag, err := store.AddTag(tx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddTag(tx, "other"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"files", "README.md", "=leading", "ai:something", "ai\u2236", "CON", "LPT³"} {
		if _, err := store.AddTag(tx, name); err != nil {
			t.Fatal(err)
		}
	}
	valueTag, err := store.AddTag(tx, "value")
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.AddValue(tx, "a:b")
	if err != nil {
		t.Fatal(err)
	}
	otherValue, err := store.AddValue(tx, "x:y")
	if err != nil {
		t.Fatal(err)
	}
	deviceValue, err := store.AddValue(tx, "CON")
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.AddFile(tx, filePath, "", info.ModTime(), info.Size(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFileTag(tx, file.Id, tag.Id, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFileTag(tx, file.Id, valueTag.Id, value.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFileTag(tx, file.Id, valueTag.Id, deviceValue.Id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	v := FuseVfs{store: store, mountPath: filepath.Join(dir, "mount")}
	linkName := v.getLinkName(file)
	entries, status := v.OpenDir(tagsDir, nil)
	if status != fuse.OK {
		t.Fatalf("OpenDir(tags): %v", status)
	}
	listedTags := make(map[string]bool)
	for _, entry := range entries {
		listedTags[entry.Name] = true
	}
	for _, name := range []string{"files", "README.md", "=leading", "ai:something", "ai\u2236", "CON", "LPT³"} {
		if !listedTags[escapeTag(name)] {
			t.Errorf("tag %q listed without its escaped name", name)
		}
		if _, status := v.OpenDir("tags/"+escapeTag(name), nil); status != fuse.OK {
			t.Errorf("OpenDir for tag %q: %v", name, status)
		}
	}
	if _, status := v.OpenDir("tags/CON", nil); status != fuse.ENOENT {
		t.Fatalf("unescaped device tag path: %v", status)
	}
	if status := v.Link("tags/a/files/"+linkName, "tags/value/="+escape("x:y")+"/files/"+linkName, nil); status != fuse.OK {
		t.Fatalf("Link valued association: %v", status)
	}
	tx, err = store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	retainedTag, retainedErr := store.FileTagExists(tx, file.Id, tag.Id, 0, true)
	addedValue, valueErr := store.FileTagExists(tx, file.Id, valueTag.Id, otherValue.Id, true)
	tx.Rollback()
	if retainedErr != nil || valueErr != nil || !retainedTag || !addedValue {
		t.Fatalf("Link changed source tags or missed destination value: tag=%v value=%v errors=(%v,%v)", retainedTag, addedValue, retainedErr, valueErr)
	}
	valueEntries, status := v.OpenDir("tags/value", nil)
	if status != fuse.OK {
		t.Fatalf("OpenDir(value): %v", status)
	}
	listedValue := false
	listedDeviceValue := false
	for _, entry := range valueEntries {
		if entry.Name == "="+escape("a:b") {
			listedValue = true
		}
		if entry.Name == "="+escape("CON") {
			listedDeviceValue = true
		}
	}
	if !listedValue {
		t.Fatal("colon value was not listed with its escaped name")
	}
	if !listedDeviceValue {
		t.Fatal("device-name value was not listed with its escaped name")
	}
	if _, status := v.OpenDir("tags/value/="+escape("CON")+"/files", nil); status != fuse.OK {
		t.Fatalf("escaped device value path: %v", status)
	}
	if _, status := v.OpenDir("tags/value/=CON/files", nil); status != fuse.ENOENT {
		t.Fatalf("unescaped device value path: %v", status)
	}
	oldValuedPath := "tags/value/=" + escape("a:b") + "/files/" + linkName
	newValuedPath := "tags/value/=" + escape("x:y") + "/files/" + linkName
	if status := v.Rename(oldValuedPath, newValuedPath, nil); status != fuse.OK {
		t.Fatalf("Rename valued association: %v", status)
	}
	tx, err = store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	oldExists, oldErr := store.FileTagExists(tx, file.Id, valueTag.Id, value.Id, true)
	newExists, newErr := store.FileTagExists(tx, file.Id, valueTag.Id, otherValue.Id, true)
	tx.Rollback()
	if oldErr != nil || newErr != nil || oldExists || !newExists {
		t.Fatalf("valued move old=%v new=%v errors=(%v,%v)", oldExists, newExists, oldErr, newErr)
	}
	for _, path := range []string{"tags/", "tags/files", "tags//a", "tags/=orphan", "queries/", "queries/a/extra"} {
		if _, status := v.OpenDir(path, nil); status == fuse.OK {
			t.Errorf("OpenDir(%q) unexpectedly succeeded", path)
		}
		if _, status := v.GetAttr(path, nil); status == fuse.OK {
			t.Errorf("GetAttr(%q) unexpectedly succeeded", path)
		}
	}
	if _, status := v.Readlink("tags/other/files/"+linkName, nil); status != fuse.ENOENT {
		t.Fatalf("Readlink through nonmatching tag: %v", status)
	}
	if _, status := v.GetAttr("tags/other/files/"+linkName, nil); status != fuse.ENOENT {
		t.Fatalf("GetAttr through nonmatching tag: %v", status)
	}
	if _, status := v.Readlink("tags/a/files/"+linkName, nil); status != fuse.OK {
		t.Fatalf("Readlink through matching tag: %v", status)
	}
}

func TestVFSTransactionPanicRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const panicValue = "injected panic"
	func() {
		defer func() {
			if got := recover(); got != panicValue {
				t.Fatalf("panic = %v, want %q", got, panicValue)
			}
		}()
		func() (status fuse.Status) {
			tx, err := store.Begin()
			if err != nil {
				t.Fatal(err)
			}
			status = fuse.EIO
			defer func() { status = finishTransaction(tx, status) }()
			if _, err := store.AddTag(tx, "uncommitted"); err != nil {
				t.Fatal(err)
			}
			panic(panicValue)
		}()
	}()

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM tag WHERE name='uncommitted'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("panic did not roll back: count=%d err=%v", count, err)
	}
}

func TestVFSOperationSQLTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER slow_insert BEFORE INSERT ON tag BEGIN
		SELECT (WITH RECURSIVE n(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM n)
		        SELECT sum(x) FROM n);
	END`)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v := FuseVfs{store: store, sqlTimeout: 100 * time.Millisecond}
	if status := v.Mkdir("tags/slow", 0, nil); status != fuse.EIO {
		t.Fatalf("timed-out Mkdir status = %v", status)
	}
	if _, err := db.Exec("DROP TRIGGER slow_insert"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM tag WHERE name='slow'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("timed-out insert count=%d err=%v", count, err)
	}
	if status := v.Mkdir("tags/after", 0, nil); status != fuse.OK {
		t.Fatalf("Mkdir after timeout status = %v", status)
	}
}

func TestVFSDeadlineRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v := FuseVfs{store: store, sqlTimeout: 50 * time.Millisecond}
	tx, cancel, err := v.beginSQL()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, err := store.AddTag(tx, "uncommitted"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := finishTransaction(tx, fuse.OK); got != fuse.EIO {
		t.Fatalf("expired transaction status = %v", got)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM tag WHERE name='uncommitted'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired transaction count=%d err=%v", count, err)
	}
}

func TestVFSTransactionsAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v := FuseVfs{store: store, mountPath: t.TempDir()}
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	check := func(got, want fuse.Status) {
		t.Helper()
		if got != want {
			t.Fatalf("status = %v, want %v", got, want)
		}
	}
	check(v.Mkdir("tags/first", 0, nil), fuse.OK)
	check(v.Rename("tags/first", "tags/second", nil), fuse.OK)
	check(v.Rename("tags/missing", "tags/other", nil), fuse.ENOENT)
	if _, status := v.Readlink("tags/second/files/missing.1", nil); status != fuse.ENOENT {
		t.Fatalf("missing link: %v", status)
	}
	exec("INSERT INTO tag(id,name) VALUES (20,'other')")
	exec("INSERT INTO file(id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (1, '/', 'example', '', '2020-01-01', 0, 0)")
	exec("INSERT INTO file_tag VALUES (1,20,0)")
	check(v.Unlink("tags/example.1", nil), fuse.EPERM)
	check(v.Unlink("queries/other/example.1", nil), fuse.EPERM)
	check(v.Unlink("tags/other/files/example.1", nil), fuse.OK)
	var remaining int
	if err := db.QueryRow("SELECT count(*) FROM file_tag").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("unlink did not commit: count=%d err=%v", remaining, err)
	}
	check(v.Unlink("tags/example.1", nil), fuse.EPERM)
	check(v.Unlink("queries/other/example.1", nil), fuse.EPERM)
	check(v.Unlink("tags/other/files/example.1", nil), fuse.ENOENT)
	if _, status := v.GetAttr("queries/other", nil); status != fuse.OK {
		t.Fatalf("query attribute: %v", status)
	}
	if err := db.QueryRow("SELECT count(*) FROM query WHERE text='other'").Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("query history not committed: count=%d err=%v", remaining, err)
	}
	exec("INSERT INTO implication VALUES (20,0,(SELECT id FROM tag WHERE name='second'),0)")
	exec("CREATE TRIGGER fail_tag_delete BEFORE DELETE ON tag BEGIN SELECT RAISE(ABORT, 'injected failure'); END")
	check(v.Rmdir("tags/second", nil), fuse.EIO)
	var count int
	if err := db.QueryRow("SELECT count(*) FROM implication").Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed delete did not roll back: count=%d err=%v", count, err)
	}
	exec("DROP TRIGGER fail_tag_delete")
	check(v.Rmdir("tags/second", nil), fuse.OK)
	check(v.Rmdir("tags/second", nil), fuse.ENOENT)
	// A completed transaction makes commit and rollback fail, exercising cleanup errors.
	tx, err := store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	check(finishTransaction(tx, fuse.OK), fuse.EIO)
	check(finishTransaction(tx, fuse.ENOENT), fuse.EIO)
	exec("DROP TABLE tag")
	if _, status := v.GetAttr("tags", nil); status != fuse.EIO {
		t.Fatalf("tag count error: %v", status)
	}
	check(v.Mkdir("tags/broken", 0, nil), fuse.EIO)
	check(v.Rename("tags/other", "tags/broken", nil), fuse.EIO)
	check(v.Rmdir("tags/other", nil), fuse.EIO)
}
