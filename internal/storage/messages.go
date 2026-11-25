package storage

import (
	"context"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (s *Storage) AddMessage(ctx context.Context, userID int64, role, content string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (user_id, role, content)
		VALUES (?, ?, ?)`, userID, role, content)
	return err
}

// RecentMessages возвращает последние limit сообщений в хронологическом порядке.
func (s *Storage) RecentMessages(ctx context.Context, userID int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT role, content FROM messages
		WHERE user_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Переворачиваем: старые -> новые.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// TrimMessages оставляет пользователю не более keep последних сообщений.
func (s *Storage) TrimMessages(ctx context.Context, userID int64, keep int) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE user_id = ? AND id NOT IN (
			SELECT id FROM messages
			WHERE user_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		)`, userID, userID, keep)
	return err
}

func (s *Storage) ClearMessages(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE user_id = ?`, userID)
	return err
}
