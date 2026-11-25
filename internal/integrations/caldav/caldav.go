// Package caldav: запись событий в календарь Apple (iCloud CalDAV)
// и любой совместимый CalDAV-сервер (Radicale, Nextcloud и т.п.).
package caldav

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

type Client struct{ http *http.Client }

func New() *Client {
	return &Client{http: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) client(ctx context.Context, login, password, endpoint string) (*caldav.Client, error) {
	dav := webdav.HTTPClientWithBasicAuth(c.http, login, password)
	return caldav.NewClient(dav, endpoint)
}

// Validate проверяет учётные данные (discovery principal -> home set).
func (c *Client) Validate(ctx context.Context, login, password, endpoint string) error {
	cl, err := c.client(ctx, login, password, endpoint)
	if err != nil {
		return err
	}
	principal, err := cl.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return fmt.Errorf("principal: %w", err)
	}
	if _, err := cl.FindCalendarHomeSet(ctx, principal); err != nil {
		return fmt.Errorf("home set: %w", err)
	}
	return nil
}

// findCalendarPath находит календарь СТРОГО по имени. Никаких fallback-ов:
// предсказуемость важнее «удобства». Календарь должен существовать,
// принимать события (VEVENT) и быть записываемым.
func (c *Client) findCalendarPath(ctx context.Context, cl *caldav.Client, principal, name string) (string, error) {
	home, err := cl.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return "", fmt.Errorf("home set: %w", err)
	}
	calendars, err := cl.FindCalendars(ctx, home)
	if err != nil {
		return "", fmt.Errorf("список календарей: %w", err)
	}
	for _, cal := range calendars {
		if strings.EqualFold(strings.TrimSpace(cal.Name), name) {
			if cal.ReadOnly {
				return "", fmt.Errorf("календарь %q только для чтения", name)
			}
			return cal.Path, nil
		}
	}
	return "", fmt.Errorf("не найден календарь %q — создай его в приложении Календарь (или задай CALDAV_CALENDAR)", name)
}

// PutEvent создаёт событие, возвращает UID и путь календаря, куда записали.
func (c *Client) PutEvent(ctx context.Context, login, password, endpoint, calendarName, title string, at time.Time) (string, string, error) {
	return c.putEvent(ctx, login, password, endpoint, calendarName, "", title, at)
}

// UpdateEvent перезаписывает событие с известным UID в том же календаре.
func (c *Client) UpdateEvent(ctx context.Context, login, password, endpoint, calendarName, uid, title string, at time.Time) error {
	_, _, err := c.putEvent(ctx, login, password, endpoint, calendarName, uid, title, at)
	return err
}

// putEvent: calendarName — имя календаря из настроек (один и тот же всегда).
func (c *Client) putEvent(ctx context.Context, login, password, endpoint, calendarName, uid, title string, at time.Time) (string, string, error) {
	cl, err := c.client(ctx, login, password, endpoint)
	if err != nil {
		return "", "", err
	}
	principal, err := cl.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return "", "", fmt.Errorf("principal: %w", err)
	}
	path, err := c.findCalendarPath(ctx, cl, principal, calendarName)
	if err != nil {
		return "", "", err
	}

	if uid == "" {
		uid = "nadia-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@nadia.bot"
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//nadia bot//RU")

	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	ev.Props.SetText(ical.PropSummary, title)
	ev.Props.SetDateTime(ical.PropDateTimeStart, at.UTC())
	ev.Props.SetDateTime(ical.PropDateTimeEnd, at.UTC().Add(time.Hour))
	cal.Children = append(cal.Children, ev.Component)

	objPath := strings.TrimSuffix(path, "/") + "/" + uid + ".ics"
	if _, err := cl.PutCalendarObject(ctx, objPath, cal, nil); err != nil {
		return "", "", fmt.Errorf("запись события (календарь %s): %w", path, err)
	}
	return uid, path, nil
}

// DeleteEvent удаляет событие по UID из настроенного календаря.
func (c *Client) DeleteEvent(ctx context.Context, login, password, endpoint, calendarName, uid string) error {
	cl, err := c.client(ctx, login, password, endpoint)
	if err != nil {
		return err
	}
	principal, err := cl.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return err
	}
	path, err := c.findCalendarPath(ctx, cl, principal, calendarName)
	if err != nil {
		return err
	}
	objPath := strings.TrimSuffix(path, "/") + "/" + uid + ".ics"
	return cl.RemoveAll(ctx, objPath, nil)
}

// BuildICS собирает ICS-тело события — используется в тестах.
func BuildICS(title string, at time.Time) []byte {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropSummary, title)
	ev.Props.SetDateTime(ical.PropDateTimeStart, at.UTC())
	cal.Children = append(cal.Children, ev.Component)
	var buf bytes.Buffer
	_ = ical.NewEncoder(&buf).Encode(cal)
	return buf.Bytes()
}

// NewHTTP открывает http.Client для внешней диагностики (тесты).
func (c *Client) NewHTTP() *http.Client { return c.http }
