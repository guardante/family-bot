package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	// Часовые пояса, вшитые в бинарник (в scratch-образе нет tzdata).
	_ "time/tzdata"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/bot"
	"nadia_bot/internal/config"
	"nadia_bot/internal/glm"
	"nadia_bot/internal/integrations"
	"nadia_bot/internal/integrations/todoist"
	"nadia_bot/internal/secrets"
	"nadia_bot/internal/storage"
	"nadia_bot/internal/web"
)

// masterKey — ключ шифрования секретов: ENCRYPTION_KEY или вывод из BOT_TOKEN.
func masterKey(cfg *config.Config) string {
	if cfg.EncryptionKey != "" {
		return cfg.EncryptionKey
	}
	return cfg.BotToken
}

const numWorkers = 4

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("конфиг", "err", err)
		os.Exit(1)
	}

	db, err := storage.Open(cfg.SQLitePath)
	if err != nil {
		slog.Error("sqlite", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := storage.Migrate(ctx, db); err != nil {
		slog.Error("миграции", "err", err)
		os.Exit(1)
	}
	if fixed, err := storage.New(db, secrets.New(masterKey(cfg))).ReencryptSecrets(ctx); err != nil {
		slog.Error("перешифрование секретов", "err", err)
	} else if fixed > 0 {
		slog.Info("секреты перенесены в шифрованный вид", "users", fixed)
	}

	api, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		slog.Error("telegram", "err", err)
		os.Exit(1)
	}
	slog.Info("бот запущен", "username", api.Self.UserName, "model", cfg.GLMModel)

	// Шифрование секретов в базе. Мастер-ключ: ENCRYPTION_KEY, а без неё —
	// выводим из BOT_TOKEN (база без .env в отрыве бесполезна).
	master := masterKey(cfg)
	if cfg.EncryptionKey == "" {
		slog.Warn("ENCRYPTION_KEY не задан — использую ключ из BOT_TOKEN. Рекомендую задать отдельный ENCRYPTION_KEY.")
	}
	cipher := secrets.New(master)

	store := storage.New(db, cipher)
	registry := integrations.NewRegistry(todoist.New())
	b := bot.New(cfg, api, store,
		glm.New(cfg.GLMAPIKey, cfg.GLMBaseURL, cfg.GLMModel, cfg.GLMVisionModel),
		registry)

	// WebApp: статика мини-приложения и JSON API. Запускается только если
	// задан WEBAPP_URL (публичный HTTPS) — иначе ничего не слушаем.
	if cfg.WebAppURL != "" {
		webHandler := web.New(store, registry, cfg.BotToken, cfg.IsAllowed, web.StaticFS())
		webMux := http.NewServeMux()
		webHandler.Register(webMux)
		srv := &http.Server{
			Addr:              ":" + cfg.WebAppPort,
			Handler:           webMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			slog.Info("веб-сервер запущен", "port", cfg.WebAppPort)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("веб-сервер", "err", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()

		// Кнопка меню чата открывает мини-приложение (нужен публичный HTTPS URL).
		// В tgbotapi v5 нет типов Bot API 6.x, поэтому сырой запрос.
		params := tgbotapi.Params{
			"menu_button": `{"type":"web_app","text":"Надя","web_app":{"url":"` + cfg.WebAppURL + `"}}`,
		}
		if _, err := api.MakeRequest("setChatMenuButton", params); err != nil {
			slog.Warn("не удалось задать кнопку меню", "err", err)
		}
	}

	if err := b.SetCommands(ctx); err != nil {
		slog.Warn("не удалось задать команды", "err", err)
	}

	go b.RunScheduler(ctx)

	// Long polling: не нужен публичный IP и TLS — идеально для слабого сервера.
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := api.GetUpdatesChan(u)

	queue := make(chan tgbotapi.Update, 64)

	// Производитель: перекладывает апдейты в очередь воркеров.
	go func() {
		defer close(queue)
		for {
			select {
			case <-ctx.Done():
				api.StopReceivingUpdates()
				return
			case upd := <-updates:
				if upd.Message == nil && upd.CallbackQuery == nil {
					continue // нулевой апдейт или нерелевантное событие
				}
				select {
				case queue <- upd:
				case <-ctx.Done():
					api.StopReceivingUpdates()
					return
				}
			}
		}
	}()

	// Воркеры: обрабатывают сообщения параллельно, чтобы долгий ответ LLM
	// не задерживал остальные сообщения.
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for upd := range queue {
				b.HandleUpdate(ctx, upd)
			}
		}()
	}
	wg.Wait()

	slog.Info("бот остановлен")
}
