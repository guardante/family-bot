package caldav

import (
	"context"
	"os"
	"testing"
)

func TestFindEventCalendar(t *testing.T) {
	login := os.Getenv("ICLOUD_LOGIN")
	pass := testPassword(t)
	uid := os.Getenv("FIND_UID")
	if login == "" || pass == "" || uid == "" {
		t.Skip("нужны ICLOUD_LOGIN, ICLOUD_PASS, FIND_UID")
	}
	c := New()
	path, err := c.FindEventCalendar(context.Background(), login, pass, "https://caldav.icloud.com", uid)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	t.Logf("найдено: %s", path)
}
