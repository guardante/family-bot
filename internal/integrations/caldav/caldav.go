// Package caldav: запись событий в календарь Apple (iCloud CalDAV)
// и любой совместимый CalDAV-сервер (Radicale, Nextcloud и т.п.).
package caldav

import (
	"bytes"
	"context"
	"errors"
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

// findCalendarPath возвращает путь календаря: с именем name, иначе первый.
func (c *Client) findCalendarPath(ctx context.Context, cl *caldav.Client, principal, name string) (string, error) {
	home, err := cl.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return "", fmt.Errorf("home set: %w", err)
	}
	calendars, err := cl.FindCalendars(ctx, home)
	if err != nil {
		return "", fmt.Errorf("список календарей: %w", err)
	}
	var first string
	for _, cal := range calendars {
		if first == "" {
			first = cal.Path
		}
		if strings.EqualFold(cal.Name, name) {
			return cal.Path, nil
		}
	}
	if first == "" {
		return "", errors.New("не найдено ни одного календаря")
	}
	return first, nil
}

// PutEvent создаёт (или перезаписывает) событие и возвращает UID.
func (c *Client) PutEvent(ctx context.Context, login, password, endpoint, calendarName, title string, at time.Time) (string, error) {
	cl, err := c.client(ctx, login, password, endpoint)
	if err != nil {
		return "", err
	}
	principal, err := cl.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return "", fmt.Errorf("principal: %w", err)
	}
	path, err := c.findCalendarPath(ctx, cl, principal, calendarName)
	if err != nil {
		return "", err
	}

	uid := "nadia-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@nadia.bot"

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
		return "", fmt.Errorf("запись события: %w", err)
	}
	return uid, nil
}

// DeleteEvent удаляет событие по UID.
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
