package bot

// Снисходительный разбор даты и времени из ответов модели:
// время "09:00", "7:00", "9" или пустое (по умолчанию 09:00);
// дата "2026-11-26", "26.11.2026" или "26.11" (прошедшая -> следующий год).

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

const defaultEventTime = "09:00"

func lenientDateTime(dateRaw, timeRaw string, loc *time.Location, now time.Time) (time.Time, error) {
	dateRaw = strings.TrimSpace(dateRaw)
	timeRaw = strings.TrimSpace(timeRaw)
	if dateRaw == "" {
		return time.Time{}, errors.New("нет даты")
	}

	day, err := lenientDate(dateRaw, loc, now)
	if err != nil {
		return time.Time{}, err
	}

	hour, minute, err := lenientTime(timeRaw)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc), nil
}

func lenientDate(s string, loc *time.Location, now time.Time) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("02.01.2006", s, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("02.01", s, loc); err == nil {
		// Год не указан: если дата уже прошла — берём следующий год.
		t = t.AddDate(now.Year(), 0, 0)
		if t.Before(now) {
			t = t.AddDate(1, 0, 0)
		}
		return t, nil
	}
	return time.Time{}, errors.New("не поняла дату")
}

func lenientTime(s string) (int, int, error) {
	if s == "" {
		return 9, 0, nil // событие без времени — на утро
	}
	parts := strings.Split(s, ":")
	if len(parts) < 1 || len(parts) > 3 {
		return 0, 0, errors.New("не поняла время")
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	minute := 0
	if len(parts) >= 2 {
		if minute, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil {
			return 0, 0, err
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, errors.New("время вне диапазона")
	}
	return hour, minute, nil
}
