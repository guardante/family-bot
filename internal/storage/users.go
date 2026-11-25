package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"nadia_bot/internal/secrets"
)

// Состояния онбординга и диалогов пользователя.
const (
	StateNone            = ""
	StateAwaitingCity    = "awaiting_city"
	StateAwaitingTime    = "awaiting_time"
	StateAwaitingTodo    = "awaiting_todo"
	StateAwaitingTodoist = "awaiting_todoist_token"
	StateAwaitingCaldav  = "awaiting_caldav"
)

type User struct {
	ID             int64
	Username       string
	FirstName      string
	CityName       *string // nil, пока город не задан
	CityLat        *float64
	CityLon        *float64
	Timezone       string
	DailyTime      string // "ЧЧ:ММ" по локальному времени пользователя
	DailyEnabled   bool
	LastDailyDate  *string // "ГГГГ-ММ-ДД", когда последний раз отправляли утренний прогноз
	State          string
	TodoistToken   *string // персональный API-токен Todoist (опциональная интеграция)
	CaldavLogin    *string // Apple ID / логин CalDAV (опциональная интеграция)
	CaldavPassword *string // пароль приложения
}

func (u *User) HasCity() bool { return u.CityName != nil }

type Storage struct {
	db     *sql.DB
	cipher *secrets.Cipher
}

func New(db *sql.DB, cipher *secrets.Cipher) *Storage {
	if cipher == nil {
		cipher = secrets.New("")
	}
	return &Storage{db: db, cipher: cipher}
}

const userCols = `id, username, first_name, city_name, city_lat, city_lon,
	timezone, daily_time, daily_enabled, last_daily_date, state, todoist_token,
	caldav_login, caldav_password`

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
		&u.Timezone, &u.DailyTime, &u.DailyEnabled, &u.LastDailyDate, &u.State, &u.TodoistToken,
		&u.CaldavLogin, &u.CaldavPassword,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.decryptUser(u)
	return u, nil
}

// decryptUser расшифровывает секретные поля после чтения из базы.
func (s *Storage) decryptUser(u *User) {
	if u.TodoistToken != nil {
		dec := s.cipher.Decrypt(*u.TodoistToken)
		u.TodoistToken = &dec
	}
	if u.CaldavPassword != nil {
		dec := s.cipher.Decrypt(*u.CaldavPassword)
		u.CaldavPassword = &dec
	}
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

func (s *Storage) SetTodoistToken(ctx context.Context, id int64, token string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET todoist_token = NULLIF(?, ''), updated_at = datetime('now')
		WHERE id = ?`, s.cipher.Encrypt(token), id)
	return err
}

// SetCaldavAuth сохраняет (пустая строка = отключить) учётку календаря.
func (s *Storage) SetCaldavAuth(ctx context.Context, id int64, login, password string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET caldav_login = NULLIF(?, ''), caldav_password = NULLIF(?, ''),
			updated_at = datetime('now')
		WHERE id = ?`, login, s.cipher.Encrypt(password), id)
	return err
}

// ReencryptSecrets переносит секреты из легаси-открытого текста в шифрованный
// вид. Вызывается на старте; безопасно запускать повторно.
func (s *Storage) ReencryptSecrets(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, todoist_token, caldav_password FROM users
		WHERE todoist_token IS NOT NULL OR caldav_password IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id    int64
		token string
		pass  string
	}
	var toFix []row
	for rows.Next() {
		var r row
		var token, pass *string
		if err := rows.Scan(&r.id, &token, &pass); err != nil {
			rows.Close()
			return 0, err
		}
		if token != nil {
			r.token = *token
		}
		if pass != nil {
			r.pass = *pass
		}
		// шифруем только те, что ещё не в формате enc:v1:
		if strings.HasPrefix(r.token, "enc:v1:") && strings.HasPrefix(r.pass, "enc:v1:") {
			continue
		}
		toFix = append(toFix, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	fixed := 0
	for _, r := range toFix {
		_, err := s.db.ExecContext(ctx, `
			UPDATE users SET todoist_token = ?, caldav_password = ? WHERE id = ?`,
			s.cipher.Encrypt(r.token), s.cipher.Encrypt(r.pass), r.id)
		if err != nil {
			return fixed, err
		}
		fixed++
	}
	return fixed, nil
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
			&u.Timezone, &u.DailyTime, &u.DailyEnabled, &u.LastDailyDate, &u.State, &u.TodoistToken,
			&u.CaldavLogin, &u.CaldavPassword,
		); err != nil {
			return nil, err
		}
		users = append(users, u)
		s.decryptUser(&users[len(users)-1])
	}
	return users, rows.Err()
}
