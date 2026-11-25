// Package bot: обработка сообщений Telegram, онбординг и чат с LLM.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/config"
	"nadia_bot/internal/glm"
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
}

func New(cfg *config.Config, api *tgbotapi.BotAPI, store *storage.Storage, llm *glm.Client) *Bot {
	return &Bot{
		cfg:     cfg,
		api:     api,
		store:   store,
		llm:     llm,
		weather: weather.New(),
	}
}

func (b *Bot) SetCommands(ctx context.Context) error {
	_, err := b.api.Request(tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "weather", Description: "Погода сейчас"},
		tgbotapi.BotCommand{Command: "city", Description: "Сменить город"},
		tgbotapi.BotCommand{Command: "time", Description: "Время утренней погоды"},
		tgbotapi.BotCommand{Command: "on", Description: "Включить утреннюю погоду"},
		tgbotapi.BotCommand{Command: "off", Description: "Выключить утреннюю погоду"},
		tgbotapi.BotCommand{Command: "forget", Description: "Забыть наш разговор"},
		tgbotapi.BotCommand{Command: "help", Description: "Что я умею"},
	))
	return err
}

func (b *Bot) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
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

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		b.reply(ctx, msg.Chat.ID, "Я понимаю только текст 🙂")
		return
	}

	if msg.IsCommand() {
		b.handleCommand(ctx, msg, user)
		return
	}

	switch user.State {
	case storage.StateAwaitingCity:
		b.handleCityInput(ctx, msg.Chat.ID, user, text)
	case storage.StateAwaitingTime:
		b.handleTimeInput(ctx, msg.Chat.ID, user, text)
	default:
		b.handleChat(ctx, msg.Chat.ID, user, text)
	}
}

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message, user *storage.User) {
	chatID := msg.Chat.ID

	switch msg.Command() {
	case "start":
		if user.HasCity() {
			b.reply(ctx, chatID, fmt.Sprintf(
				"С возвращением, %s! 👋 Город: %s, погода приходит каждый день в %s.\nСпроси меня о чём-нибудь или /weather.",
				firstName(user), *user.CityName, user.DailyTime))
			return
		}
		b.reply(ctx, chatID, fmt.Sprintf("Привет, %s! 👋 Я Надя — твой персональный бот.\nБуду каждое утро присылать погоду и отвечать на вопросы.", firstName(user)))
		b.askCity(ctx, chatID, user.ID)

	case "help":
		b.reply(ctx, chatID, "Вот что я умею:\n"+
			"• /weather — погода на сегодня\n"+
			"• /city — сменить город\n"+
			"• /time — во сколько присылать утреннюю погоду\n"+
			"• /on, /off — включить/выключить утреннюю погоду\n"+
			"• /forget — забыть наш разговор\n\n"+
			"А ещё просто пиши мне — отвечу на вопросы 🙂")

	case "city":
		b.askCity(ctx, chatID, user.ID)

	case "time":
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

	case "weather":
		if !user.HasCity() {
			b.askCity(ctx, chatID, user.ID)
			return
		}
		b.sendWeather(ctx, chatID, user, true)

	case "forget":
		if err := b.store.ClearMessages(ctx, user.ID); err != nil {
			slog.Error("clear messages", "err", err)
			return
		}
		b.reply(ctx, chatID, "Ок, забыли наш разговор 🤝 Начнём с чистого листа.")

	case "on":
		if !user.HasCity() {
			b.askCity(ctx, chatID, user.ID)
			return
		}
		if err := b.store.SetDailyEnabled(ctx, user.ID, true); err != nil {
			slog.Error("daily on", "err", err)
			return
		}
		b.reply(ctx, chatID, fmt.Sprintf("Включила ✅ Буду писать погоду каждый день в %s.", user.DailyTime))

	case "off":
		if err := b.store.SetDailyEnabled(ctx, user.ID, false); err != nil {
			slog.Error("daily off", "err", err)
			return
		}
		b.reply(ctx, chatID, "Выключила утреннюю погоду 🔕 (вернуть: /on)")

	default:
		b.reply(ctx, chatID, "Не знаю такую команду 🤷‍♀️ Посмотри /help")
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

	if err := b.store.UpdateCity(ctx, user.ID, place.Name, place.Latitude, place.Longitude, place.Timezone); err != nil {
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

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	if runed := []rune(text); len(runed) > maxMessageRunes {
		text = string(runed[:maxMessageRunes])
	}
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("send message", "chat_id", chatID, "err", err)
	}
}

func firstName(u *storage.User) string {
	if u.FirstName != "" {
		return u.FirstName
	}
	return "друг"
}
