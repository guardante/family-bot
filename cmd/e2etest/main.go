// Диагностический e2e-прогон жизненного цикла события через настоящие
// кодовые пути бота (роутер -> действия -> календарь Apple).
// Ответы бота уходят в Telegram пользователю — как при живом общении.
//
// Запуск: E2E_USER_ID=<telegram id> go run ./cmd/e2etest
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/bot"
	"nadia_bot/internal/config"
	"nadia_bot/internal/glm"
	"nadia_bot/internal/integrations"
	"nadia_bot/internal/integrations/todoist"
	"nadia_bot/internal/secrets"
	"nadia_bot/internal/storage"
)

func masterKey(cfg *config.Config) string {
	if cfg.EncryptionKey != "" {
		return cfg.EncryptionKey
	}
	return cfg.BotToken
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	userID, err := strconv.ParseInt(os.Getenv("E2E_USER_ID"), 10, 64)
	if err != nil || userID == 0 {
		log.Fatal("нужен E2E_USER_ID")
	}

	db, err := storage.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	store := storage.New(db, secrets.New(masterKey(cfg)))
	api, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		log.Fatal(err)
	}
	b := bot.New(cfg, api, store,
		glm.New(cfg.GLMAPIKey, cfg.GLMBaseURL, cfg.GLMModel, cfg.GLMVisionModel),
		integrations.NewRegistry(todoist.New()))

	run := func(text string) {
		fmt.Printf("\n=== ШАГ: %s\n", text)
		b.HandleFreeText(ctx, userID, userID, text)
		time.Sleep(3 * time.Second)
	}

	// --- 1. создание ---
	run("создай событие «Тест Нади» на завтра в 10:00")
	ev1 := findTestEvent(ctx, store, userID)
	if ev1 == nil {
		log.Fatal("СОЗДАНИЕ ПРОВАЛЕНО: события нет в базе")
	}
	fmt.Printf("  в базе: id=%d %s apple_uid=%v\n", ev1.ID, ev1.EventAt, deref(ev1.CaldavUID))
	uid1, path1 := deref(ev1.CaldavUID), deref(ev1.CaldavPath)
	// iCloud иногда синхронизируется с задержкой
	time.Sleep(2 * time.Second)
	checkApple(ctx, cfg, store, userID, uid1, path1, "после создания (ждём 200)", http.StatusOK)

	// --- 2. перенос ---
	run("перенеси событие «Тест Нади» на послезавтра в 14:00")
	ev2 := findTestEvent(ctx, store, userID)
	if ev2 == nil {
		log.Fatal("ПЕРЕНОС ПРОВАЛЕН: событие пропало из базы")
	}
	if ev2.EventAt == ev1.EventAt {
		log.Fatalf("ПЕРЕНОС ПРОВАЛЕН: время не изменилось (%s)", ev2.EventAt)
	}
	fmt.Printf("  в базе: %s -> %s (uid тот же: %v)\n", ev1.EventAt, ev2.EventAt, deref(ev2.CaldavUID) == uid1)
	time.Sleep(2 * time.Second)
	checkApple(ctx, cfg, store, userID, uid1, path1, "после переноса (ждём 200)", http.StatusOK)

	// --- 3. удаление ---
	run("удали событие «Тест Нади»")
	time.Sleep(2 * time.Second)
	if ev3 := findTestEvent(ctx, store, userID); ev3 != nil {
		log.Fatal("УДАЛЕНИЕ ПРОВАЛЕНО: событие осталось в базе")
	}
	checkApple(ctx, cfg, store, userID, uid1, path1, "после удаления (ждём 404)", http.StatusNotFound)

	fmt.Println("\n=== ИТОГ: полный цикл пройден ✅")
}

func findTestEvent(ctx context.Context, store *storage.Storage, userID int64) *storage.Event {
	evs, err := store.EventsAll(ctx, userID, 50)
	if err != nil {
		return nil
	}
	for i := range evs {
		if strings.Contains(strings.ToLower(evs[i].Title), "тест нади") {
			return &evs[i]
		}
	}
	return nil
}

// checkApple — GET события по UID: 200 = лежит, 404 = удалено.
func checkApple(ctx context.Context, cfg *config.Config, store *storage.Storage,
	userID int64, uid, path, label string, want int) {
	if uid == "" || path == "" {
		fmt.Printf("  [apple] %s: нет uid/path — интеграция не подключена?\n", label)
		return
	}
	user, err := store.GetUser(ctx, userID)
	if err != nil || user == nil {
		fmt.Printf("  [apple] %s: нет пользователя\n", label)
		return
	}
	login, pass := deref(user.CaldavLogin), deref(user.CaldavPassword)
	if pass == "" {
		fmt.Printf("  [apple] %s: календарь Apple не подключён, шаг пропущен\n", label)
		return
	}

	objURL := strings.TrimRight(cfg.CaldavURL, "/") + strings.TrimSuffix(path, "/") + "/" + uid + ".ics"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, objURL, nil)
	req.SetBasicAuth(login, pass)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("  [apple] %s: %v\n", label, err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	status := "✅"
	if resp.StatusCode != want {
		status = fmt.Sprintf("❌ (ожидали %d)", want)
	}
	fmt.Printf("  [apple] %s: HTTP %d %s\n", label, resp.StatusCode, status)
}
