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
	if err := s.UpdateCity(ctx, 42, "Казань", 55.79, 49.13, "Europe/Moscow", "2026-11-25"); err != nil {
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
	if u.LastDailyDate == nil || *u.LastDailyDate != "2026-11-25" {
		t.Fatalf("сегодняшняя рассылка должна быть помечена при онбординге: %+v", u)
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

	// --- список дел ---
	t1, err := s.AddTodo(ctx, 42, "Купить хлеб")
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.AddTodo(ctx, 42, "Позвонить маме")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTodoRemote(ctx, 42, t2, "todoist", "98765"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTodoDone(ctx, 42, t1, true); err != nil {
		t.Fatal(err)
	}

	items, err := s.Todos(ctx, 42, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Done || !items[1].Done {
		t.Fatalf("todos order/state: %+v", items)
	}
	// Выполненные уходят в конец списка, поэтому remote у items[0].
	if items[0].RemoteID == nil || *items[0].RemoteID != "98765" || *items[0].Provider != "todoist" {
		t.Fatalf("remote id not saved: %+v", items[0])
	}

	got, err := s.GetTodo(ctx, 42, t2)
	if err != nil || got == nil || got.Text != "Позвонить маме" {
		t.Fatalf("get todo: %+v %v", got, err)
	}

	if err := s.SetTodoDone(ctx, 42, t1, false); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.GetUser(ctx, 42); u == nil {
		t.Fatal("user lost")
	}
	if done, err := s.ClearDone(ctx, 42); err != nil || len(done) != 0 {
		t.Fatalf("clear done: %v %v", done, err)
	}
	if err := s.SetTodoDone(ctx, 42, t2, true); err != nil {
		t.Fatal(err)
	}
	if done, err := s.ClearDone(ctx, 42); err != nil || len(done) != 1 || done[0].ID != t2 {
		t.Fatalf("clear done after re-done: %v %v", done, err)
	}
	// t1 осталась активной, t2 удалена вместе с "сделанными".
	if items, _ := s.Todos(ctx, 42, 50); len(items) != 1 || items[0].ID != t1 || items[0].Done {
		t.Fatalf("todos after clear: %+v", items)
	}
	if _, err := s.DeleteTodo(ctx, 42, t1); err != nil {
		t.Fatal(err)
	}

	// --- токен интеграции ---
	if err := s.SetTodoistToken(ctx, 42, "secret-token"); err != nil {
		t.Fatal(err)
	}
	if u, _ = s.GetUser(ctx, 42); u.TodoistToken == nil || *u.TodoistToken != "secret-token" {
		t.Fatalf("todoist token: %+v", u)
	}
	if err := s.SetTodoistToken(ctx, 42, ""); err != nil {
		t.Fatal(err)
	}
	if u, _ = s.GetUser(ctx, 42); u.TodoistToken != nil {
		t.Fatalf("todoist token not cleared: %+v", u)
	}
}
