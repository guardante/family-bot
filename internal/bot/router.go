package bot

// Роутер намерений: любой текст или транскрипт голосового модель
// превращает в одно из запрограммированных действий (или просто отвечает).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nadia_bot/internal/glm"
	"nadia_bot/internal/storage"
	"nadia_bot/internal/weather"
)

var weekdaysRU = [...]string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}

const routerPromptFmt = `Ты — Надя, ассистентка в Telegram-боте. Твоя задача — понять намерение пользователя и выбрать действие. Верни СТРОГО один JSON-объект, без markdown и пояснений.

Действия:
{"action":"add","events":[{"title":"...","date":"YYYY-MM-DD","time":"HH:MM"}],"todos":["текст задачи"]} — записать. events — события с датой и временем (встречи, напоминания вида «напомни завтра в 7»); todos — задачи без даты и времени (списки, «не забыть купить»). Если у события не названо время — ставь 09:00. Заполняй только подходящие поля; оба массива могут быть в одном ответе.
{"action":"show_weather"} — спрашивают про погоду
{"action":"show_events"} — показать записанные события
{"action":"show_todos"} — показать список дел
{"action":"set_city","city":"..."} — пользователь сменил или назвал свой город
{"action":"set_daily_time","time":"ЧЧ:ММ"} — изменить время утреннего сообщения
{"action":"delete_event","title":"часть названия"} — удалить событие
{"action":"chat","reply":"..."} — всё остальное: вопросы, болтовня; reply — твой короткий живой ответ на языке пользователя. Погоду внутри chat не выдумывай — предлагай показать.

Контекст:
— Сегодня: %s (%s), время %s, часовой пояс пользователя: %s
— Город пользователя: %s
— Относительные даты («завтра», «в пятницу», «через неделю») считай от сегодняшнего дня.`

type routerAction struct {
	Action string `json:"action"`
	Reply  string `json:"reply"`
	City   string `json:"city"`
	Time   string `json:"time"`
	Title  string `json:"title"`
	Events []struct {
		Title string `json:"title"`
		Date  string `json:"date"`
		Time  string `json:"time"`
	} `json:"events"`
	Todos []string `json:"todos"`
}

// routeMessage спрашивает у модели намерение. history — последние сообщения диалога.
func (b *Bot) routeMessage(ctx context.Context, user *storage.User, text string, history []storage.Message) (*routerAction, error) {
	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	city := "не задан"
	if user.HasCity() {
		city = *user.CityName
	}

	system := fmt.Sprintf(routerPromptFmt,
		now.Format("02.01.2006"), weekdaysRU[int(now.Weekday())],
		now.Format("15:04"), user.Timezone, city)

	messages := make([]glm.Message, 0, len(history)+2)
	messages = append(messages, glm.Message{Role: "system", Content: system})
	for _, m := range history {
		messages = append(messages, glm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, glm.Message{Role: "user", Content: text})

	answer, err := b.llm.ChatWithTemperature(ctx, messages, 0.2, 700)
	if err != nil {
		return nil, err
	}

	action, err := parseRouterAction(answer)
	if err != nil {
		return nil, fmt.Errorf("ответ роутера не разобрать: %w", err)
	}
	return action, nil
}

func parseRouterAction(answer string) (*routerAction, error) {
	s := strings.TrimSpace(answer)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("нет JSON в ответе")
	}

	var action routerAction
	if err := json.Unmarshal([]byte(s[start:end+1]), &action); err != nil {
		return nil, err
	}
	action.Reply = strings.TrimSpace(action.Reply)
	return &action, nil
}

