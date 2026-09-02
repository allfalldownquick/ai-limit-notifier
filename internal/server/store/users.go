package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID             string
	CreatedAt      time.Time
	TelegramChatID string // "" if not linked yet (P4 pairing not implemented)
}

// CreateUser inserts a new user and returns its generated ID. Real
// onboarding is P4 (Telegram /start + pairing); today this exists so
// devices have somewhere to attach, and so tests/bootstrap can create one
// directly.
func (s *Store) CreateUser(ctx context.Context) (string, error) {
	id := "usr_" + uuid.NewString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, created_at) VALUES (?, ?)`,
		id, time.Now().UTC().Unix(),
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// SetTelegramChatID associates a user with a Telegram chat/destination.
// P4's pairing flow is the intended real caller; until then this is only
// used by test/bootstrap setup so delivery can be exercised end to end.
func (s *Store) SetTelegramChatID(ctx context.Context, userID, chatID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET telegram_chat_id = ? WHERE id = ?`,
		chatID, userID,
	)
	return err
}

// TelegramChatID returns the linked destination, or "" if the user has none
// yet.
func (s *Store) TelegramChatID(ctx context.Context, userID string) (string, error) {
	var chatID *string
	err := s.db.QueryRowContext(ctx, `SELECT telegram_chat_id FROM users WHERE id = ?`, userID).Scan(&chatID)
	if err != nil {
		return "", err
	}
	if chatID == nil {
		return "", nil
	}
	return *chatID, nil
}
