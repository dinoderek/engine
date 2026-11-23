package controlrun

import (
	"context"

	"github.com/dinoderek/engine/internal/control"
)

// read runs fn on a consistent view of the records, committing nothing.
func read(s *control.Store, fn func(*control.Tx) error) error {
	return s.Read(context.Background(), fn)
}

// write runs fn in the store's one writing transaction.
func write(s *control.Store, fn func(*control.Tx) error) error {
	return s.Write(context.Background(), fn)
}
