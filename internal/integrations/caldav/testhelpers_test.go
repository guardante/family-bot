package caldav

// Хелпер для диагностических тестов: пароль из базы зашифрован,
// расшифровываем тем же мастер-ключом, что и бот.

import (
	"os"
	"strings"
	"testing"

	"nadia_bot/internal/secrets"
)

func testPassword(t *testing.T) string {
	t.Helper()
	pass := os.Getenv("ICLOUD_PASS")
	if pass == "" {
		t.Skip("ICLOUD_PASS не задан")
	}
	if strings.HasPrefix(pass, "enc:v1:") {
		master := os.Getenv("ENCRYPTION_KEY")
		if master == "" {
			master = os.Getenv("BOT_TOKEN")
		}
		pass = secrets.New(master).Decrypt(pass)
	}
	return pass
}
