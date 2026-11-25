package caldav

// Интеграционный тест против реального CalDAV-сервера.
// Запуск: CALDAV_TEST_URL=http://127.0.0.1:5232 CALDAV_TEST_USER=testuser go test -run TestCaldav ./...
// Локально удобно поднять Radicale:
//   pip install radicale && radicale --config <cfg с auth=none>
// и создать коллекцию: curl -X MKCOL http://127.0.0.1:5232/testuser/ && curl -X MKCALENDAR http://127.0.0.1:5232/testuser/kalendar/

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCaldavFullCycle(t *testing.T) {
	url := os.Getenv("CALDAV_TEST_URL")
	if url == "" {
		t.Skip("CALDAV_TEST_URL не задан")
	}
	user := os.Getenv("CALDAV_TEST_USER")
	if user == "" {
		user = "testuser"
	}

	c := New()
	ctx := context.Background()

	if err := c.Validate(ctx, user, "pass", url); err != nil {
		t.Fatalf("validate: %v", err)
	}

	at := time.Now().Add(24 * time.Hour).Truncate(time.Minute)
	uid, path, err := c.PutEvent(ctx, user, "pass", url, "kalendar", "Тестовое событие Надя", at)
	if err != nil {
		t.Fatalf("put event: %v", err)
	}
	if uid == "" || path == "" {
		t.Fatal("пустой uid или path")
	}

	// Перезапись с тем же UID не должна падать.
	if _, _, err := c.PutEvent(ctx, user, "pass", url, "kalendar", "Тестовое событие Надя", at); err != nil {
		t.Fatalf("re-put event: %v", err)
	}
	// Обновление по сохранённому пути (как в проде).
	if err := c.UpdateEvent(ctx, user, "pass", url, path, uid, "Тестовое событие Надя", at.Add(time.Hour)); err != nil {
		t.Fatalf("update event: %v", err)
	}

	if err := c.DeleteEvent(ctx, user, "pass", url, path, uid); err != nil {
		t.Fatalf("delete event: %v", err)
	}
}
