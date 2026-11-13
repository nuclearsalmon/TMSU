package benchmark

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/oniony/TMSU/query"
	"github.com/oniony/TMSU/storage"
)

// fixture creates metadata only: none of the synthetic file paths are accessed.
func fixture(tb testing.TB) *storage.Storage {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "tmsu.db")
	if err := storage.CreateAt(path); err != nil {
		tb.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		tb.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	exec := func(q string, args ...interface{}) {
		tb.Helper()
		if _, err := tx.Exec(q, args...); err != nil {
			tb.Fatal(err)
		}
	}
	exec("INSERT INTO tag(id,name) VALUES (1,'common'),(2,'even'),(3,'quarter'),(4,'genre'),(5,'implied'),(6,'chain')")
	exec("INSERT INTO value(id,name) VALUES (1,'rock'),(2,'jazz'),(3,'derived')")
	exec("INSERT INTO implication VALUES (2,0,5,3),(5,3,6,0)")
	for i := 1; i <= 1024; i++ {
		exec("INSERT INTO file(id,directory,name,fingerprint,mod_time,size,is_dir) VALUES (?, '/synthetic', ?, '', '2020-01-01', 0, 0)", i, fmt.Sprintf("file%04d", i))
		exec("INSERT INTO file_tag VALUES (?,1,0)", i)
		if i%2 == 0 {
			exec("INSERT INTO file_tag VALUES (?,2,0)", i)
		}
		if i%4 == 0 {
			exec("INSERT INTO file_tag VALUES (?,3,0)", i)
		}
		value := 1
		if i%2 == 0 {
			value = 2
		}
		exec("INSERT INTO file_tag VALUES (?,4,?)", i, value)
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
	return store
}

func names(tb testing.TB, store *storage.Storage, expression query.Expression, tag *string) []string {
	tb.Helper()
	tx, err := store.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	result, err := store.VFSNames(tx, expression, tag)
	if err != nil {
		tb.Fatal(err)
	}
	return result
}

func TestTraversalNames(t *testing.T) {
	store := fixture(t)
	genre, implied := "genre", "implied"
	cases := []struct {
		name       string
		expression query.Expression
		tag        *string
		want       []string
	}{
		{"common", query.HasAll([]string{"common"}), nil, []string{"chain", "common", "even", "genre", "implied", "quarter"}},
		{"nested", query.HasAll([]string{"common", "even", "quarter"}), nil, []string{"chain", "common", "even", "genre", "implied", "quarter"}},
		{"restricted-values", query.HasAll([]string{"even"}), &genre, []string{"jazz"}},
		{"implied-values", query.HasAll([]string{"even"}), &implied, []string{"derived"}},
		{"no-match", query.HasAll([]string{"missing"}), nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(t, store, tc.expression, tc.tag); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func BenchmarkTraversalNames(b *testing.B) {
	store := fixture(b)
	genre := "genre"
	cases := []struct {
		name       string
		expression query.Expression
		tag        *string
	}{
		{"broad-tags", query.HasAll([]string{"common"}), nil},
		{"nested-tags", query.HasAll([]string{"common", "even", "quarter"}), nil},
		{"restricted-values", query.HasAll([]string{"even"}), &genre},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			// Warm the SQLite and OS caches; transaction setup is part of each operation.
			names(b, store, tc.expression, tc.tag)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				names(b, store, tc.expression, tc.tag)
			}
		})
	}
}
