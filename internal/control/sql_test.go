package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// sql keeps migration fixtures and raw-storage assertions independent of the
// store's state. Production reads scan typed rows directly.
func (s *Store) sql(statement string, args ...any) ([]byte, error) {
	return queryJSON(s.writer, statement, args...)
}

func queryJSON(db *sql.DB, statement string, args ...any) ([]byte, error) {
	var out []byte
	for _, part := range strings.Split(statement, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		rows, err := db.Query(part, args...)
		if err != nil {
			return nil, err
		}
		cols, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		var data []map[string]any
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				_ = rows.Close()
				return nil, err
			}
			row := map[string]any{}
			for i, col := range cols {
				row[col] = values[i]
			}
			data = append(data, row)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func fixtureSQL(ctx context.Context, path, statement string) ([]byte, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if err = db.PingContext(ctx); err != nil {
		return nil, err
	}
	return queryJSON(db, statement)
}
