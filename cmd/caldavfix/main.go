// Обслуживание календаря Apple. Не для продакшена — диагностический инструмент.
//
//	go run ./cmd/caldavfix push EVENT_ID USER_ID   # перенести событие из базы в Apple
//	go run ./cmd/caldavfix check UID               # HTTP-статус события по UID (200/404)
//
// ICLOUD_LOGIN/ICLOUD_PASS не нужны: учётные данные берутся из базы
// и расшифровываются мастер-ключом (ENCRYPTION_KEY или BOT_TOKEN).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"nadia_bot/internal/integrations/caldav"
	"nadia_bot/internal/secrets"
	"nadia_bot/internal/storage"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("режимы: push EVENT_ID USER_ID | check UID")
	}
	sqlitePath := os.Getenv("SQLITE_PATH")
	if sqlitePath == "" {
		sqlitePath = "data/nadia.db"
	}
	db, err := sql.Open("sqlite", "file:"+sqlitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var login, encPass string
	if err := db.QueryRow(`SELECT caldav_login, caldav_password FROM users WHERE caldav_login IS NOT NULL LIMIT 1`).
		Scan(&login, &encPass); err != nil {
		log.Fatal("календарь Apple не подключён:", err)
	}
	master := os.Getenv("ENCRYPTION_KEY")
	if master == "" {
		master = os.Getenv("BOT_TOKEN")
	}
	pass := strings.TrimPrefix(secrets.New(master).Decrypt(encPass), "enc:v1:")

	c := caldav.New()
	ctx := context.Background()
	endpoint := os.Getenv("CALDAV_URL")
	if endpoint == "" {
		endpoint = "https://caldav.icloud.com"
	}
	calendar := os.Getenv("CALDAV_CALENDAR")
	if calendar == "" {
		calendar = "Домашний"
	}

	switch os.Args[1] {
	case "push":
		eventID, _ := strconv.ParseInt(os.Args[2], 10, 64)
		userID, _ := strconv.ParseInt(os.Args[3], 10, 64)
		var title, atRaw string
		if err := db.QueryRow(`SELECT title, event_at FROM events WHERE id = ? AND user_id = ?`, eventID, userID).
			Scan(&title, &atRaw); err != nil {
			log.Fatal(err)
		}
		at, err := time.Parse("2006-01-02T15:04:05Z", atRaw)
		if err != nil {
			log.Fatal(err)
		}
		uid, path, err := c.PutEvent(ctx, login, pass, endpoint, calendar, title, at)
		if err != nil {
			log.Fatal("push:", err)
		}
		store := storage.New(db, secrets.New(master))
		if err := store.SetEventCaldav(ctx, userID, eventID, uid, path); err != nil {
			log.Fatal(err)
		}
		fmt.Println("событие в календаре Apple, uid:", uid)

	case "check":
		if len(os.Args) < 3 {
			log.Fatal("нужен UID")
		}
		code, err := c.CheckObject(ctx, login, pass, endpoint, calendar, os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("HTTP", code)

	default:
		log.Fatal("режимы: push EVENT_ID USER_ID | check UID")
	}
}
