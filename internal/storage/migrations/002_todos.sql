-- Список дел. Хранится локально всегда; remote_id/provider — для опциональных
-- синхронизаций с внешними сервисами (Todoist и др.).
CREATE TABLE IF NOT EXISTS todos (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    text       TEXT    NOT NULL,
    done       INTEGER NOT NULL DEFAULT 0,
    remote_id  TEXT,
    provider   TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    done_at    TEXT
);

CREATE INDEX IF NOT EXISTS idx_todos_user ON todos (user_id, done, id);

-- Интеграции: персональные токены внешних сервисов.
ALTER TABLE users ADD COLUMN todoist_token TEXT;
