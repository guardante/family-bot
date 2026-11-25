package bot

import (
	"testing"
	"time"
)

func TestParseEventArgs(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Yerevan")
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, loc)

	t.Run("дата без года в будущем", func(t *testing.T) {
		title, at, err := parseEventArgs("05.12 18:30 День рождения мамы", loc, now)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if title != "День рождения мамы" {
			t.Fatalf("title: %q", title)
		}
		want := time.Date(2026, 12, 5, 18, 30, 0, 0, loc)
		if !at.Equal(want) {
			t.Fatalf("at: %v, want %v", at, want)
		}
	})

	t.Run("дата без года в прошлом — следующий год", func(t *testing.T) {
		_, at, err := parseEventArgs("01.02 09:00 Встреча", loc, now)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if at.Year() != 2027 {
			t.Fatalf("year: %d, want 2027", at.Year())
		}
	})

	t.Run("с явным годом", func(t *testing.T) {
		title, at, err := parseEventArgs("05.01.2027 09:00 Отпуск", loc, now)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if title != "Отпуск" || at.Year() != 2027 || at.Month() != time.January || at.Day() != 5 {
			t.Fatalf("got: %q %v", title, at)
		}
	})

	t.Run("многословное название", func(t *testing.T) {
		title, _, err := parseEventArgs("25.11 22:00 Позвонить бабушке и дедушке", loc, now)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if title != "Позвонить бабушке и дедушке" {
			t.Fatalf("title: %q", title)
		}
	})

	t.Run("ошибки", func(t *testing.T) {
		bad := []string{
			"",
			"05.12 18:30",
			"5 янв 18:30 название",
			"32.12 18:30 название",
			"05.13 18:30 название",
			"05.12 25:00 название",
			"05.12 1830 название",
			"05.12.20270 18:30 название",
		}
		for _, arg := range bad {
			if _, _, err := parseEventArgs(arg, loc, now); err == nil {
				t.Errorf("ожидалась ошибка для %q", arg)
			}
		}
	})
}
