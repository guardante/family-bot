package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nadia_bot/internal/storage"
)

// RunScheduler раз в 30 секунд проверяет, кому пора слать утренний прогноз.
// last_daily_date защищает от повторной отправки и пропусков при перезапусках.
func (b *Bot) RunScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.schedulerTick(ctx)
		}
	}
}

func (b *Bot) schedulerTick(ctx context.Context) {
	b.sendDailyWeather(ctx)
	b.checkEventReminders(ctx)
}

// sendDailyWeather — утренние прогнозы тем, у кого наступило время.
func (b *Bot) sendDailyWeather(ctx context.Context) {
	users, err := b.store.DailyUsers(ctx)
	if err != nil {
		slog.Error("scheduler: получить пользователей", "err", err)
		return
	}

	for i := range users {
		u := &users[i]

		loc, err := time.LoadLocation(u.Timezone)
		if err != nil {
			slog.Error("scheduler: часовой пояс", "tz", u.Timezone, "err", err)
			continue
		}

		sendTime, err := time.Parse("15:04", u.DailyTime)
		if err != nil {
			slog.Error("scheduler: время рассылки", "user", u.ID, "time", u.DailyTime, "err", err)
			continue
		}

		now := time.Now().In(loc)
		target := time.Date(now.Year(), now.Month(), now.Day(), sendTime.Hour(), sendTime.Minute(), 0, 0, loc)
		today := now.Format("2006-01-02")

		// Уже наступило время и сегодня ещё не отправляли.
		if now.Before(target) || (u.LastDailyDate != nil && *u.LastDailyDate == today) {
			continue
		}

		b.sendWeather(ctx, u.ID, u, false)
		if err := b.store.MarkDailySent(ctx, u.ID, today); err != nil {
			slog.Error("scheduler: пометить отправку", "user", u.ID, "err", err)
		}
	}
}

// checkEventReminders — напоминания о событиях календаря.
func (b *Bot) checkEventReminders(ctx context.Context) {
	now := time.Now().UTC()
	due, err := b.store.DueReminderCandidates(ctx,
		storage.NormalizeEventAt(now), storage.NormalizeEventAt(now.Add(24*time.Hour)))
	if err != nil {
		slog.Error("scheduler: напоминания", "err", err)
		return
	}

	for _, ev := range due {
		at, err := storage.ParseEventAt(ev.EventAt)
		if err != nil {
			continue
		}
		remind := time.Duration(*ev.RemindMinutes) * time.Minute

		// Пора: осталось меньше окна напоминания, событие ещё не прошло.
		if until := at.Sub(now); until <= 0 || until > remind {
			continue
		}

		user, err := b.store.GetUser(ctx, ev.UserID)
		if err != nil || user == nil {
			continue
		}
		loc, err := time.LoadLocation(user.Timezone)
		if err != nil {
			loc = time.UTC
		}
		local := at.In(loc)

		text := fmt.Sprintf("⏰ Напоминаю: %s\nЧерез %s — в %s",
			ev.Title, humanDelta(at.Sub(now)), local.Format("15:04 (02.01)"))
		b.reply(ctx, ev.UserID, text)

		if err := b.store.MarkEventNotified(ctx, ev.ID); err != nil {
			slog.Error("scheduler: пометить напоминание", "err", err)
		}
	}
}
