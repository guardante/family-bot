// Package web: WebApp (мини-приложение) — статика, JSON API и проверка подписи Telegram.
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"nadia_bot/internal/integrations"
	"nadia_bot/internal/storage"
)

// TGUser — пользователь внутри initData.
type TGUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// ValidateInitData проверяет подпись initData (HMAC по токену бота)
// и возвращает пользователя. Алгоритм из документации Telegram Mini Apps.
func ValidateInitData(initData, botToken string, maxAge time.Duration) (TGUser, error) {
	var user TGUser
	values, err := url.ParseQuery(initData)
	if err != nil {
		return user, fmt.Errorf("initData не разобрать: %w", err)
	}
	hash := values.Get("hash")
	if hash == "" {
		return user, errors.New("нет hash")
	}

	pairs := make([]string, 0, len(values))
	for k, v := range values {
		if k == "hash" {
			continue
		}
		pairs = append(pairs, k+"="+v[0])
	}
	sort.Strings(pairs)
	checkString := strings.Join(pairs, "\n")

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(checkString))
	calculated := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(calculated), []byte(hash)) {
		return user, errors.New("подпись не совпала")
	}

	authDate, _ := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if authDate > 0 && time.Since(time.Unix(authDate, 0)) > maxAge {
		return user, errors.New("initData устарел")
	}

	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil {
		return user, fmt.Errorf("нет пользователя в initData: %w", err)
	}
	if user.ID == 0 {
		return user, errors.New("пустой user.id")
	}
	return user, nil
}

// Handler — HTTP-хендлеры WebApp.
type Handler struct {
	store    *storage.Storage
	apps     *integrations.Registry
	botToken string
	allowed  func(int64) bool
	static   http.Handler
}

func New(store *storage.Storage, apps *integrations.Registry, botToken string, allowed func(int64) bool, staticFS fs.FS) *Handler {
	return &Handler{
		store:    store,
		apps:     apps,
		botToken: botToken,
		allowed:  allowed,
		static:   http.FileServer(http.FS(staticFS)),
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/state", h.withAuth(h.handleState))
	mux.HandleFunc("/api/todos", h.withAuth(h.handleTodoAdd))
	mux.HandleFunc("/api/todos/toggle", h.withAuth(h.handleTodoToggle))
	mux.HandleFunc("/api/todos/delete", h.withAuth(h.handleTodoDelete))
	mux.HandleFunc("/api/events", h.withAuth(h.handleEventAdd))
	mux.HandleFunc("/api/events/delete", h.withAuth(h.handleEventDelete))
	mux.HandleFunc("/webapp/", func(w http.ResponseWriter, r *http.Request) {
		// StripPrefix обязателен: FileServer ищет файлы от корня ФС.
		http.StripPrefix("/webapp/", h.static).ServeHTTP(w, r)
	})
	mux.HandleFunc("/webapp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/webapp/", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/webapp/", http.StatusFound)
	})
}

