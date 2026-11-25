package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteFullCycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nadia_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Повторно — миграции должны быть идемпотентны.
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate idempotent: %v", err)
	}

	s := New(db)

	// --- создание пользователя ---
	u, err := s.EnsureUser(ctx, 42, "tester", "Тест")
	if err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	if u == nil || u.ID != 42 || u.HasCity() {
		t.Fatalf("unexpected new user: %+v", u)
	}
	if u.Timezone != "Europe/Moscow" || u.DailyTime != "08:00" || !u.DailyEnabled {
		t.Fatalf("unexpected defaults: %+v", u)
	}

	// --- отсутствующий пользователь ---
	if got, err := s.GetUser(ctx, 999); got != nil || err != nil {
		t.Fatalf("want nil,nil for missing user, got %+v, %v", got, err)
	}

	// --- город ---
	if err := s.UpdateCity(ctx, 42, "Казань", 55.79, 49.13, "Europe/Moscow"); err != nil {
		t.Fatalf("update city: %v", err)
	}
	u, err = s.GetUser(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !u.HasCity() || *u.CityName != "Казань" || u.Timezone != "Europe/Moscow" {
		t.Fatalf("city not saved: %+v", u)
	}
	if u.CityLat == nil || *u.CityLat < 55.78 || *u.CityLat > 55.80 {
		t.Fatalf("lat not saved: %+v", u)
	}

	// --- время рассылки (сбрасывает state) ---
	if err := s.SetState(ctx, 42, StateAwaitingTime); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDailyTime(ctx, 42, "09:30"); err != nil {
		t.Fatal(err)
	}
	if u, _ = s.GetUser(ctx, 42); u.DailyTime != "09:30" || u.State != StateNone {
		t.Fatalf("daily time/state: %+v", u)
	}

	// --- вкл/выкл рассылки и DailyUsers ---
	if err := s.SetDailyEnabled(ctx, 42, false); err != nil {
		t.Fatal(err)
	}
	if users, err := s.DailyUsers(ctx); err != nil || len(users) != 0 {
		t.Fatalf("daily users after off: %v %v", users, err)
	}
	if err := s.SetDailyEnabled(ctx, 42, true); err != nil {
		t.Fatal(err)
	}
	if users, err := s.DailyUsers(ctx); err != nil || len(users) != 1 {
		t.Fatalf("daily users after on: %v %v", users, err)
	}

	// --- сообщения: порядок и обрезка ---
	for i := 1; i <= 5; i++ {
		if err := s.AddMessage(ctx, 42, "user", "вопрос"); err != nil {
			t.Fatal(err)
		}
		if err := s.AddMessage(ctx, 42, "assistant", "ответ"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.TrimMessages(ctx, 42, 3); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.RecentMessages(ctx, 42, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages after trim, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" || msgs[2].Role != "assistant" {
		t.Fatalf("unexpected order: %+v", msgs)
	}

	// --- отметка отправки ---
	if err := s.MarkDailySent(ctx, 42, "2026-11-25"); err != nil {
		t.Fatal(err)
	}
	if u, _ = s.GetUser(ctx, 42); u.LastDailyDate == nil || *u.LastDailyDate != "2026-11-25" {
		t.Fatalf("last daily date: %+v", u)
	}

	// --- upsert не затирает город ---
	if _, err := s.EnsureUser(ctx, 42, "tester_new", "Тест2"); err != nil {
		t.Fatal(err)
	}
	if u, _ = s.GetUser(ctx, 42); !u.HasCity() || *u.CityName != "Казань" {
		t.Fatalf("city lost on upsert: %+v", u)
	}

	// --- очистка истории ---
	if err := s.ClearMessages(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := s.RecentMessages(ctx, 42, 100); len(msgs) != 0 {
		t.Fatalf("messages not cleared: %d", len(msgs))
	}
}
