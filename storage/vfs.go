// Copyright 2026 TMSU contributors. Licensed under GPL-3.0-or-later.
package storage

import (
	"github.com/oniony/TMSU/query"
	"github.com/oniony/TMSU/storage/database"
)

func (store *Storage) VFSNames(tx *Tx, expression query.Expression, tagName *string) ([]string, error) {
	return database.VFSNames(tx.tx, expression, tagName)
}
