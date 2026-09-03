package store

import "context"

// LoadTelegramBotOffset returns the next Telegram getUpdates offset to
// request (one past the last update this process durably recorded as
// processed), so a restarted bot worker resumes instead of reprocessing —
// or, worse, permanently skipping — updates.
func (s *Store) LoadTelegramBotOffset(ctx context.Context) (int64, error) {
	var offset int64
	err := s.db.QueryRowContext(ctx, `SELECT last_update_id FROM telegram_bot_state WHERE id = 1`).Scan(&offset)
	return offset, err
}

// SaveTelegramBotOffset durably records the next offset to request.
func (s *Store) SaveTelegramBotOffset(ctx context.Context, offset int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_bot_state SET last_update_id = ? WHERE id = 1`, offset)
	return err
}
