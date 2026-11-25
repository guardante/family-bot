package caldav

// Разовая проверка против реального iCloud. Запуск:
//   ICLOUD_LOGIN=... ICLOUD_PASS=... go test ./internal/integrations/caldav/ -run TestIcloud -v
// Создаёт тестовое событие и сразу удаляет его.

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestIcloudPush(t *testing.T) {
	login := os.Getenv("ICLOUD_LOGIN")
	pass := testPassword(t)
	if login == "" || pass == "" {
		t.Skip("ICLOUD_LOGIN/ICLOUD_PASS не заданы")
	}

	c := New()
	ctx := context.Background()

	if err := c.Validate(ctx, login, pass, "https://caldav.icloud.com"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	t.Log("валидация ок")

	calendar := os.Getenv("ICLOUD_CALENDAR")
	if calendar == "" {
		calendar = "Домашний"
	}

	at := time.Now().Add(24 * time.Hour).Truncate(time.Minute)
	uid, path, err := c.PutEvent(ctx, login, pass, "https://caldav.icloud.com", calendar, "Тест Надя (проверка)", at)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	t.Logf("событие записано, uid=%s path=%s", uid, path)

	if err := c.DeleteEvent(ctx, login, pass, "https://caldav.icloud.com", calendar, uid); err != nil {
		t.Logf("удаление: %v (событие можно убрать вручную)", err)
	} else {
		t.Log("удаление ок")
	}
}
