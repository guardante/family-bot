package storage

import (
	"context"
)

// Todo — одна задача. RemoteID/Provider заполняются, если задача
// синхронизирована с внешним сервисом (например, Todoist).
type Todo struct {
	ID        int64
	UserID    int64
	Text      string
	Done      bool
	RemoteID  *string
	Provider  *string
	CreatedAt string
	DoneAt    *string
}

func (s *Storage) AddTodo(ctx context.Context, userID int64, text string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO todos (user_id, text) VALUES (?, ?)`, userID, text)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Todos возвращает список: невыполненные по порядку добавления, затем выполненные.
func (s *Storage) Todos(ctx context.Context, userID int64, limit int) ([]Todo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, text, done, remote_id, provider, created_at, done_at
		FROM todos
		WHERE user_id = ?
		ORDER BY done ASC, id ASC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var todos []Todo
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.UserID, &t.Text, &t.Done, &t.RemoteID, &t.Provider, &t.CreatedAt, &t.DoneAt); err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	return todos, rows.Err()
}

func (s *Storage) GetTodo(ctx context.Context, userID, todoID int64) (*Todo, error) {
	t := &Todo{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, text, done, remote_id, provider, created_at, done_at
		FROM todos WHERE user_id = ? AND id = ?`, userID, todoID,
	).Scan(&t.ID, &t.UserID, &t.Text, &t.Done, &t.RemoteID, &t.Provider, &t.CreatedAt, &t.DoneAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Storage) SetTodoDone(ctx context.Context, userID, todoID int64, done bool) error {
	var doneAt any
	if done {
		doneAt = "datetime('now')" // см. SQL ниже: используем выражение
	}
	_ = doneAt
	q := `UPDATE todos SET done = ?, done_at = NULL WHERE user_id = ? AND id = ?`
	if done {
		q = `UPDATE todos SET done = ?, done_at = datetime('now') WHERE user_id = ? AND id = ?`
	}
	_, err := s.db.ExecContext(ctx, q, done, userID, todoID)
	return err
}

func (s *Storage) SetTodoRemote(ctx context.Context, userID, todoID int64, provider, remoteID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE todos SET provider = ?, remote_id = ? WHERE user_id = ? AND id = ?`,
		provider, remoteID, userID, todoID)
	return err
}

// DeleteTodo удаляет задачу и возвращает её (для отката в внешнем сервисе).
func (s *Storage) DeleteTodo(ctx context.Context, userID, todoID int64) (*Todo, error) {
	todo, err := s.GetTodo(ctx, userID, todoID)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM todos WHERE user_id = ? AND id = ?`, userID, todoID); err != nil {
		return nil, err
	}
	return todo, nil
}

// ClearDone удаляет выполненные задачи и возвращает их (для синхронизации).
func (s *Storage) ClearDone(ctx context.Context, userID int64) ([]Todo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, text, done, remote_id, provider, created_at, done_at
		FROM todos WHERE user_id = ? AND done = 1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var todos []Todo
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.UserID, &t.Text, &t.Done, &t.RemoteID, &t.Provider, &t.CreatedAt, &t.DoneAt); err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM todos WHERE user_id = ? AND done = 1`, userID); err != nil {
		return nil, err
	}
	return todos, nil
}
