package bot

// Настройки: инлайн-меню со статусом города, рассылки и интеграций.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/storage"
)

func (b *Bot) settingsText(u *storage.User) string {
	city := "не задан"
	if u.HasCity() {
		city = *u.CityName
	}
	daily := "выключена 🔕"
	if u.DailyEnabled {
		daily = "включена 🔔"
	}
	td := "не подключён"
	if todoistToken(u) != "" {
		td = "подключён ✅"
	}
	cal := "не подключён"
	if _, _, ok := caldavCreds(u); ok {
		cal = "подключён ✅"
	}
	return fmt.Sprintf("⚙️ Настройки\n\n🏙 Город: %s\n⏰ Утренняя погода: %s (%s)\n🔗 Todoist: %s\n📅 Календарь Apple: %s", city, u.DailyTime, daily, td, cal)
}

func (b *Bot) settingsMarkup(u *storage.User) tgbotapi.InlineKeyboardMarkup {
	dailyLabel := "🔔 Включить утреннюю погоду"
	if u.DailyEnabled {
		dailyLabel = "🔕 Выключить утреннюю погоду"
	}
	tdLabel := "🔗 Подключить Todoist"
	if todoistToken(u) != "" {
		tdLabel = "🧩 Отключить Todoist"
	}
	calLabel := "📅 Подключить Apple Календарь"
	if _, _, ok := caldavCreds(u); ok {
		calLabel = "📅 Отключить Apple Календарь"
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏙 Сменить город", "st_city"),
			tgbotapi.NewInlineKeyboardButtonData("⏰ Время рассылки", "st_time"),
		),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(dailyLabel, "st_daily")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(tdLabel, "st_todoist")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(calLabel, "st_caldav")),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🧠 Забыть разговор", "st_forget"),
			tgbotapi.NewInlineKeyboardButtonData("✖️ Закрыть", "st_close"),
		),
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) handleSettingsCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, user *storage.User, data string) {
	chatID := cb.Message.Chat.ID
	messageID := cb.Message.MessageID

	switch data {
	case "st_city":
		b.answerCallback(cb.ID, "")
		_, _ = b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		b.askCity(ctx, chatID, user.ID)

	case "st_time":
		b.answerCallback(cb.ID, "")
		_, _ = b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		b.cmdTime(ctx, chatID, user)

	case "st_daily":
		if err := b.store.SetDailyEnabled(ctx, user.ID, !user.DailyEnabled); err != nil {
			slog.Error("toggle daily", "err", err)
			b.answerCallback(cb.ID, "Ошибка")
			return
		}
		if user.DailyEnabled {
			b.answerCallback(cb.ID, "Утренняя погода выключена 🔕")
		} else {
			b.answerCallback(cb.ID, "Утренняя погода включена 🔔")
		}
		if u, err := b.store.GetUser(ctx, user.ID); err == nil && u != nil {
			b.editWithMarkup(ctx, chatID, messageID, b.settingsText(u), b.settingsMarkup(u))
		}

	case "st_todoist":
		if todoistToken(user) != "" {
			if err := b.store.SetTodoistToken(ctx, user.ID, ""); err != nil {
				slog.Error("todoist disconnect", "err", err)
				b.answerCallback(cb.ID, "Ошибка")
				return
			}
			b.answerCallback(cb.ID, "Todoist отключён")
			b.editWithMarkup(ctx, chatID, messageID, b.settingsText(user), b.settingsMarkup(user))
			return
		}
		b.answerCallback(cb.ID, "")
		_, _ = b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		if err := b.store.SetState(ctx, user.ID, storage.StateAwaitingTodoist); err != nil {
			slog.Error("set state", "err", err)
			return
		}
		b.reply(ctx, chatID, "Пришли персональный API-токен Todoist одним сообщением.\n\n"+
			"Где взять: Todoist → Настройки → Интеграции → API-токен.\nОтменить: /cancel")

	case "st_caldav":
		if _, _, ok := caldavCreds(user); ok {
			if err := b.store.SetCaldavAuth(ctx, user.ID, "", ""); err != nil {
				slog.Error("caldav disconnect", "err", err)
				b.answerCallback(cb.ID, "Ошибка")
				return
			}
			b.answerCallback(cb.ID, "Календарь Apple отключён")
			b.editWithMarkup(ctx, chatID, messageID, b.settingsText(user), b.settingsMarkup(user))
			return
		}
		b.answerCallback(cb.ID, "")
		_, _ = b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		if err := b.store.SetState(ctx, user.ID, storage.StateAwaitingCaldav); err != nil {
			slog.Error("set state", "err", err)
			return
		}
		b.reply(ctx, chatID, "Подключим календарь Apple 📅\n\n"+
			"Пришли одним сообщением в формате:\napple_id:пароль_приложения\n\n"+
			"Где взять: account.apple.com → Вход и безопасность → Пароль приложения (создай для бота).\n"+
			"Обычный пароль от Apple ID не подходит — только пароль приложения!\nОтменить: /cancel")

	case "st_forget":
		if err := b.store.ClearMessages(ctx, user.ID); err != nil {
			slog.Error("clear messages", "err", err)
			b.answerCallback(cb.ID, "Ошибка")
			return
		}
		b.answerCallback(cb.ID, "Разговор забыт 🧠")
		b.editWithMarkup(ctx, chatID, messageID, b.settingsText(user), b.settingsMarkup(user))

	case "st_close":
		b.answerCallback(cb.ID, "")
		_, _ = b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))

	default:
		b.answerCallback(cb.ID, "")
	}
}

