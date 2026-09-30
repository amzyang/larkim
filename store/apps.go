package store

import (
	"context"
	"database/sql"
	"slices"
)

// enrollApps records the apps a reaction block names, so the lookup pass has
// them to ask about. It runs with every write of a block: the block is the
// only place an app reactor's id appears.
func enrollApps(ctx context.Context, tx *sql.Tx, reactionsJSON string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO apps (app_id)
 SELECT DISTINCT json_extract(d.value, '$.operator.operator_id')
 FROM json_each(CASE WHEN json_valid(?1) THEN ?1 ELSE '{}' END, '$.details') d
 WHERE json_extract(d.value, '$.operator.operator_type') = 'app'
   AND json_extract(d.value, '$.operator.operator_id') <> ''
 ON CONFLICT(app_id) DO NOTHING`, reactionsJSON)
	return err
}

// AppsToResolve lists apps whose name has never been looked up.
func (s *Store) AppsToResolve(ctx context.Context, limit int) ([]string, error) {
	return queryAll(ctx, s.db, scanOne[string], `SELECT app_id FROM apps WHERE checked_at = 0 ORDER BY app_id LIMIT ?`, limit)
}

// SetAppName records a lookup's answer. An empty name settles an app the
// tenant will not show.
func (s *Store) SetAppName(ctx context.Context, appID, name string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apps SET name = ?, checked_at = ? WHERE app_id = ?`, name, now, appID)
	return err
}

// AppNames maps those of ids that are apps to their names; an app not yet
// named, or never to be, maps to "".
func (s *Store) AppNames(ctx context.Context, ids []string) (map[string]string, error) {
	type app struct{ id, name string }
	out := map[string]string{}
	for chunk := range slices.Chunk(ids, 500) {
		rows, err := queryAll(ctx, s.db, func(sc scanner) (app, error) {
			var a app
			err := sc.Scan(&a.id, &a.name)
			return a, err
		}, `SELECT app_id, name FROM apps WHERE app_id IN `+inClause(len(chunk)), anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for _, a := range rows {
			out[a.id] = a.name
		}
	}
	return out, nil
}