// handleMessageFree — свободное сообщение (текст или транскрипт голосового).
func (b *Bot) handleMessageFree(ctx context.Context, chatID int64, user *storage.User, text string, transcript bool) {
	if runed := []rune(text); len(runed) > 3500 {
		text = string(runed[:3500])
	}

	if transcript {
		text = strings.TrimSpace(text)
	}
	if err := b.store.AddMessage(ctx, user.ID, "user", text); err != nil {
		slog.Error("save user message", "err", err)
	}

	if _, err := b.api.Request(tgbotapiChatAction(chatID)); err != nil {
		slog.Warn("chat action", "err", err)
	}

	history, err := b.store.RecentMessages(ctx, user.ID, b.cfg.HistoryLimit)
	if err != nil {
		slog.Error("load history", "err", err)
		history = nil
	}

	prefix := ""
	if transcript {
		prefix = "🎤 Услышала: «" + text + "»\n\n"
		b.reply(ctx, chatID, prefix)
	}

	action, err := b.routeMessage(ctx, user, text, history)
	if err != nil {
		slog.Warn("router", "err", err)
		b.chatFallback(ctx, chatID, user, text, history)
		return
	}

	if action.Action == "chat" {
		reply := action.Reply
		if reply == "" {
			b.chatFallback(ctx, chatID, user, text, history)
			return
		}
		if err := b.store.AddMessage(ctx, user.ID, "assistant", reply); err != nil {
			slog.Error("save assistant message", "err", err)
		}
		b.trimHistory(ctx, user.ID)
		b.reply(ctx, chatID, reply)
		return
	}

	note := b.execAction(ctx, chatID, user, action)
	if note == "" {
		note = "[выполнила: " + action.Action + "]"
	}
	if err := b.store.AddMessage(ctx, user.ID, "assistant", note); err != nil {
		slog.Error("save action note", "err", err)
	}
	b.trimHistory(ctx, user.ID)
}

// execAction выполняет распознанное действие и возвращает заметку для истории.
func (b *Bot) execAction(ctx context.Context, chatID int64, user *storage.User, action *routerAction) string {
	switch action.Action {
	case "add":
		return b.execAdd(ctx, chatID, user, action)
	case "show_weather":
		b.cmdWeather(ctx, chatID, user)
		return "[показала погоду]"
	case "show_events":
		b.cmdEvents(ctx, chatID, user)
		return "[показала события]"
	case "show_todos":
		b.sendTodosList(ctx, chatID, user)
		return "[показала дела]"
	case "set_city":
		return b.execSetCity(ctx, chatID, user, action.City)
	case "set_daily_time":
		t, err := time.Parse("15:04", strings.TrimSpace(action.Time))
		if err != nil {
			b.reply(ctx, chatID, "Не поняла время 🕐 Напиши в формате ЧЧ:ММ, например 08:00")
			return "[не смогла поменять время]"
		}
		hhmm := t.Format("15:04")
		if err := b.store.UpdateDailyTime(ctx, user.ID, hhmm); err != nil {
			slog.Error("update daily time", "err", err)
			return ""
		}
		b.reply(ctx, chatID, fmt.Sprintf("Готово! Буду писать тебе в %s ⏰", hhmm))
		return "[время утреннего сообщения: " + hhmm + "]"
	case "delete_event":
		return b.execDeleteEvent(ctx, chatID, user, action.Title)
	default:
		return ""
	}
}

func (b *Bot) execAdd(ctx context.Context, chatID int64, user *storage.User, action *routerAction) string {
	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)

	var lines []string
	var notes []string

	for _, ev := range action.Events {
		title := strings.TrimSpace(ev.Title)
		if title == "" {
			continue
		}
		// Дублируем подстраховку модели на случай, если она вернула пустое время.
		if ev.Time == "" {
			ev.Time = defaultEventTime
		}
		at, err := lenientDateTime(ev.Date, ev.Time, loc, now)
		if err != nil {
			slog.Warn("router event time", "date", ev.Date, "time", ev.Time, "err", err)
			continue
		}
		remind := 30
		id, err := b.store.AddEvent(ctx, user.ID, title, storage.NormalizeEventAt(at), &remind)
		if err != nil {
			slog.Error("router add event", "err", err)
			continue
		}
		if err := b.caldavPush(ctx, user, id, title, at); err != nil {
			slog.Warn("caldav push", "err", err)
			lines = append(lines, fmt.Sprintf("⚠️ «%s» — в календарь Apple не добавилось", title))
		}
		lines = append(lines, fmt.Sprintf("📅 %s — %s в %s", title, at.Format("02.01"), at.Format("15:04")))
		notes = append(notes, fmt.Sprintf("%s (%s %s)", title, ev.Date, ev.Time))
	}

	for _, todo := range action.Todos {
		todo = strings.TrimSpace(todo)
		if todo == "" {
			continue
		}
		if runed := []rune(todo); len(runed) > 200 {
			todo = string(runed[:200])
		}
		todoID, err := b.store.AddTodo(ctx, user.ID, todo)
		if err != nil {
			slog.Error("router add todo", "err", err)
			continue
		}
		if token := todoistToken(user); token != "" {
			if p, ok := b.apps.Get("todoist"); ok {
				if remoteID, err := p.AddTask(ctx, token, todo); err == nil {
					_ = b.store.SetTodoRemote(ctx, user.ID, todoID, p.Name(), remoteID)
				}
			}
		}
		lines = append(lines, "✅ "+todo)
		notes = append(notes, "задача: "+todo)
	}

	if len(lines) == 0 {
		b.reply(ctx, chatID, "Не поняла, что записать 🤔 Попробуй уточнить дату и время.")
		return "[ничего не записала]"
	}

	b.reply(ctx, chatID, "Готово!\n"+strings.Join(lines, "\n"))
	return "Добавила: " + strings.Join(notes, "; ")
}

