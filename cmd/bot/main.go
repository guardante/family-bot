package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	// Часовые пояса, вшитые в бинарник (в scratch-образе нет tzdata).
	_ "time/tzdata"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/bot"
	"nadia_bot/internal/config"
	"nadia_bot/internal/glm"
	"nadia_bot/internal/storage"
)

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

	api, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		slog.Error("telegram", "err", err)
		os.Exit(1)
	}
	slog.Info("бот запущен", "username", api.Self.UserName, "model", cfg.GLMModel)

	store := storage.New(db)
	b := bot.New(cfg, api, store, glm.New(cfg.GLMAPIKey, cfg.GLMBaseURL, cfg.GLMModel))

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
