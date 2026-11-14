package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaVersionSafety(t *testing.T) {
	cases := []struct{ name, schema, want string }{
		{"future major", "CREATE TABLE version (major, minor, patch, revision); INSERT INTO version VALUES (1,0,0,0)", "newer"},
		{"future revision", "CREATE TABLE version (major, minor, patch, revision); INSERT INTO version VALUES (0,7,0,2)", "newer"},
		{"empty", "CREATE TABLE version (major, minor, patch, revision)", "malformed"},
		{"bad value", "CREATE TABLE version (major, minor, patch, revision); INSERT INTO version VALUES ('bad',7,0,1)", "malformed"},
		{"multiple", "CREATE TABLE version (major, minor, patch, revision); INSERT INTO version VALUES (0,7,0,1),(0,7,0,2)", "malformed"},
		{"bad columns", "CREATE TABLE version (x, minor, patch); INSERT INTO version VALUES (0,7,0)", "malformed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			db, err := sql.Open("sqlite3", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(tc.schema); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if opened, err := OpenAt(path); err == nil {
				opened.Close()
				t.Fatal("expected error")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
			db, err = sql.Open("sqlite3", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var count int
			if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='file'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("schema changed: count=%d err=%v", count, err)
			}
		})
	}
}

func TestLegacyVersionRead(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE version (major, minor, patch); INSERT INTO version VALUES (0,6,0)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	v, err := currentSchemaVersion(tx)
	if err != nil || v.String() != "0.6.0-0" {
		t.Fatalf("version %v, error %v", v, err)
	}
}
