package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID             string
	CreatedAt      time.Time
	TelegramChatID string // "" if not linked yet
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

// FindOrCreateTelegramUser resolves the server user for a Telegram
// identity, creating one the first time this telegram_user_id is seen.
//
// telegram_user_id — Telegram's own stable numeric account id, taken from
// the server-verified Update, never from anything a pairing request
// supplies — is the only thing ever used as identity. A username is never
// read or stored here: it's mutable and Telegram itself doesn't treat it as
// a stable identifier, so this project doesn't either (see
// docs/PROTOCOL_V1.md).
func (s *Store) FindOrCreateTelegramUser(ctx context.Context, telegramUserID, chatID int64) (userID string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE telegram_user_id = ?`, telegramUserID).Scan(&userID)
	if err == nil {
		// Keep the delivery destination current. Stable in practice for a
		// private chat, but free to refresh and correct if that ever changes.
		if _, uerr := s.db.ExecContext(ctx, `UPDATE users SET telegram_chat_id = ? WHERE id = ?`, fmt.Sprint(chatID), userID); uerr != nil {
			return "", uerr
		}
		return userID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	userID = "usr_" + uuid.NewString()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO users (id, created_at, telegram_user_id, telegram_chat_id) VALUES (?, ?, ?, ?)`,
		userID, time.Now().UTC().Unix(), telegramUserID, fmt.Sprint(chatID),
	)
	if err != nil {
		return "", err
	}
	return userID, nil
}

// UserByTelegramID is a read-only lookup, unlike FindOrCreateTelegramUser:
// it never creates a user, so callers (tests in particular) can assert
// "no user was created for this identity" without the act of checking
// creating one.
func (s *Store) UserByTelegramID(ctx context.Context, telegramUserID int64) (userID string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE telegram_user_id = ?`, telegramUserID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return userID, true, nil
}
