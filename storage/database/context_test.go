package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestContextCancellationRollsBackAndReportsRowErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	tx, err := db.BeginContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO tag(name) VALUES ('temporary')"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query("SELECT name FROM tag")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := readTag(rows); !errors.Is(err, context.Canceled) {
		t.Fatalf("row error = %v", err)
	}
	rows.Close()
	if _, err := tx.Exec("INSERT INTO tag(name) VALUES ('late')"); err == nil {
		t.Fatal("exec succeeded after cancellation")
	}
	_ = tx.Rollback() // database/sql may already have rolled back on cancellation.
	check, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var count int
	if err := check.QueryRow("SELECT count(*) FROM tag WHERE name='temporary'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestCancellationAfterPartialRowIteration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tx, err := db.BeginContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO tag(name) VALUES ('first'), ('second')"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query("SELECT id, name FROM tag ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	first, err := readTag(rows)
	if err != nil || first == nil || first.Name != "first" {
		t.Fatalf("first row = %v, error = %v", first, err)
	}
	cancel()
	if tags, err := readTags(rows, nil); !errors.Is(err, context.Canceled) || tags != nil {
		t.Fatalf("remaining rows = %v, error = %v", tags, err)
	}
}

func TestContextDeadlineInterruptsSQL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	tx, err := db.BeginContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.Query("WITH RECURSIVE n(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM n) SELECT sum(x) FROM n")
	if err == nil {
		defer rows.Close()
		rows.Next()
		err = rows.Err()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}
