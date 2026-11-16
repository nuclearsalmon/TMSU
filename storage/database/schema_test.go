package database

import (
	"database/sql"
	"fmt"
	"os"
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

func TestOpenAtUpgradesSupportedSchemas(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version []int
	}{
		{"empty database", nil},
		{"current database", []int{0, 7, 0, 1}},
		{"previous revision", []int{0, 7, 0, 0}},
		{"older schema", []int{0, 5, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			if tc.version == nil {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := CreateAt(path); err != nil {
					t.Fatal(err)
				}
				if len(tc.version) == 4 && (tc.version[1] != 7 || tc.version[3] != 1) {
					if err := setSchemaVersionForTest(t, path, tc.version[0], tc.version[1], tc.version[2], tc.version[3]); err != nil {
						t.Fatal(err)
					}
				}
			}

			opened, err := OpenAt(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := opened.Close(); err != nil {
				t.Fatal(err)
			}
			if got := readSchemaVersion(t, path); got != "0.7.0-1" {
				t.Fatalf("schema version = %s, want 0.7.0-1", got)
			}
		})
	}
}

func TestOpenAtUpgradeFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := CreateAt(path); err != nil {
		t.Fatal(err)
	}
	if err := setSchemaVersionForTest(t, path, 0, 6, 0, 0); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE setting"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenAt(path); err == nil {
		opened.Close()
		t.Fatal("expected schema upgrade to fail")
	}
	if got := readSchemaVersion(t, path); got != "0.6.0-0" {
		t.Fatalf("failed upgrade changed schema version to %s", got)
	}
}

func TestOpenAtRejectsPartialSchemaWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE file (wrong TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenAt(path); err == nil {
		opened.Close()
		t.Fatal("expected partial schema to be rejected")
	}
	db, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tag'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial upgrade left tag table: count=%d error=%v", count, err)
	}
}

func TestOpenReadOnlyAtDoesNotMigrateOrWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := OpenReadOnlyAt(path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO tag (name) VALUES ('read-only')"); err == nil {
		t.Fatal("read-only database accepted a write")
	}
	tx.Rollback()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := setSchemaVersionForTest(t, path, 0, 6, 0, 0); err != nil {
		t.Fatal(err)
	}
	if db, err := OpenReadOnlyAt(path); err == nil {
		db.Close()
		t.Fatal("read-only open accepted an older schema")
	}
	if got := readSchemaVersion(t, path); got != "0.6.0-0" {
		t.Fatalf("read-only open changed schema version to %s", got)
	}
}

func readSchemaVersion(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var major, minor, patch, revision int
	if err := db.QueryRow("SELECT major, minor, patch, revision FROM version").Scan(&major, &minor, &patch, &revision); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d.%d.%d-%d", major, minor, patch, revision)
}

func setSchemaVersionForTest(t *testing.T, path string, major, minor, patch, revision int) error {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE version SET major=?, minor=?, patch=?, revision=?", major, minor, patch, revision)
	closeErr := db.Close()
	if err != nil {
		return err
	}
	return closeErr
}
