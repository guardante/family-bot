package caldav

import (
	"context"
	"os"
	"testing"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

func TestListEvents(t *testing.T) {
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
	principal, _ := cl.FindCurrentUserPrincipal(context.Background())
	home, _ := cl.FindCalendarHomeSet(context.Background(), principal)
	cals, err := cl.FindCalendars(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	for _, cal := range cals {
		if !supportsEvents(cal) {
			continue
		}
		objs, err := cl.QueryCalendar(context.Background(), cal.Path,
			&caldav.CalendarQuery{CompFilter: caldav.CompFilter{
				Name:  "VCALENDAR",
				Comps: []caldav.CompFilter{{Name: "VEVENT"}},
			}})
		if err != nil {
			t.Logf("календарь %q: %v", cal.Name, err)
			continue
		}
		for _, o := range objs {
			for _, ev := range o.Data.Events() {
				if p := ev.Props.Get("UID"); p != nil {
					title := ""
					if s := ev.Props.Get("SUMMARY"); s != nil {
						title = s.Value
					}
					t.Logf("календарь=%q uid=%s title=%q", cal.Name, p.Value, title)
				}
			}
		}
	}
}
