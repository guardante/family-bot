package bot

// Роутер намерений через нативный function calling: модель получает
// инструменты («скилы») и либо вызывает их, либо просто отвечает текстом.
// Соврать про «выполнила» структурно нельзя — выполнение только через вызовы.

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

// routerAction — нормализованное действие из вызова инструмента.
type routerAction struct {
	Action  string `json:"action"`
	Reply   string `json:"reply"`
	City    string `json:"city"`
	Time    string `json:"time"`
	Date    string `json:"date"`
	Title   string `json:"title"`
	RawText string `json:"-"` // исходное сообщение (для извлечения города в коде)
	Events  []struct {
		Title string `json:"title"`
		Date  string `json:"date"`
		Time  string `json:"time"`
	} `json:"events"`
	Todos []string `json:"todos"`
}

const systemPromptFmt = `Ты — Надя, дружелюбная ассистентка в Telegram. Отвечай коротко, тепло и живо, на языке пользователя.

Сегодня %s (%s), время %s, часовой пояс пользователя: %s. Город: %s.
События пользователя:
%s
Открытые задачи:
%s

Правила:
— Выполняй ТОЛЬКО ПОСЛЕДНЕЕ сообщение пользователя. История — это контекст: всё, что было раньше, уже обработано (заметки ассистентки в [скобках] — выполненные действия). Не повторяй, не доделывай и не «исправляй» старые просьбы.
— Изменения календаря и задач — ТОЛЬКО через вызовы инструментов. Текстовый ответ никогда ничего не выполняет, поэтому никогда не утверждай в тексте, что ты что-то создала/перенесла/удалила.
— Запрос погоды = инструмент show_weather. Параметр city — это ПРОСТО КОПИЯ города из текста сообщения (как.search-запрос): «погоду в Тюмени» → city=Тюмень; «можно погоду в Сочи?» → city=Сочи; города нет → city="". city НИКАК не связан с сохранённым городом и настройками — просто копируй то, что написано.
— Если пользователь просит ИЗМЕНИТЬ сохранённый город («я переехал», «смени город») — ответь, что город меняется командой /city (инструментом это не делается).
— Относительные даты («завтра», «в пятницу») считай от сегодняшнего дня.
— Если пользователь называет только название события без глагола и без даты — уточни одним вопросом.
— Если пользователь называет только название события без глагола и без даты — уточни одним вопросом.`

// routerTools — «скилы» Надежды для function calling.
func routerTools() []glm.Tool {
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		}
	}
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	eventArr := func() map[string]any {
		return map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": str("название события"),
					"date":  str("дата YYYY-MM-DD"),
					"time":  str("время HH:MM; если не названо — 09:00"),
				},
				"required": []string{"title", "date"},
			},
		}
	}

	return []glm.Tool{
		{Type: "function", Function: glm.ToolFunction{
			Name:        "add",
			Description: "Записать события в календарь и/или задачи в список. События — с датой и временем; задачи — без даты («купить хлеб»).",
			Parameters: obj(map[string]any{
				"events": eventArr(),
				"todos": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			}),
		}},
		{Type: "function", Function: glm.ToolFunction{
			Name:        "move_event",
			Description: "Перенести существующее событие на другую дату и/или время.",
			Parameters: obj(map[string]any{
				"title": str("часть названия события"),
				"date":  str("новая дата YYYY-MM-DD"),
				"time":  str("новое время HH:MM; опционально — иначе сохраняется прежнее"),
			}),
		}},
		{Type: "function", Function: glm.ToolFunction{
			Name:        "delete_event",
			Description: "Удалить событие.",
			Parameters: obj(map[string]any{
				"title": str("часть названия события"),
			}, "title"),
		}},
		{Type: "function", Function: glm.ToolFunction{
			Name:        "show_weather",
			Description: "Показать погоду. city ОБЯЗАТЕЛЕН: город из текста запроса пользователя («погоду в Тюмени» → city=Тюмень; «Хочу погоду в Сочи» → city=Сочи). Если города в тексте нет — пустая строка (погода для сохранённого города).",
			Parameters: obj(map[string]any{
				"city": str("город из текста запроса; если города в запросе нет — пустая строка"),
			}, "city"),
		}},
		{Type: "function", Function: glm.ToolFunction{
			Name:        "show_events",
			Description: "Показать записанные события.",
			Parameters:  obj(map[string]any{}),
		}},
		{Type: "function", Function: glm.ToolFunction{
			Name:        "show_todos",
			Description: "Показать список задач.",
			Parameters:  obj(map[string]any{}),
		}},
		{Type: "function", Function: glm.ToolFunction{
			Name:        "set_daily_time",
			Description: "Изменить время ежедневного сообщения с погодой.",
			Parameters: obj(map[string]any{
				"time": str("время HH:MM"),
			}, "time"),
		}},
	}
}

