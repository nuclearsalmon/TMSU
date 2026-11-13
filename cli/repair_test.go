package cli

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oniony/TMSU/common/fingerprint"
	"github.com/oniony/TMSU/entities"
	"github.com/oniony/TMSU/storage"
	"github.com/oniony/TMSU/storage/database"
)

func TestRepairPretendPreservesDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db")
	if err := database.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO file (id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (1,?,?, '', '2020-01-01', 0, 0)", dir, "missing")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{{LongName: "--pretend"}, {LongName: "--remove"}, {LongName: "--rationalize"}}
	if err, _ := repairExec(opts, nil, path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("pretend modified source database")
	}
}

func TestRepairPretendDoesNotUpgradeSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := database.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE version SET revision=0"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err, _ := repairExec(Options{{LongName: "--pretend"}}, nil, path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("pretend upgraded source")
	}
}

func TestBuildPathBySizeMapRejectsIncompleteSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	if err := buildPathBySizeMapRecursive(path, make(map[int64][]string)); err == nil {
		t.Fatal("expected an error for an inaccessible search path")
	}
}

func TestRepairMovedPretendReservesCandidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db")
	if err := database.CreateAt(path); err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err := store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	settings, err := store.Settings(tx)
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(dir, "candidate")
	if err := os.WriteFile(candidate, []byte("same"), 0600); err != nil {
		t.Fatal(err)
	}
	fp, err := fingerprint.Create(candidate, settings.FileFingerprintAlgorithm(), settings.DirectoryFingerprintAlgorithm(), settings.SymlinkFingerprintAlgorithm())
	if err != nil {
		t.Fatal(err)
	}
	missing := entities.Files{
		&entities.File{Directory: dir, Name: "old1", Size: 4, Fingerprint: fp},
		&entities.File{Directory: dir, Name: "old2", Size: 4, Fingerprint: fp},
	}
	var output bytes.Buffer
	if err := repairMoved(store, tx, missing, []string{candidate, candidate}, true, settings, &output); err != nil {
		t.Fatal(err)
	}
	if missing[0] != nil || missing[1] == nil || strings.Count(output.String(), "updated path to") != 1 {
		t.Fatalf("missing=%v output=%q", missing, output.String())
	}
	if err := repairMissing(store, tx, missing, true, false, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "old2: missing") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestFinishRepairTransactionOutput(t *testing.T) {
	failure := errors.New("failure")
	for _, tc := range []struct {
		name                               string
		pretend                            bool
		commandErr, commitErr, rollbackErr error
		wantOutput                         bool
		wantErr                            error
	}{
		{"commit", false, nil, nil, nil, true, nil},
		{"pretend", true, nil, nil, nil, true, nil},
		{"command failure", false, failure, nil, nil, false, failure},
		{"commit failure", false, nil, failure, nil, false, failure},
		{"rollback failure", true, nil, nil, failure, false, failure},
		{"command and rollback failure", false, failure, nil, errors.New("rollback failure"), false, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &testTransaction{commitErr: tc.commitErr, rollbackErr: tc.rollbackErr}
			output := bytes.NewBufferString("updated\n")
			var destination bytes.Buffer
			err := finishRepairTransaction(tx, tc.commandErr, tc.pretend, output, &destination)
			if !errors.Is(err, tc.wantErr) || (destination.Len() > 0) != tc.wantOutput {
				t.Fatalf("error=%v output=%q", err, destination.String())
			}
		})
	}
}

func TestDetermineStatusesUnexpectedStatError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loop")
	if err := os.Symlink(path, path); err != nil {
		t.Skip(err)
	}
	_, _, _, err := determineStatuses(entities.Files{&entities.File{Directory: dir, Name: "loop"}})
	if err == nil {
		t.Fatal("expected stat error")
	}
}
