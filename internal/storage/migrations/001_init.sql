-- Пользователи бота. id = Telegram user id.
CREATE TABLE IF NOT EXISTS users (
    id              INTEGER PRIMARY KEY,
    username        TEXT,
    first_name      TEXT,
    city_name       TEXT,
    city_lat        REAL,
    city_lon        REAL,
    timezone        TEXT        NOT NULL DEFAULT 'Europe/Moscow',
    daily_time      TEXT        NOT NULL DEFAULT '08:00',
    daily_enabled   INTEGER     NOT NULL DEFAULT 1,
    last_daily_date TEXT,
    state           TEXT        NOT NULL DEFAULT '',
    created_at      TEXT        NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT        NOT NULL DEFAULT (datetime('now'))
);

-- История диалога с LLM, храним последние N сообщений на пользователя.
CREATE TABLE IF NOT EXISTS messages (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER     NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT        NOT NULL CHECK (role IN ('user', 'assistant')),
    content    TEXT        NOT NULL,
    created_at TEXT        NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_messages_user_id_created ON messages (user_id, created_at DESC, id DESC);