// withAuth проверяет подпись initData и пускает дальше с userID.
func (h *Handler) withAuth(next func(http.ResponseWriter, *http.Request, TGUser)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.Header.Get("X-Telegram-Init-Data")
		if initData == "" {
			http.Error(w, "запрос не из Telegram", http.StatusUnauthorized)
			return
		}
		user, err := ValidateInitData(initData, h.botToken, 24*time.Hour)
		if err != nil {
			slog.Warn("webapp auth", "err", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !h.allowed(user.ID) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Пользователь мог не писать боту /start — но раз в вайтлисте, создаём.
		if _, err := h.store.EnsureUser(r.Context(), user.ID, user.Username, user.FirstName); err != nil {
			slog.Error("webapp ensure user", "err", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		next(w, r, user)
	}
}

// ---- обработчики API ----

type stateResponse struct {
	User   userState       `json:"user"`
	Todos  []storage.Todo  `json:"todos"`
	Events []storage.Event `json:"events"`
}

type userState struct {
	City          string `json:"city"`
	DailyTime     string `json:"daily_time"`
	DailyEnabled  bool   `json:"daily_enabled"`
	TodoistLinked bool   `json:"todoist_linked"`
}

func (h *Handler) handleState(w http.ResponseWriter, r *http.Request, tgUser TGUser) {
	user, err := h.store.GetUser(r.Context(), tgUser.ID)
	if err != nil || user == nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	todos, err := h.store.Todos(r.Context(), tgUser.ID, 200)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	events, err := h.store.UpcomingEvents(r.Context(), tgUser.ID, storage.NormalizeEventAt(time.Now().Add(-2*time.Hour)), 200)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	st := stateResponse{
		User: userState{
			DailyTime:     user.DailyTime,
			DailyEnabled:  user.DailyEnabled,
			TodoistLinked: user.TodoistToken != nil && *user.TodoistToken != "",
		},
		Todos:  todos,
		Events: events,
	}
	if user.HasCity() {
		st.User.City = *user.CityName
	}
	writeJSON(w, st)
}

type todoRequest struct {
	Text string `json:"text"`
	ID   int64  `json:"id"`
	Done *bool  `json:"done"`
}

func (h *Handler) handleTodoAdd(w http.ResponseWriter, r *http.Request, tgUser TGUser) {
	var req todoRequest
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "пустой текст", http.StatusBadRequest)
		return
	}
	if runed := []rune(text); len(runed) > 200 {
		text = string(runed[:200])
	}

	user, err := h.store.GetUser(r.Context(), tgUser.ID)
	if err != nil || user == nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	todoID, err := h.store.AddTodo(r.Context(), tgUser.ID, text)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	// Опциональная синхронизация с Todoist (local-first).
	if user.TodoistToken != nil && *user.TodoistToken != "" {
		if p, ok := h.apps.Get("todoist"); ok {
			if remoteID, err := p.AddTask(r.Context(), *user.TodoistToken, text); err == nil {
				_ = h.store.SetTodoRemote(r.Context(), tgUser.ID, todoID, p.Name(), remoteID)
			} else {
				slog.Warn("webapp todoist add", "err", err)
			}
		}
	}

	writeJSON(w, map[string]any{"id": todoID})
}

func (h *Handler) handleTodoToggle(w http.ResponseWriter, r *http.Request, tgUser TGUser) {
	var req todoRequest
	if err := readJSON(r, &req); err != nil || req.ID == 0 || req.Done == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	todo, err := h.store.GetTodo(r.Context(), tgUser.ID, req.ID)
	if err != nil || todo == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := h.store.SetTodoDone(r.Context(), tgUser.ID, req.ID, *req.Done); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	// Синхронизация отметки.
	if user, _ := h.store.GetUser(r.Context(), tgUser.ID); user != nil && todo.RemoteID != nil && todo.Provider != nil && user.TodoistToken != nil && *user.TodoistToken != "" {
		if p, ok := h.apps.Get(*todo.Provider); ok {
			var syncErr error
			if *req.Done {
				syncErr = p.CloseTask(r.Context(), *user.TodoistToken, *todo.RemoteID)
			} else {
				syncErr = p.ReopenTask(r.Context(), *user.TodoistToken, *todo.RemoteID)
			}
			if syncErr != nil {
				slog.Warn("webapp todoist toggle", "err", syncErr)
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) handleTodoDelete(w http.ResponseWriter, r *http.Request, tgUser TGUser) {
	var req todoRequest
	if err := readJSON(r, &req); err != nil || req.ID == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	todo, err := h.store.DeleteTodo(r.Context(), tgUser.ID, req.ID)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if user, _ := h.store.GetUser(r.Context(), tgUser.ID); user != nil && todo != nil && todo.RemoteID != nil && todo.Provider != nil && user.TodoistToken != nil && *user.TodoistToken != "" {
		if p, ok := h.apps.Get(*todo.Provider); ok {
			_ = p.DeleteTask(r.Context(), *user.TodoistToken, *todo.RemoteID) // best effort
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

type eventRequest struct {
	Title         string `json:"title"`
	EventAt       string `json:"event_at"` // UTC RFC3339 от клиента
	RemindMinutes *int   `json:"remind_minutes"`
	ID            int64  `json:"id"`
}

var allowedReminders = map[int]bool{0: true, 5: true, 15: true, 30: true, 60: true, 180: true, 1440: true}

func (h *Handler) handleEventAdd(w http.ResponseWriter, r *http.Request, tgUser TGUser) {
	var req eventRequest
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		http.Error(w, "пустое название", http.StatusBadRequest)
		return
	}
	if runed := []rune(title); len(runed) > 200 {
		title = string(runed[:200])
	}
	at, err := time.Parse(time.RFC3339, req.EventAt)
	if err != nil {
		http.Error(w, "неверное время", http.StatusBadRequest)
		return
	}
	if req.RemindMinutes != nil && !allowedReminders[*req.RemindMinutes] {
		http.Error(w, "неверное напоминание", http.StatusBadRequest)
		return
	}

	id, err := h.store.AddEvent(r.Context(), tgUser.ID, title, storage.NormalizeEventAt(at), req.RemindMinutes)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (h *Handler) handleEventDelete(w http.ResponseWriter, r *http.Request, tgUser TGUser) {
	var req eventRequest
	if err := readJSON(r, &req); err != nil || req.ID == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, err := h.store.DeleteEvent(r.Context(), tgUser.ID, req.ID); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer func() { _, _ = io.Copy(io.Discard, r.Body) }() // опустошить тело
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
}
