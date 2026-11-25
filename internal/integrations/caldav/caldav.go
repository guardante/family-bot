// Package caldav: запись событий в календарь Apple (iCloud CalDAV)
// и любой совместимый CalDAV-сервер (Radicale, Nextcloud и т.п.).
package caldav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// findCalendarPath возвращает путь календаря для событий:
// приоритет — календарь с именем name («Надя»), иначе первый записываемый,
// который принимает VEVENT. Списки Reminders у iCloud отдают только VTODO —
// запись VEVENT в них даёт 403, поэтому фильтруем по SupportedComponentSet.
func (c *Client) findCalendarPath(ctx context.Context, cl *caldav.Client, principal, name string) (string, error) {
	home, err := cl.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return "", fmt.Errorf("home set: %w", err)
	}
	calendars, err := cl.FindCalendars(ctx, home)
	if err != nil {
		return "", fmt.Errorf("список календарей: %w", err)
	}
	fallback := ""
	for _, cal := range calendars {
		if !supportsEvents(cal) {
			continue
		}
		if strings.EqualFold(cal.Name, name) {
			return cal.Path, nil
		}
		if fallback == "" {
			fallback = cal.Path
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", errors.New("нет подходящего календаря для событий (только списки напоминаний?)")
}

func supportsEvents(cal caldav.Calendar) bool {
	if cal.ReadOnly {
		return false
	}
	for _, comp := range cal.SupportedComponentSet {
		if strings.EqualFold(comp, "VEVENT") {
			return true
		}
	}
	return false
}

// PutEvent создаёт событие, возвращает UID и путь календаря, куда записали.
func (c *Client) PutEvent(ctx context.Context, login, password, endpoint, calendarName, title string, at time.Time) (string, string, error) {
	return c.putEvent(ctx, login, password, endpoint, calendarName, "", title, at)
}

// UpdateEvent перезаписывает событие с известным UID ровно в его календаре.
// calendarPath — путь из caldav_path события (или имя для поиска).
func (c *Client) UpdateEvent(ctx context.Context, login, password, endpoint, calendarPath, uid, title string, at time.Time) error {
	_, _, err := c.putEvent(ctx, login, password, endpoint, calendarPath, uid, title, at)
	return err
}

// putEvent: calendarRef — либо путь календаря (".../calendars/ID/"), либо имя.
func (c *Client) putEvent(ctx context.Context, login, password, endpoint, calendarRef, uid, title string, at time.Time) (string, string, error) {
	cl, err := c.client(ctx, login, password, endpoint)
	if err != nil {
		return "", "", err
	}

	path := calendarRef
	if !strings.Contains(path, "/calendars/") {
		// Это имя — нужно найти путь.
		principal, err := cl.FindCurrentUserPrincipal(ctx)
		if err != nil {
			return "", "", fmt.Errorf("principal: %w", err)
		}
		path, err = c.findCalendarPath(ctx, cl, principal, calendarRef)
		if err != nil {
			return "", "", err
		}
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

// FindEventCalendar ищет, в каком календаре лежит событие с данным UID.
// Нужно для легаси-событий, записанных до хранения пути.
func (c *Client) FindEventCalendar(ctx context.Context, login, password, endpoint, uid string) (string, error) {
	cl, err := c.client(ctx, login, password, endpoint)
	if err != nil {
		return "", err
	}
	principal, err := cl.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return "", err
	}
	home, err := cl.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return "", err
	}
	calendars, err := cl.FindCalendars(ctx, home)
	if err != nil {
		return "", err
	}
	client := c.http
	for _, cal := range calendars {
		if !supportsEvents(cal) {
			continue
		}
		objPath := strings.TrimSuffix(cal.Path, "/") + "/" + uid + ".ics"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, objPath, nil)
		if err != nil {
			return "", err
		}
		// basic auth через обёртку уже не применить — клиент голый.
		// Делаем запрос через caldav-клиент GET-ом с тем же транспортом.
		resp, err := client.Do(authRequest(req, login, password))
		if err != nil {
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return cal.Path, nil
		}
	}
	return "", errors.New("событие с этим UID не найдено ни в одном календаре")
}

func authRequest(req *http.Request, login, password string) *http.Request {
	req.SetBasicAuth(login, password)
	return req
}

// DeleteEvent удаляет событие по UID из указанного календаря (путь).
func (c *Client) DeleteEvent(ctx context.Context, login, password, endpoint, calendarPath, uid string) error {
	cl, err := c.client(ctx, login, password, endpoint)
	if err != nil {
		return err
	}
	path := calendarPath
	if !strings.Contains(path, "/calendars/") {
		principal, err := cl.FindCurrentUserPrincipal(ctx)
		if err != nil {
			return err
		}
		path, err = c.findCalendarPath(ctx, cl, principal, path)
		if err != nil {
			return err
		}
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
