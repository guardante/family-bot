package bot

// Голосовые сообщения: речь -> текст (STT) -> события календаря (LLM).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/glm"
	"nadia_bot/internal/storage"
)

const extractPromptFmt = `Сейчас у пользователя %s (%s). Текст продиктован голосом:

«%s»

Извлеки из текста события календаря. Верни ТОЛЬКО JSON-массив без пояснений и без markdown:
[{"title":"краткое название","date":"YYYY-MM-DD","time":"HH:MM"}]

Правила:
— Относительные слова ("завтра", "в пятницу", "через неделю", "на выходных") вычисли от текущей даты.
— Если время не названо — ставь 09:00.
— Не выдумывай события, которых нет в тексте.
— Если событий нет, верни []`

type extractedEvent struct {
	Title string `json:"title"`
	Date  string `json:"date"`
	Time  string `json:"time"`
}

// handleVoice — голосовое сообщение: расшифровка и перенос событий в календарь.
func (b *Bot) handleVoice(ctx context.Context, msg *tgbotapi.Message, user *storage.User) {
	chatID := msg.Chat.ID
	if b.stt == nil {
		b.reply(ctx, chatID, "Распознавание голоса ещё не настроено 🔇 Попроси хозяина бота задать STT_API_KEY.")
		return
	}
	if _, err := b.api.Request(tgbotapi.NewChatAction(chatID, "typing")); err != nil {
		slog.Warn("chat action", "err", err)
	}

	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: msg.Voice.FileID})
	if err != nil {
		slog.Error("telegram get voice", "err", err)
		b.reply(ctx, chatID, "Не смогла скачать голосовое 😔 Попробуй ещё раз.")
		return
	}
	audio, err := b.downloadFile(ctx, file.Link(b.api.Token))
	if err != nil {
		slog.Error("download voice", "err", err)
		b.reply(ctx, chatID, "Не смогла скачать голосовое 😔 Попробуй ещё раз.")
		return
	}

	transcript, err := b.stt.Transcribe(ctx, audio, "voice.ogg", "ru")
	if err != nil {
		slog.Error("stt", "err", err)
		b.reply(ctx, chatID, "Не получилось разобрать речь 😔 Попробуй говорить чуть ближе к микрофону.")
		return
	}
	if transcript == "" {
		b.reply(ctx, chatID, "Не расслышала ничего 🤔 Попробуй ещё раз.")
		return
	}

	loc, err := time.LoadLocation(user.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)

	answer, err := b.llm.Chat(ctx, []glm.Message{{
		Role:    "user",
		Content: fmt.Sprintf(extractPromptFmt, now.Format("02.01.2006 15:04"), user.Timezone, transcript),
	}})
	if err != nil {
		slog.Error("llm extract", "err", err)
		b.reply(ctx, chatID, "🎤 Услышала: «"+transcript+"»\n\nНо подумать не получилось 😔 Добавь событие вручную: /event ДД.ММ ЧЧ:ММ Название")
		return
	}

	events := parseExtractedEvents(answer)
	if len(events) == 0 {
		b.reply(ctx, chatID, "🎤 Услышала: «"+transcript+"»\n\nСобытий в этом не нашла. Добавить вручную: /event ДД.ММ ЧЧ:ММ Название")
		return
	}

	var saved []string
	caldavNote := ""
	for _, ev := range events {
		at, err := time.ParseInLocation("2006-01-02 15:04", ev.Date+" "+ev.Time, loc)
		if err != nil {
			slog.Warn("extracted event time", "date", ev.Date, "time", ev.Time, "err", err)
			continue
		}

		remind := 30
		id, err := b.store.AddEvent(ctx, user.ID, ev.Title, storage.NormalizeEventAt(at), &remind)
		if err != nil {
			slog.Error("add event from voice", "err", err)
			continue
		}
		if err := b.caldavPush(ctx, user, id, ev.Title, at); err != nil {
			slog.Warn("caldav push", "err", err)
			caldavNote = "\n\n⚠️ В календарь Apple добавить не удалось: " + err.Error()
		}
		saved = append(saved, fmt.Sprintf("• %s — %s", ev.Title, at.Format("02.01 в 15:04")))
	}

	if len(saved) == 0 {
		b.reply(ctx, chatID, "🎤 Услышала: «"+transcript+"»\n\nНо даты разобрать не смогла 😔 Добавь вручную: /event ДД.ММ ЧЧ:ММ Название")
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("🎤 Услышала: «%s»\n\n📅 Добавила:\n%s%s", transcript, strings.Join(saved, "\n"), caldavNote))
}

// parseExtractedEvents вынимает JSON-массив событий из ответа модели.
func parseExtractedEvents(answer string) []extractedEvent {
	s := strings.TrimSpace(answer)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	// Модель иногда добавляет текст до/после — берём от первой [ до последней ].
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end < start {
		return nil
	}

	var events []extractedEvent
	if err := json.Unmarshal([]byte(s[start:end+1]), &events); err != nil {
		return nil
	}

	valid := make([]extractedEvent, 0, len(events))
	for _, ev := range events {
		ev.Title = strings.TrimSpace(ev.Title)
		ev.Date = strings.TrimSpace(ev.Date)
		ev.Time = strings.TrimSpace(ev.Time)
		if ev.Title == "" {
			continue
		}
		if ev.Time == "" {
			ev.Time = "09:00"
		}
		if _, err := time.Parse("2006-01-02", ev.Date); err != nil {
			continue
		}
		if _, err := time.Parse("15:04", ev.Time); err != nil {
			continue
		}
		valid = append(valid, ev)
	}
	return valid
}
