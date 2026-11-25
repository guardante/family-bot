// Package integrations: интерфейс внешних сервисов списка дел.
// Локально задачи всегда хранятся в SQLite; интеграции — опциональны,
// их можно подключать по одной или несколько.
package integrations

import "context"

// Provider — интеграция с внешним сервисом задач.
type Provider interface {
	// Name — идентификатор провайдера, сохраняется в БД ("todoist", ...).
	Name() string
	// Title — человекочитаемое имя для интерфейса бота.
	Title() string
	// Validate проверяет токен пользователя.
	Validate(ctx context.Context, token string) error
	// AddTask создаёт задачу и возвращает её id в внешнем сервисе.
	AddTask(ctx context.Context, token, text string) (string, error)
	// CloseTask отмечает задачу выполненной.
	CloseTask(ctx context.Context, token, remoteID string) error
	// ReopenTask возвращает задачу в работу.
	ReopenTask(ctx context.Context, token, remoteID string) error
	// DeleteTask удаляет задачу.
	DeleteTask(ctx context.Context, token, remoteID string) error
}

// Registry — все известные провайдеры.
type Registry struct {
	byName map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{byName: make(map[string]Provider, len(providers))}
	for _, p := range providers {
		r.byName[p.Name()] = p
	}
	return r
}

func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.byName[name]
	return p, ok
}

// SyncError — ошибка внешнего сервиса, при которой локальные данные сохранены.
// Бот показывает её пользователю мягко, не как сбой.
type SyncError struct {
	Provider string
	Err      error
}

func (e *SyncError) Error() string {
	return e.Provider + ": " + e.Err.Error()
}
