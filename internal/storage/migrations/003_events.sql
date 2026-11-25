-- События календаря. Хранятся локально; напоминания шлёт бот.
-- event_at — UTC в формате RFC3339 ("2006-01-02T15:04:05Z").
CREATE TABLE IF NOT EXISTS events (
    id             INTEGER PRIMARY KEY,
    user_id        INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title          TEXT    NOT NULL,
    event_at       TEXT    NOT NULL,
    remind_minutes INTEGER,          -- за сколько минут напомнить; NULL = без напоминания
    notified       INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_events_user_at ON events (user_id, event_at);
