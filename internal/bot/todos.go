package bot

// Список дел: локально в SQLite + опциональная синхронизация с внешними
// сервисами через integrations.Registry.

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/storage"
)

const (
	maxTodosShown = 30 // сколько задач показываем в инлайн-кнопках
	maxTodoLen    = 200
)

func todoistToken(u *storage.User) string {
	if u.TodoistToken == nil {
		return ""
	}
	return *u.TodoistToken
}

func (b *Bot) sendTodosList(ctx context.Context, chatID int64, user *storage.User) {
	items, err := b.store.Todos(ctx, user.ID, maxTodosShown)
	if err != nil {
		slog.Error("todos list", "err", err)
		b.reply(ctx, chatID, "Не получилось загрузить дела 😔 Попробуй позже.")
		return
	}
	markup := todosMarkup(items)
	msg := tgbotapi.NewMessage(chatID, todosText(items))
	msg.ReplyMarkup = &markup
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("send todos", "err", err)
	}
}

func todosText(items []storage.Todo) string {
	open, done := 0, 0
	for _, t := range items {
		if t.Done {
			done++
		} else {
			open++
		}
	}
	if len(items) == 0 {
		return "📝 Дела пусты. Нажми «Добавить», чтобы создать первую задачу.\n" +
			"А ещё можно прислать фото списка — я распознаю текст."
	}
	return fmt.Sprintf("📝 Дела: %d в работе, %d сделано\nНажми на задачу, чтобы отметить.", open, done)
}

func todosMarkup(items []storage.Todo) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, t := range items {
		label := "☐ " + truncateRunes(t.Text, 42)
		if t.Done {
			label = "✅ " + truncateRunes(t.Text, 42)
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "td_toggle:"+strconv.FormatInt(t.ID, 10)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить", "td_add"),
		tgbotapi.NewInlineKeyboardButtonData("🧹 Убрать сделанные", "td_clear"),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) toggleTodo(ctx context.Context, cbID string, chatID int64, messageID int, user *storage.User, todoID int64) {
	t, err := b.store.GetTodo(ctx, user.ID, todoID)
	if err != nil || t == nil {
		b.answerCallback(cbID, "Задача не найдена")
		return
	}

	newDone := !t.Done
	if err := b.store.SetTodoDone(ctx, user.ID, todoID, newDone); err != nil {
		slog.Error("toggle todo", "err", err)
		b.answerCallback(cbID, "Ошибка сохранения")
		return
	}

	// Синхронизация local-first: ошибка внешнего сервиса не мешает локально.
	syncFail := ""
	if t.RemoteID != nil && t.Provider != nil && todoistToken(user) != "" {
		if p, ok := b.apps.Get(*t.Provider); ok {
			var syncErr error
			if newDone {
				syncErr = p.CloseTask(ctx, todoistToken(user), *t.RemoteID)
			} else {
				syncErr = p.ReopenTask(ctx, todoistToken(user), *t.RemoteID)
			}
			if syncErr != nil {
				slog.Warn("todo sync", "provider", *t.Provider, "err", syncErr)
				syncFail = "В " + p.Title() + " не обновилось, локально — да"
			}
		}
	}

	if syncFail != "" {
		b.answerCallback(cbID, syncFail)
	} else {
		b.answerCallback(cbID, "")
	}
	b.renderTodosEdit(ctx, chatID, messageID, user)
}

func (b *Bot) addTodoFromText(ctx context.Context, chatID int64, user *storage.User, text string) {
	if runed := []rune(text); len(runed) > maxTodoLen {
		text = string(runed[:maxTodoLen])
	}
	todoID, err := b.store.AddTodo(ctx, user.ID, text)
	if err != nil {
		slog.Error("add todo", "err", err)
		b.reply(ctx, chatID, "Не получилось сохранить 😔 Попробуй ещё раз.")
		return
	}

	// Опциональная синхронизация с Todoist.
	syncNote := ""
	if token := todoistToken(user); token != "" {
		if p, ok := b.apps.Get("todoist"); ok {
			remoteID, err := p.AddTask(ctx, token, text)
			if err != nil {
				slog.Warn("todoist add", "err", err)
				syncNote = "\n\n⚠️ В Todoist добавить не удалось: " + err.Error()
			} else if err := b.store.SetTodoRemote(ctx, user.ID, todoID, p.Name(), remoteID); err != nil {
				slog.Warn("todo remote id", "err", err)
			}
		}
	}

	if err := b.store.SetState(ctx, user.ID, storage.StateNone); err != nil {
		slog.Error("set state", "err", err)
	}

	if syncNote != "" {
		b.reply(ctx, chatID, "Добавила ✅"+syncNote)
	}
	b.sendTodosList(ctx, chatID, user)
}

func (b *Bot) clearDoneTodos(ctx context.Context, cbID string, chatID int64, messageID int, user *storage.User) {
	done, err := b.store.ClearDone(ctx, user.ID)
	if err != nil {
		slog.Error("clear done", "err", err)
		b.answerCallback(cbID, "Ошибка")
		return
	}

	// Удаляем выполненные и во внешнем сервисе (по возможности).
	if token := todoistToken(user); token != "" {
		if p, ok := b.apps.Get("todoist"); ok {
			for _, t := range done {
				if t.RemoteID == nil || t.Provider == nil || *t.Provider != p.Name() {
					continue
				}
				if err := p.DeleteTask(ctx, token, *t.RemoteID); err != nil {
					slog.Warn("todoist delete", "err", err)
				}
			}
		}
	}

	b.answerCallback(cbID, "Убрала выполненные 🧹")
	b.renderTodosEdit(ctx, chatID, messageID, user)
}

func (b *Bot) renderTodosEdit(ctx context.Context, chatID int64, messageID int, user *storage.User) {
	items, err := b.store.Todos(ctx, user.ID, maxTodosShown)
	if err != nil {
		slog.Error("todos list", "err", err)
		return
	}
	b.editWithMarkup(ctx, chatID, messageID, todosText(items), todosMarkup(items))
}

// truncateRunes обрезает строку по рунам (эмодзи не ломаем).
func truncateRunes(s string, n int) string {
	if runed := []rune(s); len(runed) > n {
		return strings.TrimSpace(string(runed[:n])) + "…"
	}
	return s
}
