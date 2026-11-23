package control

import (
	"context"
	"database/sql"
)

// Trace makes every query a later Read or Write runs call fn with its text
// and arguments, so a test can show which records a read touched; a nil fn
// stops it. It adds no behaviour: the traced Tx runs the same queries.
func (s *Store) Trace(fn func(query string, args []any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trace = fn
}

// tracing is a querier that reports each query before running it.
type tracing struct {
	querier
	fn func(query string, args []any)
}

func (t tracing) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	t.fn(query, args)
	return t.querier.ExecContext(ctx, query, args...)
}

func (t tracing) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	t.fn(query, args)
	return t.querier.QueryContext(ctx, query, args...)
}

func (t tracing) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	t.fn(query, args)
	return t.querier.QueryRowContext(ctx, query, args...)
}