// routeMessage спрашивает модель и возвращает действия + текстовый ответ.
func (b *Bot) routeMessage(ctx context.Context, user *storage.User, text string, history []storage.Message) ([]*routerAction, string, error) {
	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	city := "не задан"
	if user.HasCity() {
		city = *user.CityName
	}

	system := fmt.Sprintf(systemPromptFmt,
		now.Format("02.01.2006"), weekdaysRU[int(now.Weekday())],
		now.Format("15:04"), user.Timezone, city,
		b.contextEvents(ctx, user, loc),
		b.contextTodos(ctx, user))

	messages := make([]glm.Message, 0, len(history)+2)
	messages = append(messages, glm.Message{Role: "system", Content: system})
	for _, m := range history {
		messages = append(messages, glm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, glm.Message{Role: "user", Content: text})

	content, calls, err := b.llm.ToolsChat(ctx, messages, routerTools())
	if err != nil {
		return nil, "", err
	}
	slog.Info("router: ответ модели", "calls", len(calls), "content", truncateString(content, 150))

	var actions []*routerAction
	for _, call := range calls {
		a, err := toolToAction(call.Function.Name, call.Function.Arguments)
		if err != nil {
			slog.Warn("tool call", "name", call.Function.Name, "err", err)
			continue
		}
		a.RawText = text
		actions = append(actions, a)
	}
	return actions, strings.TrimSpace(content), nil
}

func strArg(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func truncateString(s string, n int) string {
	if runed := []rune(s); len(runed) > n {
		return string(runed[:n]) + "…"
	}
	return s
}

// toolToAction превращает вызов инструмента в действие для execAction.
func toolToAction(name, argsJSON string) (*routerAction, error) {
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return nil, err
		}
	}

	a := &routerAction{Action: name}
	switch name {
	case "add":
		if evs, ok := args["events"].([]any); ok {
			for _, e := range evs {
				m, ok := e.(map[string]any)
				if !ok {
					continue
				}
				a.Events = append(a.Events, struct {
					Title string `json:"title"`
					Date  string `json:"date"`
					Time  string `json:"time"`
				}{
					Title: strArg(m, "title"),
					Date:  strArg(m, "date"),
					Time:  strArg(m, "time"),
				})
			}
		}
		if todos, ok := args["todos"].([]any); ok {
			for _, t := range todos {
				if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
					a.Todos = append(a.Todos, s)
				}
			}
		}
	case "move_event":
		a.Title = strArg(args, "title")
		a.Date = strArg(args, "date")
		a.Time = strArg(args, "time")
	case "delete_event":
		a.Title = strArg(args, "title")
	case "set_city":
		a.City = strArg(args, "city")
	case "set_daily_time":
		a.Time = strArg(args, "time")
	case "show_weather", "show_events", "show_todos":
		// аргументов нет
	default:
		return nil, fmt.Errorf("неизвестный инструмент %q", name)
	}
	return a, nil
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

	if transcript {
		b.reply(ctx, chatID, "🎤 Услышала: «"+text+"»")
	}

	actions, reply, err := b.routeMessage(ctx, user, text, history)
	if err != nil {
		slog.Warn("router", "err", err)
		b.chatFallback(ctx, chatID, user, text, history)
		return
	}

	if len(actions) == 0 {
		// Модель просто отвечает.
		reply = strings.TrimSpace(reply)
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

	var notes []string
	for _, a := range actions {
		if note := b.execAction(ctx, chatID, user, a); note != "" {
			notes = append(notes, note)
		}
	}
	if len(notes) > 0 {
		if err := b.store.AddMessage(ctx, user.ID, "assistant", strings.Join(notes, "; ")); err != nil {
			slog.Error("save action note", "err", err)
		}
		b.trimHistory(ctx, user.ID)
	}
}

