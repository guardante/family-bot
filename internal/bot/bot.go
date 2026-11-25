// Package bot: обработка сообщений Telegram, онбординг, чат с LLM, меню и дела.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/config"
	"nadia_bot/internal/glm"
	"nadia_bot/internal/integrations"
	"nadia_bot/internal/storage"
	"nadia_bot/internal/weather"
)

const (
	maxMessageRunes = 4000 // лимит Telegram — 4096, берём с запасом
	keepMessages    = 40   // сколько сообщений диалога храним в БД на пользователя
)

type Bot struct {
	cfg     *config.Config
	api     *tgbotapi.BotAPI
	store   *storage.Storage
	llm     *glm.Client
	weather *weather.Client
	apps    *integrations.Registry
	http    *http.Client // скачивание фото из Telegram
}

func New(cfg *config.Config, api *tgbotapi.BotAPI, store *storage.Storage,
	llm *glm.Client, apps *integrations.Registry) *Bot {
	return &Bot{
		cfg:     cfg,
		api:     api,
		store:   store,
		llm:     llm,
		weather: weather.New(),
		apps:    apps,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (b *Bot) SetCommands(ctx context.Context) error {
	_, err := b.api.Request(tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "todos", Description: "Список дел"},
		tgbotapi.BotCommand{Command: "weather", Description: "Погода сейчас"},
		tgbotapi.BotCommand{Command: "city", Description: "Сменить город"},
		tgbotapi.BotCommand{Command: "time", Description: "Время утренней погоды"},
		tgbotapi.BotCommand{Command: "forget", Description: "Забыть наш разговор"},
		tgbotapi.BotCommand{Command: "settings", Description: "Настройки"},
		tgbotapi.BotCommand{Command: "help", Description: "Что я умею"},
	))
	return err
}

func (b *Bot) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
	if upd.CallbackQuery != nil {
		b.handleCallback(ctx, upd.CallbackQuery)
		return
	}
	if upd.Message == nil || upd.Message.From == nil {
		return
	}
	msg := upd.Message

	if !b.cfg.IsAllowed(msg.From.ID) {
		slog.Info("чужой, молча игнорирую", "id", msg.From.ID, "username", msg.From.UserName)
		return
	}

	user, err := b.store.EnsureUser(ctx, msg.From.ID, msg.From.UserName, msg.From.FirstName)
	if err != nil {
		slog.Error("не удалось создать пользователя", "err", err)
		return
	}

	// Фото (и картинки документом) — распознавание текста.
	if msg.Photo != nil || isImageDocument(msg.Document) {
		b.handlePhoto(ctx, msg)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		b.reply(ctx, msg.Chat.ID, "Я понимаю текст и фото 🙂")
		return
	}

	if msg.IsCommand() {
		b.handleCommand(ctx, msg, user)
		return
	}

	// Кнопки постоянного меню работают в любом состоянии.
	if b.handleMenuButton(ctx, msg.Chat.ID, user, text) {
		return
	}

	switch user.State {
	case storage.StateAwaitingCity:
		b.handleCityInput(ctx, msg.Chat.ID, user, text)
	case storage.StateAwaitingTime:
		b.handleTimeInput(ctx, msg.Chat.ID, user, text)
	case storage.StateAwaitingTodo:
		b.addTodoFromText(ctx, msg.Chat.ID, user, text)
	case storage.StateAwaitingTodoist:
		b.connectTodoist(ctx, msg.Chat.ID, user, text)
	default:
		b.handleChat(ctx, msg.Chat.ID, user, text)
	}
}

func isImageDocument(doc *tgbotapi.Document) bool {
	return doc != nil && strings.HasPrefix(doc.MimeType, "image/")
}

