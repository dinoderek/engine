package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TelegramDelivery records one inbox update, including a pending send. Several
// informational updates can share the same digest message and rendered text.
type TelegramDelivery struct {
	Update  int    `json:"update_id"`
	Message int64  `json:"message_id"`
	Text    string `json:"text"`
	Settled bool   `json:"settled"`
}

const telegramSchema = `CREATE TABLE IF NOT EXISTS telegram_deliveries (
 update_id INTEGER PRIMARY KEY, message_id INTEGER NOT NULL DEFAULT 0,
 text TEXT NOT NULL DEFAULT '', settled INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS telegram_state (
 id INTEGER PRIMARY KEY CHECK(id=1), offset INTEGER NOT NULL DEFAULT 0,
 digest_at TEXT NOT NULL DEFAULT '');
 INSERT OR IGNORE INTO telegram_state(id) VALUES(1); `

// TelegramState reads delivery state independently of command records.
func (s *Store) TelegramState() ([]TelegramDelivery, int64, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil, 0, time.Time{}, errors.New("store is closed")
	}
	out, err := s.sql("SELECT update_id,message_id,text,settled FROM telegram_deliveries ORDER BY update_id;")
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	var deliveries []TelegramDelivery
	// SQLite encodes booleans as integers.
	var rows []struct {
		Update  int   `json:"update_id"`
		Message int64 `json:"message_id"`
		Text    string
		Settled int
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		if err = json.Unmarshal(out, &rows); err != nil {
			return nil, 0, time.Time{}, err
		}
	}
	for _, r := range rows {
		deliveries = append(deliveries, TelegramDelivery{r.Update, r.Message, r.Text, r.Settled != 0})
	}
	out, err = s.sql("SELECT offset,digest_at FROM telegram_state WHERE id=1;")
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	var meta []struct {
		Offset int64
		Digest string `json:"digest_at"`
	}
	if err = json.Unmarshal(out, &meta); err != nil {
		return nil, 0, time.Time{}, err
	}
	if len(meta) != 1 {
		return nil, 0, time.Time{}, errors.New("missing Telegram state")
	}
	var at time.Time
	if meta[0].Digest != "" {
		if at, err = time.Parse(time.RFC3339Nano, meta[0].Digest); err != nil {
			return nil, 0, time.Time{}, err
		}
	}
	return deliveries, meta[0].Offset, at, nil
}

// SaveTelegram atomically records deliveries and, when nonzero, the poll offset
// and digest time. Offset never moves backwards when the two loops save.
func (s *Store) SaveTelegram(deliveries []TelegramDelivery, offset int64, digest time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("store is closed")
	}
	var sql strings.Builder
	sql.WriteString("BEGIN IMMEDIATE;\n")
	for _, d := range deliveries {
		fmt.Fprintf(&sql, "INSERT INTO telegram_deliveries(update_id,message_id,text,settled) VALUES(%d,%d,%s,%t) ON CONFLICT(update_id) DO UPDATE SET message_id=excluded.message_id,text=excluded.text,settled=excluded.settled;\n", d.Update, d.Message, literal(d.Text), d.Settled)
	}
	fmt.Fprintf(&sql, "UPDATE telegram_state SET offset=max(offset,%d)", offset)
	if !digest.IsZero() {
		fmt.Fprintf(&sql, ",digest_at=%s", literal(digest.UTC().Format(time.RFC3339Nano)))
	}
	sql.WriteString(" WHERE id=1; COMMIT;")
	_, err := s.sql(sql.String())
	return err
}