// execAction выполняет распознанное действие и возвращает заметку для истории.
func (b *Bot) execAction(ctx context.Context, chatID int64, user *storage.User, action *routerAction) string {
	switch action.Action {
	case "add":
		return b.execAdd(ctx, chatID, user, action)
	case "show_weather":
		b.execShowWeather(ctx, chatID, user, action.City, action.RawText)
		return "[показала погоду]"
	case "show_events":
		b.cmdEvents(ctx, chatID, user)
		return "[показала события]"
	case "show_todos":
		b.sendTodosList(ctx, chatID, user)
		return "[показала дела]"
	case "delete_event":
		return b.execDeleteEvent(ctx, chatID, user, action.Title)
	case "move_event":
		return b.execMoveEvent(ctx, chatID, user, action)
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
	default:
		return ""
	}
}

// execShowWeather — погода: для названного города (разово) или сохранённого.
// city приходит от модели; если пусто — пробуем извлечь город из текста
// отдельным мини-запросом (надёжнее, чем большой роутер), иначе — дефолт.
func (b *Bot) execShowWeather(ctx context.Context, chatID int64, user *storage.User, city, rawText string) {
	city = strings.TrimSpace(city)
	if city == "" && strings.TrimSpace(rawText) != "" {
		if extracted := b.extractCity(ctx, rawText); extracted != "" {
			city = extracted
		}
	}
	if city == "" {
		b.cmdWeather(ctx, chatID, user)
		return
	}

	place, err := b.weather.Geocode(ctx, city)
	if err != nil {
		if err == weather.ErrNotFound {
			b.reply(ctx, chatID, fmt.Sprintf("Не нашла город %q 🤔", city))
			return
		}
		slog.Error("geocode", "err", err)
		b.reply(ctx, chatID, "Сервис геокодинга не отвечает 😔 Попробуй позже.")
		return
	}

	forecast, err := b.weather.DailyForecast(ctx, place.Latitude, place.Longitude, place.Timezone)
	if err != nil {
		slog.Error("forecast", "err", err)
		b.reply(ctx, chatID, "Не смогла узнать погоду 😔 Попробуй позже.")
		return
	}
	forecast.City = place.DisplayName()
	b.reply(ctx, chatID, weather.FormatDaily(forecast)+fmt.Sprintf("\n\n📌 Разовый запрос — сохранённый город не меняла (%s).", cityOr(user)))
}

// extractCity — крошечный выделенный запрос: только «найди город в тексте».
// Такие узкие задачи модель выполняет почти безошибочно.
func (b *Bot) extractCity(ctx context.Context, text string) string {
	answer, err := b.llm.ChatWithTemperature(ctx, []glm.Message{
		{Role: "system", Content: `Из сообщения пользователя извлеки название города, для которого нужна погода. Верни ТОЛЬКО название города (обычное именование, например «Тюмень»), без кавычек и пояснений. Если города в сообщении нет — верни пустую строку.`},
		{Role: "user", Content: text},
	}, 0.0, 50)
	if err != nil {
		slog.Warn("extract city", "err", err)
		return ""
	}
	answer = strings.TrimSpace(answer)
	if answer != "" && answer != `""` && !strings.Contains(answer, "\n") && len([]rune(answer)) < 60 {
		return answer
	}
	return ""
}

func cityOr(u *storage.User) string {
	if u.HasCity() {
		return *u.CityName
	}
	return "не задан"
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
		// Дублируем подстраховку модели на случай пустого времени.
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
			lines = append(lines, fmt.Sprintf("⚠️ «%s» — в календарь Apple не добавилось: %v", title, err))
		} else {
			lines = append(lines, fmt.Sprintf("📅 %s — %s в %s (календарь «%s»)", title, at.Format("02.01"), at.Format("15:04"), b.cfg.CaldavCalendar))
			notes = append(notes, fmt.Sprintf("%s (%s %s)", title, ev.Date, ev.Time))
		}
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

