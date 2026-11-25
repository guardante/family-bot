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
	CaldavUID     *string // UID события в календаре Apple (если синхронизировано)
	CaldavPath    *string // путь календаря Apple, где лежит событие
}

// NormalizeEventAt приводит время к формату хранения: UTC, секунды.
func NormalizeEventAt(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// ParseEventAt читает время события из формата хранения.
func ParseEventAt(s string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05Z", s)
}

const eventCols = `id, user_id, title, event_at, remind_minutes, notified, created_at, caldav_uid, caldav_path`

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

func (s *Storage) DeleteEvent(ctx context.Context, userID, eventID int64) (*Event, error) {
	ev, err := s.getEvent(ctx, userID, eventID)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE user_id = ? AND id = ?`, userID, eventID); err != nil {
		return nil, err
	}
	return ev, nil
}

// UpdateEventAt переносит событие на новое время и перезапускает напоминание.
func (s *Storage) UpdateEventAt(ctx context.Context, userID, eventID int64, atUTC string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE events SET event_at = ?, notified = 0 WHERE user_id = ? AND id = ?`,
		atUTC, userID, eventID)
	return err
}

// SetEventCaldav привязывает событие к событию в календаре Apple (UID + путь).
func (s *Storage) SetEventCaldav(ctx context.Context, userID, eventID int64, uid, path string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE events SET caldav_uid = ?, caldav_path = ? WHERE user_id = ? AND id = ?`,
		uid, path, userID, eventID)
	return err
}

func (s *Storage) getEvent(ctx context.Context, userID, eventID int64) (*Event, error) {
	e := &Event{}
	err := s.db.QueryRowContext(ctx, `
		SELECT `+eventCols+` FROM events WHERE user_id = ? AND id = ?`, userID, eventID,
	).Scan(&e.ID, &e.UserID, &e.Title, &e.EventAt, &e.RemindMinutes, &e.Notified, &e.CreatedAt, &e.CaldavUID, &e.CaldavPath)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func scanEvents(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Event, error) {
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.EventAt, &e.RemindMinutes, &e.Notified, &e.CreatedAt, &e.CaldavUID, &e.CaldavPath); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
