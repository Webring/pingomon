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

type PingStat struct {
	Addr string  `ch:"addr"`
	Avg  float64 `ch:"avg"`
	Min  float64 `ch:"min"`
	Max  float64 `ch:"max"`
}

func main() {
	// настраиваем slog (structured logging)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// читаем конфиг
	var cfg config.Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		slog.Error("config error", "err", err)
		os.Exit(1)
	}

	// подключение к ClickHouse
	conn, err := storage.NewClickhouse(cfg.ClickHouseDSN)
	if err != nil {
		slog.Error("clickhouse connect", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	slog.Info("Connected to ClickHouse")

	// Telegram бот
	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("telegram bot init", "err", err)
		os.Exit(1)
	}

	slog.Info("Bot authorized", "username", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		user := update.Message.From.UserName
		slog.Info("Received command", "user", user, "text", update.Message.Text)

		switch update.Message.Command() {
		case "stats":
			stats, err := getPingStats(conn)
			if err != nil {
				slog.Error("query error", "err", err)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID,
					fmt.Sprintf("❌ Query error: %v", err))
				bot.Send(msg)
				continue
			}

			text := formatStats(stats)
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)

			slog.Info("Sent stats", "user", user)

		default:
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "ℹ️ Доступные команды:\n/stats — показать статистику")
			bot.Send(msg)
		}
	}
}

func getPingStats(conn clickhouse.Conn) ([]PingStat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := `
		SELECT
			addr,
			avg(latency_ms / 1000000.0) AS avg,
			min(latency_ms / 1000000.0) AS min,
			max(latency_ms / 1000000.0) AS max
		FROM pingomon.checks
		GROUP BY addr
		ORDER BY avg ASC
	`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var result []PingStat
	for rows.Next() {
		var r PingStat
		if err := rows.ScanStruct(&r); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result = append(result, r)
	}
	return result, nil
}

func formatStats(stats []PingStat) string {
	if len(stats) == 0 {
		return "Нет данных о пингах 📭"
	}
	msg := "📊 *Ping statistics:*\n"
	for _, s := range stats {
		msg += fmt.Sprintf("• `%s`\n  avg: *%.2f ms* | min: %.2f | max: %.2f\n",
			s.Addr, s.Avg, s.Min, s.Max)
	}
	return msg
}
