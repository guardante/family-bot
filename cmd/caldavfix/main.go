// Разовый инструмент: переносит событие из локальной базы в календарь Apple
// и сохраняет привязку. Запуск:
//
//	EVENT_ID=1 ICLOUD_LOGIN=... ICLOUD_PASS=... go run ./cmd/caldavfix
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"nadia_bot/internal/integrations/caldav"
	"nadia_bot/internal/secrets"
	"nadia_bot/internal/storage"
)

func main() {
	sqlitePath := os.Getenv("SQLITE_PATH")
	if sqlitePath == "" {
		sqlitePath = "data/nadia.db"
	}
	eventID, _ := strconv.ParseInt(os.Getenv("EVENT_ID"), 10, 64)
	login, pass := os.Getenv("ICLOUD_LOGIN"), os.Getenv("ICLOUD_PASS")
	title := os.Getenv("EVENT_TITLE")
	atRaw := os.Getenv("EVENT_AT")
	if eventID == 0 || login == "" || pass == "" || title == "" || atRaw == "" {
		log.Fatal("нужны EVENT_ID, ICLOUD_LOGIN, ICLOUD_PASS, EVENT_TITLE, EVENT_AT")
	}
	at, err := time.Parse("2006-01-02T15:04:05Z", atRaw)
	if err != nil {
		log.Fatal(err)
	}

	db, err := storage.Open(sqlitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	master := os.Getenv("ENCRYPTION_KEY")
	if master == "" {
		master = os.Getenv("BOT_TOKEN")
	}
	cipher := secrets.New(master)
	ctx := context.Background()
	c := caldav.New()
	uid, path, err := c.PutEvent(ctx, login, pass, "https://caldav.icloud.com", "Надя", title, at)
	if err != nil {
		log.Fatal("push:", err)
	}
	_ = path
	userID, err := strconv.ParseInt(os.Getenv("USER_ID"), 10, 64)
	if err != nil {
		log.Fatal(err)
	}
	if err := storage.New(db, cipher).SetEventCaldav(ctx, userID, eventID, uid, path); err != nil {
		log.Fatal(err)
	}
	fmt.Println("событие перенесено в календарь Apple, uid:", uid)
}
