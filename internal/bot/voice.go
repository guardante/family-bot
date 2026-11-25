package bot

// Голосовые сообщения: речь -> текст (STT) -> роутер намерений
// (события, задачи, погода — или просто разговор).

import (
	"context"
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nadia_bot/internal/storage"
)

// handleVoice — голосовое сообщение: расшифровываем и отдаём общему роутеру.
func (b *Bot) handleVoice(ctx context.Context, msg *tgbotapi.Message, user *storage.User) {
	chatID := msg.Chat.ID
	if b.stt == nil {
		b.reply(ctx, chatID, "Распознавание голоса ещё не настроено 🔇 Попроси хозяина бота задать STT_API_KEY.")
		return
	}
	if _, err := b.api.Request(tgbotapiChatAction(chatID)); err != nil {
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

	// Дальше — как обычное сообщение: роутер сам поймёт, события это,
	// задачи, погода или просто разговор.
	b.handleMessageFree(ctx, chatID, user, transcript, true)
}
