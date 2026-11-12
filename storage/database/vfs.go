// Copyright 2026 TMSU contributors. Licensed under GPL-3.0-or-later.
package database

import "github.com/oniony/TMSU/query"

// VFSNames returns distinct tags, or values of tagName, on files matching
// expression. Expand implications on distinct tag/value pairs, not per file.
func VFSNames(tx *Tx, expression query.Expression, tagName *string) ([]string, error) {
	b := NewBuilder()
	b.AppendSql(`WITH RECURSIVE pairs(tag_id, value_id) AS (
 SELECT DISTINCT ft.tag_id, ft.value_id FROM file_tag ft
 WHERE ft.file_id IN (SELECT id FROM file WHERE `)
	buildQueryBranch(expression, b, false, false)
	b.AppendSql(`)
 UNION
 SELECT i.implied_tag_id, i.implied_value_id FROM implication i
 JOIN pairs p ON i.tag_id = p.tag_id AND (i.value_id = 0 OR i.value_id = p.value_id)
 )`)
	if tagName == nil {
		b.AppendSql(`SELECT DISTINCT t.name FROM pairs p JOIN tag t ON t.id = p.tag_id ORDER BY t.name`)
	} else {
		b.AppendSql(`SELECT DISTINCT v.name FROM pairs p JOIN tag t ON t.id = p.tag_id JOIN value v ON v.id = p.value_id WHERE t.name = `)
		b.AppendParam(*tagName)
		b.AppendSql(` ORDER BY v.name`)
	}
	rows, err := tx.Query(b.Sql(), b.Params()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