// connectCaldav — пользователь прислал "apple_id:пароль_приложения".
func (b *Bot) connectCaldav(ctx context.Context, chatID int64, user *storage.User, text string) {
	login, pass, ok := strings.Cut(strings.TrimSpace(text), ":")
	if !ok || login == "" || pass == "" {
		b.reply(ctx, chatID, "Нужно два значения через двоеточие:\napple_id:пароль_приложения\n\nПопробуй ещё раз или /cancel.")
		return
	}

	if err := b.caldav.Validate(ctx, login, pass, b.cfg.CaldavURL); err != nil {
		b.reply(ctx, chatID, "Не подключилось: "+err.Error()+"\nПроверь логин и пароль приложения, или /cancel.")
		return
	}

	if err := b.store.SetCaldavAuth(ctx, user.ID, login, pass); err != nil {
		slog.Error("save caldav", "err", err)
		return
	}
	if err := b.store.SetState(ctx, user.ID, storage.StateNone); err != nil {
		slog.Error("set state", "err", err)
	}
	b.reply(ctx, chatID, "Календарь Apple подключён ✅\nНовые события будут появляться на iPhone и Mac. А ещё можно просто наговорить их голосом 🎙")
}

// connectTodoist — пользователь прислал токен в ответ на запрос.
func (b *Bot) connectTodoist(ctx context.Context, chatID int64, user *storage.User, text string) {
	p, ok := b.apps.Get("todoist")
	if !ok {
		_ = b.store.SetState(ctx, user.ID, storage.StateNone)
		b.reply(ctx, chatID, "Интеграция Todoist сейчас недоступна 🤷‍♀️")
		return
	}

	if err := p.Validate(ctx, text); err != nil {
		b.reply(ctx, chatID, "Токен не подошёл: "+err.Error()+"\nПроверь и пришли ещё раз, или /cancel.")
		return
	}

	if err := b.store.SetTodoistToken(ctx, user.ID, text); err != nil {
		slog.Error("save todoist token", "err", err)
		return
	}
	if err := b.store.SetState(ctx, user.ID, storage.StateNone); err != nil {
		slog.Error("set state", "err", err)
	}
	b.reply(ctx, chatID, "Todoist подключён ✅\nНовые дела будут появляться и там, отметки синхронизируются.")
}
