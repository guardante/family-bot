package bot

// Распознавание текста на фото через vision-модель GLM.

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	defaultOcrPrompt = "Извлеки весь текст с изображения дословно. Верни только извлечённый текст, без комментариев. Если текста на изображении нет — одной короткой фразой опиши, что на нём."
	maxPhotoBytes    = 19 << 20 // лимит Telegram на скачивание файлов
)

// handlePhoto — фото или картинка документом: скачиваем и отправляем в vision-модель.
// Подпись к фото (если есть) используется как вопрос про изображение.
func (b *Bot) handlePhoto(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	if _, err := b.api.Request(tgbotapi.NewChatAction(chatID, "typing")); err != nil {
		slog.Warn("chat action", "err", err)
	}

	var fileID, mime string
	if msg.Photo != nil {
		fileID = msg.Photo[len(msg.Photo)-1].FileID // самое большое разрешение
		mime = "image/jpeg"
	} else if msg.Document != nil {
		fileID = msg.Document.FileID
		mime = msg.Document.MimeType
		if mime == "" {
			mime = "image/jpeg"
		}
	}
	if fileID == "" {
		return
	}

	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		slog.Error("telegram get file", "err", err)
		b.reply(ctx, chatID, "Не смогла скачать фото 😔 Попробуй ещё раз.")
		return
	}

	data, err := b.downloadFile(ctx, file.Link(b.api.Token))
	if err != nil {
		slog.Error("download photo", "err", err)
		b.reply(ctx, chatID, "Не смогла скачать фото 😔 Попробуй ещё раз.")
		return
	}

	prompt := defaultOcrPrompt
	title := "📄 Текст с фото:"
	if msg.Caption != "" {
		prompt = msg.Caption
		title = "🔍 По фото отвечаю:"
	}

	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	answer, err := b.llm.VisionChat(ctx, prompt, dataURL)
	if err != nil {
		slog.Error("vision", "err", err)
		b.reply(ctx, chatID, "Не получилось разобрать фото 😔 Попробуй ещё раз чуть позже.")
		return
	}

	b.reply(ctx, chatID, title+"\n\n"+answer)
}

func (b *Bot) downloadFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram file: статус %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPhotoBytes))
}
