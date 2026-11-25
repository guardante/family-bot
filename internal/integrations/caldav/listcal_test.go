package caldav

import (
	"context"
	"os"
	"testing"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

func TestListCalendars(t *testing.T) {
	login := os.Getenv("ICLOUD_LOGIN")
	pass := os.Getenv("ICLOUD_PASS")
	if login == "" || pass == "" {
		t.Skip("нет env")
	}
	dav := webdav.HTTPClientWithBasicAuth(New().NewHTTP(), login, pass)
	cl, err := caldav.NewClient(dav, "https://caldav.icloud.com")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := cl.FindCurrentUserPrincipal(context.Background())
	if err != nil {
		t.Fatal("principal:", err)
	}
	t.Log("principal:", principal)
	home, err := cl.FindCalendarHomeSet(context.Background(), principal)
	if err != nil {
		t.Fatal("home:", err)
	}
	t.Log("home:", home)
	cals, err := cl.FindCalendars(context.Background(), home)
	if err != nil {
		t.Fatal("calendars:", err)
	}
	for _, c := range cals {
		t.Logf("name=%q readonly=%v vevent=%v path=%s", c.Name, c.ReadOnly, c.SupportedComponentSet, c.Path)
	}
}
