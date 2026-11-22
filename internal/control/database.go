package control

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // Registers the in-process database/sql driver.
)

// database opens each connection with the same pragmas, including replacement
// connections created by database/sql. Only the writer can change the file.
func (s *Store) database(readonly bool) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: s.path}
	q := url.Values{"_pragma": {"journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(ON)", "busy_timeout(5000)"}}
	q.Set("_txlock", "immediate")
	size := 1
	if readonly {
		q.Set("mode", "ro")
		q.Del("_txlock")
		size = 4
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(size)
	db.SetMaxIdleConns(size)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (s *Store) openDatabases() error {
	// Create privately before SQLite creates its WAL and shared-memory files.
	if err := s.privateFile(); err != nil {
		return err
	}
	var err error
	s.writer, err = s.database(false)
	if err == nil {
		s.readers, err = s.database(true)
	}
	return err
}

func (s *Store) closeDatabases() error {
	var errs []error
	for _, db := range []*sql.DB{s.readers, s.writer} {
		if db != nil {
			errs = append(errs, db.Close())
		}
	}
	return errors.Join(errs...)
}

func (s *Store) transaction(fn func(*sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = fn(tx); err != nil {
		return err
	}
	return s.commit(tx)
}