// handleMenuButton обрабатывает постоянную клавиатуру.
func (b *Bot) handleMenuButton(ctx context.Context, chatID int64, user *storage.User, text string) bool {
	switch text {
	case "🌤 Погода":
		b.cmdWeather(ctx, chatID, user)
	case "📝 Дела":
		b.cmdTodos(ctx, chatID, user)
	case "⚙️ Настройки":
		b.cmdSettings(ctx, chatID, user)
	default:
		return false
	}
	return true
}

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message, user *storage.User) {
	chatID := msg.Chat.ID

	switch msg.Command() {
	case "start":
		b.cmdStart(ctx, chatID, user)
	case "help":
		b.reply(ctx, chatID, "Вот что я умею:\n"+
			"• /weather — погода на сегодня\n"+
			"• /todos — список дел (и фото с текстом тоже можно!)\n"+
			"• /city — сменить город\n"+
			"• /time — во сколько присылать утреннюю погоду\n"+
			"• /settings — настройки и интеграции\n"+
			"• /forget — забыть наш разговор\n"+
			"• /cancel — выйти из текущего вопроса\n\n"+
			"Пришли фото с текстом — распознаю. Просто пиши мне — отвечу на вопросы 🙂")

	case "city":
		b.cmdCity(ctx, chatID, user)
	case "time":
		b.cmdTime(ctx, chatID, user)
	case "weather":
		b.cmdWeather(ctx, chatID, user)
	case "todos":
		b.cmdTodos(ctx, chatID, user)
	case "settings":
		b.cmdSettings(ctx, chatID, user)
	case "forget":
		if err := b.store.ClearMessages(ctx, user.ID); err != nil {
			slog.Error("clear messages", "err", err)
			return
		}
		b.reply(ctx, chatID, "Ок, забыли наш разговор 🤝 Начнём с чистого листа.")
	case "cancel":
		if err := b.store.SetState(ctx, user.ID, storage.StateNone); err != nil {
			slog.Error("set state", "err", err)
			return
		}
		b.reply(ctx, chatID, "Ок, отменила ✋")
	default:
		b.reply(ctx, chatID, "Не знаю такую команду 🤷‍♀️ Посмотри /help")
	}
}

func (b *Bot) cmdStart(ctx context.Context, chatID int64, user *storage.User) {
	if user.HasCity() {
		b.sendWithMenu(ctx, chatID, fmt.Sprintf("С возвращением, %s! 👋 Всё на месте: город %s, погода приходит в %s.", firstName(user), *user.CityName, user.DailyTime))
		return
	}
	b.sendWithMenu(ctx, chatID, fmt.Sprintf("Привет, %s! 👋 Я Надя — твой персональный бот.\nБуду каждое утро присылать погоду, вести список дел и отвечать на вопросы.", firstName(user)))
	b.askCity(ctx, chatID, user.ID)
}

func (b *Bot) cmdCity(ctx context.Context, chatID int64, user *storage.User) {
	b.askCity(ctx, chatID, user.ID)
}

func (b *Bot) cmdTime(ctx context.Context, chatID int64, user *storage.User) {
	if !user.HasCity() {
		b.askCity(ctx, chatID, user.ID)
		return
	}
	if err := b.store.SetState(ctx, user.ID, storage.StateAwaitingTime); err != nil {
		slog.Error("set state", "err", err)
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf(
		"Сейчас присылаю погоду в %s ⏰\nВо сколько присылать? Напиши время в формате ЧЧ:ММ, например 08:00.",
		user.DailyTime))
}

func (b *Bot) cmdWeather(ctx context.Context, chatID int64, user *storage.User) {
	if !user.HasCity() {
		b.askCity(ctx, chatID, user.ID)
		return
	}
	b.sendWeather(ctx, chatID, user, true)
}

func (b *Bot) cmdTodos(ctx context.Context, chatID int64, user *storage.User) {
	b.sendTodosList(ctx, chatID, user)
}

func (b *Bot) cmdSettings(ctx context.Context, chatID int64, user *storage.User) {
	markup := b.settingsMarkup(user)
	msg := tgbotapi.NewMessage(chatID, b.settingsText(user))
	msg.ReplyMarkup = &markup
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("send settings", "err", err)
	}
}

func (b *Bot) askCity(ctx context.Context, chatID int64, userID int64) {
	if err := b.store.SetState(ctx, userID, storage.StateAwaitingCity); err != nil {
		slog.Error("set state", "err", err)
		return
	}
	b.reply(ctx, chatID, "Напиши свой город 🏙 Например: Казань")
}

