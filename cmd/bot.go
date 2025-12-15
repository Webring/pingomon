package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
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

	pgPool, err := storage.NewPostgresPool(cfg.PostgresDSN)
	if err != nil {
		slog.Error("postgres connect", "err", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	targetRepo := storage.NewTargetRepository(pgPool)
	if err := targetRepo.EnsureSchema(context.Background()); err != nil {
		slog.Error("ensure targets schema", "err", err)
		os.Exit(1)
	}

	slog.Info("Connected to Postgres for targets")

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
		tgID := update.Message.From.ID
		slog.Info("Received command", "user", user, "id", tgID, "text", update.Message.Text)

		switch update.Message.Command() {
		case "stats":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			targets, err := targetRepo.ListUserSubscriptions(ctx, tgID)
			cancel()
			if err != nil {
				slog.Error("list subscriptions", "err", err, "user", user, "id", tgID)
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"❌ Не удалось получить список подписок. Попробуйте позже."))
				continue
			}

			if len(targets) == 0 {
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"ℹ️ У тебя пока нет подписок. Добавь через /add <url>."))
				continue
			}

			stats, err := getPingStats(conn, targets)
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

		case "add":
			raw := strings.TrimSpace(update.Message.CommandArguments())
			normalized, err := normalizeURL(raw)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"❌ Укажи адрес в формате https://example.com"))
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err = targetRepo.AddSubscription(ctx, tgID, normalized)
			cancel()

			if err != nil {
				if err == storage.ErrSubscriptionExists {
					bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
						"ℹ️ Ты уже подписан на этот адрес."))
					continue
				}
				slog.Error("add target", "err", err, "user", user)
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"❌ Не удалось сохранить адрес, попробуй позже."))
				continue
			}

			bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
				fmt.Sprintf("✅ Адрес `%s` добавлен. Он будет опрашиваться.", normalized)))
			slog.Info("Added new target", "user", user, "url", normalized)

		case "subs":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			subs, err := targetRepo.ListUserSubscriptions(ctx, tgID)
			cancel()
			if err != nil {
				slog.Error("list subscriptions", "err", err, "user", user)
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"❌ Не удалось получить подписки."))
				continue
			}
			if len(subs) == 0 {
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"ℹ️ Подписок пока нет. Добавь через /add <url>."))
				continue
			}
			builder := strings.Builder{}
			builder.WriteString("📄 Твои подписки:\n")
			for _, s := range subs {
				builder.WriteString("• ")
				builder.WriteString(s)
				builder.WriteByte('\n')
			}
			bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, builder.String()))

		case "del":
			raw := strings.TrimSpace(update.Message.CommandArguments())
			normalized, err := normalizeURL(raw)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"❌ Укажи адрес в формате https://example.com"))
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			removed, err := targetRepo.RemoveSubscription(ctx, tgID, normalized)
			cancel()
			if err != nil {
				slog.Error("remove subscription", "err", err, "user", user)
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"❌ Не удалось удалить подписку."))
				continue
			}
			if !removed {
				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
					"ℹ️ Такой подписки не было."))
				continue
			}
			bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID,
				"✅ Подписка удалена."))

		default:
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "ℹ️ Доступные команды:\n/stats — показать статистику по своим адресам\n/add <url> — добавить адрес для мониторинга\n/subs — показать твои подписки\n/del <url> — удалить подписку")
			bot.Send(msg)
		}
	}
}

func getPingStats(conn clickhouse.Conn, addrs []string) ([]PingStat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := `
		SELECT
			addr,
			avg(latency_ms / 1000000.0) AS avg,
			min(latency_ms / 1000000.0) AS min,
			max(latency_ms / 1000000.0) AS max
		FROM pingomon.checks
		WHERE addr IN @addrs
		GROUP BY addr
		ORDER BY avg ASC
	`

	rows, err := conn.Query(ctx, query, clickhouse.Named("addrs", addrs))
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

func normalizeURL(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty url")
	}

	trimmed := strings.TrimSpace(raw)
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme")
	}

	if u.Host == "" {
		return "", fmt.Errorf("empty host")
	}

	u.Fragment = ""
	return u.String(), nil
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
