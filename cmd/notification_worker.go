package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"pingomon/internal/config"
	"pingomon/internal/storage"

	"github.com/ClickHouse/clickhouse-go/v2"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/ilyakaznacheev/cleanenv"
)

type errorReport struct {
	Addr    string    `ch:"addr"`
	Count   uint64    `ch:"cnt"`
	FirstAt time.Time `ch:"first_at"`
	LastAt  time.Time `ch:"last_at"`
	Errors  []string  `ch:"errs"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	var cfg config.Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		slog.Error("config error", "err", err)
		os.Exit(1)
	}

	chConn, err := storage.NewClickhouse(cfg.ClickHouseDSN)
	if err != nil {
		slog.Error("clickhouse connect", "err", err)
		os.Exit(1)
	}
	defer chConn.Close()

	pgPool, err := storage.NewPostgresPool(cfg.PostgresDSN)
	if err != nil {
		slog.Error("postgres connect", "err", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	targetRepo := storage.NewTargetRepository(pgPool)
	if err := targetRepo.EnsureSchema(context.Background()); err != nil {
		slog.Error("ensure schema", "err", err)
		os.Exit(1)
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("telegram bot init", "err", err)
		os.Exit(1)
	}

	slog.Info("Notification worker started")

	pollInterval := time.Duration(cfg.NotifyIntervalSec) * time.Second
	window := time.Duration(cfg.NotifyWindowMin) * time.Minute

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		runIteration(bot, chConn, targetRepo, window)
	}
}

func runIteration(bot *tgbotapi.BotAPI, chConn clickhouse.Conn, repo *storage.TargetRepository, window time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	from := time.Now().Add(-window)
	reports, err := fetchErrorReports(ctx, chConn, from)
	if err != nil {
		slog.Error("fetch error reports", "err", err)
		return
	}

	if len(reports) == 0 {
		slog.Debug("no error reports in window")
		return
	}

	targets := make([]string, 0, len(reports))
	for _, r := range reports {
		targets = append(targets, r.Addr)
	}

	subscribers, err := repo.SubscribersByTargets(ctx, targets)
	if err != nil {
		slog.Error("load subscribers", "err", err)
		return
	}

	for _, r := range reports {
		users := subscribers[r.Addr]
		if len(users) == 0 {
			continue
		}

		text := formatNotification(r)
		for _, uid := range users {
			msg := tgbotapi.NewMessage(uid, text)
			msg.ParseMode = "Markdown"
			if _, err := bot.Send(msg); err != nil {
				slog.Error("send notification", "err", err, "user", uid, "addr", r.Addr)
			}
		}
	}
}

func fetchErrorReports(ctx context.Context, conn clickhouse.Conn, from time.Time) ([]errorReport, error) {
	query := `
		SELECT
			addr,
			count() AS cnt,
			min(ts) AS first_at,
			max(ts) AS last_at,
			arrayDistinct(groupArray(err)) AS errs
		FROM pingomon.checks
		WHERE ts >= @from AND err != ''
		GROUP BY addr
		HAVING cnt > 0
	`

	rows, err := conn.Query(ctx, query, clickhouse.Named("from", from))
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []errorReport
	for rows.Next() {
		var r errorReport
		if err := rows.ScanStruct(&r); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	return out, nil
}

func formatNotification(r errorReport) string {
	text := fmt.Sprintf("⚠️ Проблемы с `%s`\n", r.Addr)
	text += fmt.Sprintf("С %s по %s заметил %d ошибок.\n",
		r.FirstAt.UTC().Format(time.RFC822),
		r.LastAt.UTC().Format(time.RFC822),
		r.Count)

	if len(r.Errors) == 0 {
		text += "Текстов ошибок нет (err пустая строка)."
		return text
	}

	text += "Уникальные ошибки:\n"
	for _, e := range r.Errors {
		text += fmt.Sprintf("• %s\n", e)
	}
	return text
}