func (b *Bot) execDeleteEvent(ctx context.Context, chatID int64, user *storage.User, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		b.reply(ctx, chatID, "Скажи, какое событие удалить — хотя бы часть названия 🙏")
		return "[не поняла, что удалять]"
	}
	events, err := b.store.EventsAll(ctx, user.ID, 50)
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
				if err := b.caldav.DeleteEvent(ctx, login, pass, b.cfg.CaldavURL, b.cfg.CaldavCalendar, *deleted.CaldavUID); err != nil {
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

// execMoveEvent — «перенеси событие X на завтра / на 17:00».
func (b *Bot) execMoveEvent(ctx context.Context, chatID int64, user *storage.User, action *routerAction) string {
	title := strings.TrimSpace(action.Title)
	if title == "" {
		b.reply(ctx, chatID, "Скажи, какое событие перенести — хотя бы часть названия 🙏")
		return "[не поняла, что переносить]"
	}
	if action.Date == "" && action.Time == "" {
		b.reply(ctx, chatID, "На какую дату или время перенести? Например: «перенеси встречу на завтра» или «…на 17:00»")
		return "[не поняла, куда переносить]"
	}

	events, err := b.store.EventsAll(ctx, user.ID, 50)
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
		oldAt, err := storage.ParseEventAt(ev.EventAt)
		if err != nil {
			continue
		}

		loc, err := time.LoadLocation(user.Timezone)
		if err != nil {
			loc = time.UTC
		}
		now := time.Now().In(loc)
		oldLocal := oldAt.In(loc)

		// Новая дата: названа — берём её; не названа — оставляем прежний день.
		day := oldLocal
		if action.Date != "" {
			day, err = lenientDate(action.Date, loc, now)
			if err != nil {
				b.reply(ctx, chatID, "Не поняла дату 🤔 "+err.Error())
				return "[не поняла дату]"
			}
		}
		// Новое время: названо — берём его; не названо — сохраняем прежнее.
		hour, minute := oldLocal.Hour(), oldLocal.Minute()
		if action.Time != "" {
			hour, minute, err = lenientTime(action.Time)
			if err != nil {
				b.reply(ctx, chatID, "Не поняла время 🤔 "+err.Error())
				return "[не поняла время]"
			}
		}
		newAt := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)

		if err := b.store.UpdateEventAt(ctx, user.ID, ev.ID, storage.NormalizeEventAt(newAt)); err != nil {
			slog.Error("move event", "err", err)
			b.reply(ctx, chatID, "Не получилось перенести 😔")
			return ""
		}
		if ev.CaldavUID != nil {
			if login, pass, ok := caldavCreds(user); ok {
				if err := b.caldav.UpdateEvent(ctx, login, pass, b.cfg.CaldavURL, b.cfg.CaldavCalendar, *ev.CaldavUID, ev.Title, newAt); err != nil {
					slog.Warn("caldav move", "err", err)
					b.reply(ctx, chatID, fmt.Sprintf("Перенесла локально, но в календаре Apple не обновилось: %v", err))
					return "[перенесла локально, caldav ошибка]"
				}
			}
		}
		b.reply(ctx, chatID, fmt.Sprintf("Перенесла «%s»: %s → %s",
			ev.Title,
			oldLocal.Format("02.01 в 15:04"),
			newAt.Format("02.01 в 15:04")))
		return "[перенесла: " + ev.Title + " на " + newAt.Format("02.01 15:04") + "]"
	}

	b.reply(ctx, chatID, fmt.Sprintf("Не нашла среди событий ничего про «%s» 🤔 Посмотри список: /events", title))
	return "[событие не найдено]"
}

// chatFallback — обычный разговор без роутера (если роутер сломался).
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

// contextEvents — фактический список событий для промпта, чтобы модель
// не выдумывала «что у пользователя в календаре».
func (b *Bot) contextEvents(ctx context.Context, user *storage.User, loc *time.Location) string {
	events, err := b.upcomingEvents(ctx, user, 10)
	if err != nil || len(events) == 0 {
		return "— (нет)"
	}
	var lines []string
	for _, ev := range events {
		at, err := storage.ParseEventAt(ev.EventAt)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("— %s в %s: «%s»", at.In(loc).Format("02.01.2006"), at.In(loc).Format("15:04"), ev.Title))
	}
	if len(lines) == 0 {
		return "— (нет)"
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) contextTodos(ctx context.Context, user *storage.User) string {
	todos, err := b.store.Todos(ctx, user.ID, 10)
	if err != nil {
		return "— (нет)"
	}
	var lines []string
	for _, t := range todos {
		if !t.Done {
			lines = append(lines, "— "+t.Text)
		}
	}
	if len(lines) == 0 {
		return "— (нет)"
	}
	return strings.Join(lines, "\n")
}
