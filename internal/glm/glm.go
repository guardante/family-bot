// Package glm: клиент к GLM API (OpenAI-совместимый /chat/completions).
package glm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// VisionPart — часть мультимодального сообщения (текст или изображение).
type VisionPart struct {
	Type     string    `json:"type"` // "text" | "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type Client struct {
	apiKey      string
	baseURL     string
	model       string
	visionModel string
	http        *http.Client
}

func New(apiKey, baseURL, model, visionModel string) *Client {
	return &Client{
		apiKey:      apiKey,
		baseURL:     strings.TrimRight(baseURL, "/"),
		model:       model,
		visionModel: visionModel,
		http:        &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) Model() string       { return c.model }
func (c *Client) VisionModel() string { return c.visionModel }

// Chat отправляет текстовый диалог и возвращает ответ модели.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	return c.doChat(ctx, map[string]any{
		"model":       c.model,
		"messages":    messages,
		"temperature": 0.7,
		"max_tokens":  900,
	})
}

// VisionChat отправляет изображение (data URL) с инструкцией и возвращает ответ.
func (c *Client) VisionChat(ctx context.Context, prompt, dataURL string) (string, error) {
	return c.doChat(ctx, map[string]any{
		"model": c.visionModel,
		"messages": []map[string]any{{
			"role": "user",
			"content": []VisionPart{
				{Type: "image_url", ImageURL: &ImageURL{URL: dataURL}},
				{Type: "text", Text: prompt},
			},
		}},
		"temperature": 0.2,
		"max_tokens":  1500,
	})
}

func (c *Client) doChat(ctx context.Context, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("запрос к GLM: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GLM API: статус %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("ответ GLM не разобрать: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("GLM API: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("GLM API: пустой ответ")
	}

	answer := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if answer == "" {
		return "", fmt.Errorf("GLM API: пустой ответ модели")
	}
	return answer, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
