package bot

import (
	"testing"
	"time"
)

func TestLenientTime(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, loc)

	cases := []struct {
		date, tm     string
		wantH, wantM int
		wantErr      bool
	}{
		{"2026-11-26", "09:00", 9, 0, false},
		{"2026-11-26", "7:00", 7, 0, false}, // без ведущего нуля
		{"2026-11-26", "", 9, 0, false},     // нет времени -> утро
		{"26.11", "18:30", 18, 30, false},   // дата без года
		{"2026-11-26", "25:00", 0, 0, true}, // час вне диапазона
		{"", "09:00", 0, 0, true},           // нет даты
	}
	for _, c := range cases {
		got, err := lenientDateTime(c.date, c.tm, loc, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q %q: ждали ошибку, получили %v", c.date, c.tm, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q %q: err %v", c.date, c.tm, err)
			continue
		}
		if got.Hour() != c.wantH || got.Minute() != c.wantM {
			t.Errorf("%q %q: got %02d:%02d, want %02d:%02d", c.date, c.tm, got.Hour(), got.Minute(), c.wantH, c.wantM)
		}
	}
}

func TestLenientDatePastWithoutYear(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, loc)
	got, err := lenientDate("20.11", loc, now) // уже прошло в этом году
	if err != nil {
		t.Fatal(err)
	}
	if got.Year() != 2027 || got.Month() != time.November || got.Day() != 20 {
		t.Fatalf("got %v, want 20.11.2027", got)
	}
}