func (b *Bot) handleCityInput(ctx context.Context, chatID int64, user *storage.User, text string) {
	place, err := b.weather.Geocode(ctx, text)
	if errors.Is(err, weather.ErrNotFound) {
		b.reply(ctx, chatID, "Не нашла такой город 🤔 Попробуй написать иначе, например: Нижний Новгород")
		return
	}
	if err != nil {
		slog.Error("geocode", "err", err)
		b.reply(ctx, chatID, "Что-то сломалось у сервиса геокодинга 😔 Попробуй чуть позже.")
		return
	}

	// Прогноз уйдёт пользователю прямо сейчас (ниже), поэтому сегодняшний
	// утренний тик пропускаем — иначе он придёт вторым в течение 30 секунд.
	today := ""
	if loc, err := time.LoadLocation(place.Timezone); err == nil {
		today = time.Now().In(loc).Format("2006-01-02")
	}

	if err := b.store.UpdateCity(ctx, user.ID, place.Name, place.Latitude, place.Longitude, place.Timezone, today); err != nil {
		slog.Error("update city", "err", err)
		return
	}

	updated := *user
	cityName := place.Name
	cityLat, cityLon := place.Latitude, place.Longitude
	updated.CityName = &cityName
	updated.CityLat = &cityLat
	updated.CityLon = &cityLon
	updated.Timezone = place.Timezone

	b.reply(ctx, chatID, fmt.Sprintf(
		"Запомнила: %s ✅\nБуду присылать погоду каждый день в %s (поменять: /time).\n\nА вот что там сегодня:",
		place.DisplayName(), updated.DailyTime))
	b.sendWeather(ctx, chatID, &updated, false)
}

func (b *Bot) handleTimeInput(ctx context.Context, chatID int64, user *storage.User, text string) {
	t, err := time.Parse("15:04", text)
	if err != nil {
		b.reply(ctx, chatID, "Не поняла время 🕐 Напиши в формате ЧЧ:ММ, например 08:00")
		return
	}
	hhmm := t.Format("15:04")
	if err := b.store.UpdateDailyTime(ctx, user.ID, hhmm); err != nil {
		slog.Error("update daily time", "err", err)
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("Готово! Буду писать тебе в %s ⏰", hhmm))
}

// handleChat — свободный вопрос пользователя через LLM с сохранением контекста.
func (b *Bot) handleChat(ctx context.Context, chatID int64, user *storage.User, text string) {
	if len(text) > 3500 {
		text = text[:3500]
	}

	if err := b.store.AddMessage(ctx, user.ID, "user", text); err != nil {
		slog.Error("save user message", "err", err)
	}

	if _, err := b.api.Request(tgbotapi.NewChatAction(chatID, "typing")); err != nil {
		slog.Warn("chat action", "err", err)
	}

	history, err := b.store.RecentMessages(ctx, user.ID, b.cfg.HistoryLimit)
	if err != nil {
		slog.Error("load history", "err", err)
		history = nil
	}

	messages := make([]glm.Message, 0, len(history)+1)
	messages = append(messages, glm.Message{Role: "system", Content: b.systemPrompt(user)})
	for _, m := range history {
		messages = append(messages, glm.Message{Role: m.Role, Content: m.Content})
	}

	answer, err := b.llm.Chat(ctx, messages)
	if err != nil {
		slog.Error("llm chat", "err", err)
		b.reply(ctx, chatID, "Хм, не получилось подумать 😔 Попробуй ещё раз чуть позже.")
		return
	}

	if err := b.store.AddMessage(ctx, user.ID, "assistant", answer); err != nil {
		slog.Error("save assistant message", "err", err)
	}
	if err := b.store.TrimMessages(ctx, user.ID, keepMessages); err != nil {
		slog.Error("trim messages", "err", err)
	}

	b.reply(ctx, chatID, answer)
}

