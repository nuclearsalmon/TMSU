package cli

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oniony/TMSU/storage"
)

type testTransaction struct {
	commits, rollbacks     int
	commitErr, rollbackErr error
}

func (tx *testTransaction) Commit() error   { tx.commits++; return tx.commitErr }
func (tx *testTransaction) Rollback() error { tx.rollbacks++; return tx.rollbackErr }

func TestFinishTransaction(t *testing.T) {
	commandErr := errors.New("command failed")
	commitErr := errors.New("commit failed")
	rollbackErr := errors.New("rollback failed")
	for _, tc := range []struct {
		name                       string
		initial, commit, rollback  error
		wantCommits, wantRollbacks int
		wantError                  error
		wantText                   string
	}{
		{"success", nil, nil, nil, 1, 0, nil, ""},
		{"commit error", nil, commitErr, nil, 1, 0, commitErr, "could not commit"},
		{"command error", commandErr, nil, nil, 0, 1, commandErr, ""},
		{"rollback error", commandErr, nil, rollbackErr, 0, 1, commandErr, "rollback failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &testTransaction{commitErr: tc.commit, rollbackErr: tc.rollback}
			err := tc.initial
			finishTransaction(tx, &err)
			if tx.commits != tc.wantCommits || tx.rollbacks != tc.wantRollbacks || !errors.Is(err, tc.wantError) || (tc.wantText != "" && (err == nil || !strings.Contains(err.Error(), tc.wantText))) {
				t.Fatalf("commits=%d rollbacks=%d error=%v", tx.commits, tx.rollbacks, err)
			}
		})
	}
}

func TestFinishTransactionPanic(t *testing.T) {
	tx := &testTransaction{}
	func() {
		defer func() {
			if recover() != "failure" {
				t.Error("panic was not propagated")
			}
		}()
		func() {
			var err error
			defer finishTransaction(tx, &err)
			panic("failure")
		}()
	}()
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestConfigTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("INSERT INTO setting (name, value) VALUES ('transaction_test', 'before')"); err != nil {
		t.Fatal(err)
	}

	readValue := func() string {
		t.Helper()
		var value string
		if err := db.QueryRow("SELECT value FROM setting WHERE name = 'transaction_test'").Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if err, _ := configExec(nil, []string{"transaction_test=after", "invalid=too=many"}, path); err == nil {
		t.Fatal("expected command error")
	}
	if got := readValue(); got != "before" {
		t.Fatalf("failed command persisted value %q", got)
	}
	if err, _ := configExec(nil, []string{"transaction_test=after"}, path); err != nil {
		t.Fatal(err)
	}
	if got := readValue(); got != "after" {
		t.Fatalf("successful command did not persist value: %q", got)
	}
}

func TestDeleteCommitsWithWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	if err := storage.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddTag(tx, "present"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	err, warnings := deleteExec(nil, []string{"present", "missing"}, path)
	if err != nil || len(warnings) != 1 {
		t.Fatalf("error=%v warnings=%v", err, warnings)
	}
	store, err = storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err = store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tag, err := store.TagByName(tx, "present")
	if err != nil || tag != nil {
		t.Fatalf("tag=%v error=%v", tag, err)
	}
}
