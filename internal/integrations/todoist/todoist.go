// Package todoist: интеграция со списком дел Todoist (REST API v2).
// Токен — персональный, берётся из настроек Todoist (Settings → Integrations → API token).
package todoist

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

const baseURL = "https://api.todoist.com/rest/v2"

type Provider struct{ http *http.Client }

func New() *Provider {
	return &Provider{http: &http.Client{Timeout: 15 * time.Second}}
}

func (p *Provider) Name() string  { return "todoist" }
func (p *Provider) Title() string { return "Todoist" }

func (p *Provider) do(ctx context.Context, token, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("токен отклонён (статус %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Todoist: статус %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return data, nil
}

func (p *Provider) Validate(ctx context.Context, token string) error {
	_, err := p.do(ctx, token, http.MethodGet, "/projects", nil)
	return err
}

func (p *Provider) AddTask(ctx context.Context, token, text string) (string, error) {
	data, err := p.do(ctx, token, http.MethodPost, "/tasks", map[string]string{"content": text})
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (p *Provider) CloseTask(ctx context.Context, token, remoteID string) error {
	_, err := p.do(ctx, token, http.MethodPost, "/tasks/"+remoteID+"/close", nil)
	return err
}

func (p *Provider) ReopenTask(ctx context.Context, token, remoteID string) error {
	_, err := p.do(ctx, token, http.MethodPost, "/tasks/"+remoteID+"/reopen", nil)
	return err
}

func (p *Provider) DeleteTask(ctx context.Context, token, remoteID string) error {
	_, err := p.do(ctx, token, http.MethodDelete, "/tasks/"+remoteID, nil)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}