func (b *Bot) systemPrompt(u *storage.User) string {
	city := "не указан"
	if u.HasCity() {
		city = *u.CityName
	}
	nowStr := time.Now().Format("02.01.2006 15:04")
	if loc, err := time.LoadLocation(u.Timezone); err == nil {
		nowStr = time.Now().In(loc).Format("02.01.2006 15:04")
	}

	return fmt.Sprintf(`Ты — Надя, дружелюбная ассистентка в Telegram-боте. Общайся коротко, тепло и по делу, как хороший друг: без канцелярита и длинных лекций. Отвечай на языке пользователя (обычно русский). Не используй markdown-разметку (звёздочки, решётки, код-блоки) — пиши просто текст. Если спрашивают про погоду, предложи команду /weather и не выдумывай погоду сам.

Данные о пользователе:
— Город: %s
— Часовой пояс: %s
— Сейчас у пользователя: %s`, city, u.Timezone, nowStr)
}

// sendWeather отправляет прогноз на сегодня. reportErrors — писать ли пользователю об ошибке.
func (b *Bot) sendWeather(ctx context.Context, chatID int64, u *storage.User, reportErrors bool) {
	if !u.HasCity() {
		return
	}
	forecast, err := b.weather.DailyForecast(ctx, *u.CityLat, *u.CityLon, u.Timezone)
	if err != nil {
		slog.Error("forecast", "err", err)
		if reportErrors {
			b.reply(ctx, chatID, "Не смогла узнать погоду 😔 Попробуй позже.")
		}
		return
	}
	forecast.City = *u.CityName
	b.reply(ctx, chatID, weather.FormatDaily(forecast))
}

// ---- колбэки от инлайн-кнопок ----

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil {
		return
	}
	if !b.cfg.IsAllowed(cb.From.ID) {
		b.answerCallback(cb.ID, "Нет доступа")
		return
	}

	user, err := b.store.EnsureUser(ctx, cb.From.ID, cb.From.UserName, cb.From.FirstName)
	if err != nil {
		slog.Error("не удалось создать пользователя", "err", err)
		return
	}

	data := cb.Data
	chatID := cb.Message.Chat.ID
	messageID := cb.Message.MessageID

	switch {
	case data == "td_add":
		if err := b.store.SetState(ctx, user.ID, storage.StateAwaitingTodo); err != nil {
			slog.Error("set state", "err", err)
			return
		}
		b.answerCallback(cb.ID, "Напиши задачу следующим сообщением ✍️")
	case strings.HasPrefix(data, "td_toggle:"):
		id, err := strconv.ParseInt(strings.TrimPrefix(data, "td_toggle:"), 10, 64)
		if err != nil {
			b.answerCallback(cb.ID, "Хм, не поняла")
			return
		}
		b.toggleTodo(ctx, cb.ID, chatID, messageID, user, id)
	case data == "td_clear":
		b.clearDoneTodos(ctx, cb.ID, chatID, messageID, user)
	case strings.HasPrefix(data, "st_"):
		b.handleSettingsCallback(ctx, cb, user, data)
	default:
		b.answerCallback(cb.ID, "")
	}
}

// ---- helpers ----

func (b *Bot) answerCallback(cbID, text string) {
	if _, err := b.api.Request(tgbotapi.NewCallback(cbID, text)); err != nil {
		slog.Warn("answer callback", "err", err)
	}
}

func (b *Bot) editWithMarkup(ctx context.Context, chatID int64, messageID int, text string, markup tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, truncateRunes(text, maxMessageRunes))
	edit.ReplyMarkup = &markup
	if _, err := b.api.Request(edit); err != nil {
		slog.Warn("edit message", "err", err)
	}
}

func (b *Bot) sendWithMenu(ctx context.Context, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, truncateRunes(text, maxMessageRunes))
	msg.ReplyMarkup = mainMenuKeyboard()
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("send message", "chat_id", chatID, "err", err)
	}
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, truncateRunes(text, maxMessageRunes))
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("send message", "chat_id", chatID, "err", err)
	}
}

// mainMenuKeyboard — постоянная клавиатура внизу экрана.
func mainMenuKeyboard() *tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🌤 Погода"),
			tgbotapi.NewKeyboardButton("📝 Дела"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("⚙️ Настройки"),
		),
	)
	kb.ResizeKeyboard = true
	return &kb
}

func firstName(u *storage.User) string {
	if u.FirstName != "" {
		return u.FirstName
	}
	return "друг"
}
