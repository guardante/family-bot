// Package config загружает конфигурацию бота из переменных окружения.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// Telegram
	BotToken string

	// Белый список Telegram ID. Пустой = пустят всех.
	AllowedIDs []int64

	// GLM (OpenAI-совместимый API)
	GLMAPIKey  string
	GLMBaseURL string
	GLMModel   string

	// Postgres -> SQLite: путь к файлу базы
	SQLitePath string

	// Сколько последних сообщений диалога отправлять модели.
	HistoryLimit int
}

func Load() (*Config, error) {
	cfg := &Config{
		BotToken:     strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		GLMAPIKey:    strings.TrimSpace(os.Getenv("GLM_API_KEY")),
		GLMBaseURL:   getenv("GLM_BASE_URL", "https://api.z.ai/api/paas/v4"),
		GLMModel:     getenv("GLM_MODEL", "glm-4.5-flash"),
		SQLitePath:   getenv("SQLITE_PATH", "data/nadia.db"),
		HistoryLimit: 16,
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN не задан (токен от @BotFather)")
	}
	if cfg.GLMAPIKey == "" {
		return nil, fmt.Errorf("GLM_API_KEY не задан (ключ из кабинета Z.AI)")
	}

	if raw := strings.TrimSpace(os.Getenv("ALLOWED_TELEGRAM_IDS")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("ALLOWED_TELEGRAM_IDS: неверный id %q: %w", part, err)
			}
			cfg.AllowedIDs = append(cfg.AllowedIDs, id)
		}
	}

	return cfg, nil
}

// IsAllowed проверяет, пускаем ли мы этого пользователя.
// Если белый список пустой — пускаем всех.
func (c *Config) IsAllowed(id int64) bool {
	if len(c.AllowedIDs) == 0 {
		return true
	}
	for _, allowed := range c.AllowedIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
