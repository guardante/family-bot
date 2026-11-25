package storage

import (
	"context"
	"database/sql"
	"errors"
)

// Состояния онбординга пользователя.
const (
	StateNone         = ""
	StateAwaitingCity = "awaiting_city"
	StateAwaitingTime = "awaiting_time"
)

type User struct {
	ID            int64
	Username      string
	FirstName     string
	CityName      *string // nil, пока город не задан
	CityLat       *float64
	CityLon       *float64
	Timezone      string
	DailyTime     string // "ЧЧ:ММ" по локальному времени пользователя
	DailyEnabled  bool
	LastDailyDate *string // "ГГГГ-ММ-ДД", когда последний раз отправляли утренний прогноз
	State         string
}

func (u *User) HasCity() bool { return u.CityName != nil }

type Storage struct{ db *sql.DB }

func New(db *sql.DB) *Storage {
	return &Storage{db: db}
}

const userCols = `id, username, first_name, city_name, city_lat, city_lon,
	timezone, daily_time, daily_enabled, last_daily_date, state`

// EnsureUser создаёт пользователя, если его нет, и обновляет имя/юзернейм.
func (s *Storage) EnsureUser(ctx context.Context, id int64, username, firstName string) (*User, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, first_name)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''))
		ON CONFLICT (id) DO UPDATE
		SET username   = excluded.username,
			first_name = excluded.first_name,
			updated_at = datetime('now')`,
		id, username, firstName)
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, id)
}

// GetUser возвращает пользователя или (nil, nil), если его нет.
func (s *Storage) GetUser(ctx context.Context, id int64) (*User, error) {
	u := &User{}
	err := s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE id = ?`, id,
	).Scan(
		&u.ID, &u.Username, &u.FirstName, &u.CityName, &u.CityLat, &u.CityLon,
		&u.Timezone, &u.DailyTime, &u.DailyEnabled, &u.LastDailyDate, &u.State,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateCity сохраняет город и помечает сегодняшнюю рассылку отправленной:
// пользователь уже получил прогноз в момент онбординга, и шедулер не должен
// на ближайшем тике «навёрстывать» этот день вторым сообщением.
func (s *Storage) UpdateCity(ctx context.Context, id int64, name string, lat, lon float64, timezone, dailySentDate string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET city_name = ?, city_lat = ?, city_lon = ?,
			timezone = ?, state = '',
			last_daily_date = NULLIF(?, ''),
			updated_at = datetime('now')
		WHERE id = ?`,
		name, lat, lon, timezone, dailySentDate, id)
	return err
}

func (s *Storage) UpdateDailyTime(ctx context.Context, id int64, hhmm string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET daily_time = ?, state = '', updated_at = datetime('now')
		WHERE id = ?`, hhmm, id)
	return err
}

func (s *Storage) SetDailyEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET daily_enabled = ?, updated_at = datetime('now')
		WHERE id = ?`, enabled, id)
	return err
}

func (s *Storage) SetState(ctx context.Context, id int64, state string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET state = ?, updated_at = datetime('now')
		WHERE id = ?`, state, id)
	return err
}

func (s *Storage) MarkDailySent(ctx context.Context, id int64, date string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET last_daily_date = ?, updated_at = datetime('now')
		WHERE id = ?`, date, id)
	return err
}

// DailyUsers — те, кому нужно отправлять утренний прогноз.
func (s *Storage) DailyUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userCols+` FROM users
		WHERE daily_enabled = 1 AND city_name IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Username, &u.FirstName, &u.CityName, &u.CityLat, &u.CityLon,
			&u.Timezone, &u.DailyTime, &u.DailyEnabled, &u.LastDailyDate, &u.State,
		); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
