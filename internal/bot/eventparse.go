package bot

// Разбор аргументов команды /event:
//
//	/event 05.01 18:30 День рождения мамы
//	/event 05.01.2027 18:30 Отпуск
//
// Год опционален: если дата в этом году уже прошла, берётся следующий.

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

func parseEventArgs(args string, loc *time.Location, now time.Time) (string, time.Time, error) {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) < 3 {
		return "", time.Time{}, errors.New("нужны дата, время и название")
	}

	dateParts := strings.Split(fields[0], ".")
	if len(dateParts) != 2 && len(dateParts) != 3 {
		return "", time.Time{}, errors.New("дата — в формате ДД.ММ или ДД.ММ.ГГГГ")
	}
	day, errD := strconv.Atoi(dateParts[0])
	month, errM := strconv.Atoi(dateParts[1])
	if errD != nil || errM != nil || day < 1 || day > 31 || month < 1 || month > 12 {
		return "", time.Time{}, errors.New("не поняла дату")
	}

	year := now.In(loc).Year()
	withYear := len(dateParts) == 3
	if withYear {
		y, err := strconv.Atoi(dateParts[2])
		if err != nil || y < 2000 || y > 2100 {
			return "", time.Time{}, errors.New("не поняла год")
		}
		year = y
	}

	hm := strings.Split(fields[1], ":")
	if len(hm) != 2 {
		return "", time.Time{}, errors.New("время — в формате ЧЧ:ММ")
	}
	hour, errH := strconv.Atoi(hm[0])
	minute, errMin := strconv.Atoi(hm[1])
	if errH != nil || errMin != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return "", time.Time{}, errors.New("не поняла время")
	}

	at := time.Date(year, time.Month(month), day, hour, minute, 0, 0, loc)
	// Дата без года уже прошла в этом году — имеем в виду следующий год.
	if !withYear && at.Before(now) {
		at = at.AddDate(1, 0, 0)
	}

	title := strings.Join(fields[2:], " ")
	if runed := []rune(title); len(runed) > 200 {
		title = string(runed[:200])
	}
	if strings.TrimSpace(title) == "" {
		return "", time.Time{}, errors.New("нужно название")
	}
	return title, at, nil
}
