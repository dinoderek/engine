package control

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TelegramDelivery records one inbox update, including a pending send. Several
// informational updates can share the same digest message and rendered text.
type TelegramDelivery struct {
	Update  int    `json:"update_id"`
	Message int64  `json:"message_id"`
	Text    string `json:"text"`
	Status  string `json:"status"`
	Settled bool   `json:"settled"`
}

const telegramSchema = `CREATE TABLE IF NOT EXISTS telegram_deliveries (
 update_id INTEGER PRIMARY KEY, message_id INTEGER NOT NULL DEFAULT 0,
 text TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', settled INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS telegram_state (
 id INTEGER PRIMARY KEY CHECK(id=1), offset INTEGER NOT NULL DEFAULT 0,
 digest_at TEXT NOT NULL DEFAULT '');
 INSERT OR IGNORE INTO telegram_state(id) VALUES(1); `

// TelegramState reads delivery state independently of command records.
func (s *Store) TelegramState() ([]TelegramDelivery, int64, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lock == nil {
		return nil, 0, time.Time{}, errors.New("store is closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := s.readers.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query("SELECT update_id,message_id,text,status,settled FROM telegram_deliveries ORDER BY update_id")
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	var deliveries []TelegramDelivery
	for rows.Next() {
		var d TelegramDelivery
		if err = rows.Scan(&d.Update, &d.Message, &d.Text, &d.Status, &d.Settled); err != nil {
			_ = rows.Close()
			return nil, 0, time.Time{}, err
		}
		deliveries = append(deliveries, d)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	var offset int64
	var digest string
	if err = tx.QueryRow("SELECT offset,digest_at FROM telegram_state WHERE id=1").Scan(&offset, &digest); err != nil {
		return nil, 0, time.Time{}, err
	}
	var at time.Time
	if digest != "" {
		if at, err = time.Parse(time.RFC3339Nano, digest); err != nil {
			return nil, 0, time.Time{}, err
		}
	}
	return deliveries, offset, at, tx.Commit()
}

// SaveTelegram atomically records deliveries and, when nonzero, the poll offset
// and digest time. Offset never moves backwards when the two loops save.
func (s *Store) SaveTelegram(deliveries []TelegramDelivery, offset int64, digest time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lock == nil {
		return errors.New("store is closed")
	}
	return s.transaction(func(tx *sql.Tx) error {
		for _, d := range deliveries {
			_, err := tx.Exec("INSERT INTO telegram_deliveries(update_id,message_id,text,status,settled) VALUES(?,?,?,?,?) ON CONFLICT(update_id) DO UPDATE SET message_id=excluded.message_id,text=excluded.text,status=excluded.status,settled=excluded.settled", d.Update, d.Message, d.Text, d.Status, d.Settled)
			if err != nil {
				return err
			}
		}
		if digest.IsZero() {
			_, err := tx.Exec("UPDATE telegram_state SET offset=max(offset,?) WHERE id=1", offset)
			return err
		}
		_, err := tx.Exec("UPDATE telegram_state SET offset=max(offset,?),digest_at=? WHERE id=1", offset, digest.UTC().Format(time.RFC3339Nano))
		return err
	})
}