func (b *Bot) execSetCity(ctx context.Context, chatID int64, user *storage.User, city string) string {
	city = strings.TrimSpace(city)
	if city == "" {
		b.reply(ctx, chatID, "Название города не поняла 🤔 Напиши ещё раз, например: Москва")
		return "[не поняла город]"
	}
	place, err := b.weather.Geocode(ctx, city)
	if err != nil {
		if err == weather.ErrNotFound {
			b.reply(ctx, chatID, "Не нашла такой город 🤔 Попробуй написать иначе.")
			return "[город не найден]"
		}
		slog.Error("geocode", "err", err)
		b.reply(ctx, chatID, "Сервис геокодинга не отвечает 😔 Попробуй позже.")
		return "[ошибка геокодинга]"
	}
	if err := b.store.UpdateCity(ctx, user.ID, place.Name, place.Latitude, place.Longitude, place.Timezone, ""); err != nil {
		slog.Error("update city", "err", err)
		return ""
	}
	b.reply(ctx, chatID, fmt.Sprintf("Сменила город: %s ✅", place.DisplayName()))
	return "[город: " + place.Name + "]"
}

func (b *Bot) execDeleteEvent(ctx context.Context, chatID int64, user *storage.User, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		b.reply(ctx, chatID, "Скажи, какое событие удалить — хотя бы часть названия 🙏")
		return "[не поняла, что удалять]"
	}
	events, err := b.upcomingEvents(ctx, user, 50)
	if err != nil {
		slog.Error("events list", "err", err)
		b.reply(ctx, chatID, "Не получилось загрузить события 😔")
		return ""
	}
	needle := strings.ToLower(title)
	for _, ev := range events {
		if !strings.Contains(strings.ToLower(ev.Title), needle) {
			continue
		}
		deleted, err := b.store.DeleteEvent(ctx, user.ID, ev.ID)
		if err != nil {
			slog.Error("delete event", "err", err)
			b.reply(ctx, chatID, "Не получилось удалить 😔")
			return ""
		}
		if deleted != nil && deleted.CaldavUID != nil {
			if login, pass, ok := caldavCreds(user); ok {
				if err := b.caldav.DeleteEvent(ctx, login, pass, b.cfg.CaldavURL, "Надя", *deleted.CaldavUID); err != nil {
					slog.Warn("caldav delete", "err", err)
				}
			}
		}
		b.reply(ctx, chatID, "Удалила: "+deleted.Title)
		return "[удалила событие: " + deleted.Title + "]"
	}
	b.reply(ctx, chatID, fmt.Sprintf("Не нашла среди событий ничего про «%s» 🤔 Посмотри список: /events", title))
	return "[событие не найдено]"
}

// chatFallback — старый путь: обычный разговор без роутера (если роутер сломался).
func (b *Bot) chatFallback(ctx context.Context, chatID int64, user *storage.User, text string, history []storage.Message) {
	messages := make([]glm.Message, 0, len(history)+2)
	messages = append(messages, glm.Message{Role: "system", Content: b.systemPrompt(user)})
	for _, m := range history {
		messages = append(messages, glm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, glm.Message{Role: "user", Content: text})

	answer, err := b.llm.Chat(ctx, messages)
	if err != nil {
		slog.Error("llm chat", "err", err)
		b.reply(ctx, chatID, "Хм, не получилось подумать 😔 Попробуй ещё раз чуть позже.")
		return
	}
	if err := b.store.AddMessage(ctx, user.ID, "assistant", answer); err != nil {
		slog.Error("save assistant message", "err", err)
	}
	b.trimHistory(ctx, user.ID)
	b.reply(ctx, chatID, answer)
}
