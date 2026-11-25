package storage

import (
	"context"
	"time"
)

// Event — событие календаря. EventAt — UTC RFC3339 с точностью до секунды,
// поэтому лексикографическое сравнение строк совпадает с хронологическим.
type Event struct {
	ID            int64
	UserID        int64
	Title         string
	EventAt       string
	RemindMinutes *int
	Notified      bool
	CreatedAt     string
}

// NormalizeEventAt приводит время к формату хранения: UTC, секунды.
func NormalizeEventAt(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// ParseEventAt читает время события из формата хранения.
func ParseEventAt(s string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05Z", s)
}

const eventCols = `id, user_id, title, event_at, remind_minutes, notified, created_at`

func (s *Storage) AddEvent(ctx context.Context, userID int64, title, eventAtUTC string, remindMinutes *int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO events (user_id, title, event_at, remind_minutes)
		VALUES (?, ?, ?, ?)`, userID, title, eventAtUTC, remindMinutes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpcomingEvents — события пользователя с указанного момента (UTC строка).
func (s *Storage) UpcomingEvents(ctx context.Context, userID int64, fromUTC string, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+eventCols+` FROM events
		WHERE user_id = ? AND event_at >= ?
		ORDER BY event_at ASC LIMIT ?`, userID, fromUTC, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// DueReminderCandidates — ненапомянутые события в ближайшие сутки,
// финальный отбор по remind_minutes делает вызывающий код.
func (s *Storage) DueReminderCandidates(ctx context.Context, nowUTC, untilUTC string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+eventCols+` FROM events
		WHERE notified = 0 AND remind_minutes IS NOT NULL
		  AND event_at > ? AND event_at <= ?
		ORDER BY event_at ASC LIMIT 50`, nowUTC, untilUTC)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *Storage) MarkEventNotified(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET notified = 1 WHERE id = ?`, id)
	return err
}

func (s *Storage) DeleteEvent(ctx context.Context, userID, eventID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE user_id = ? AND id = ?`, userID, eventID)
	return err
}

func scanEvents(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Event, error) {
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.EventAt, &e.RemindMinutes, &e.Notified, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
