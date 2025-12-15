package config

type Config struct {
	ClickHouseDSN     string `env:"CLICKHOUSE_DSN" env-required:"true"`
	PostgresDSN       string `env:"POSTGRES_DSN" env-required:"true"`
	TelegramBotToken  string `env:"TELEGRAM_BOT_TOKEN" env-required:"true"`
	PingTimeout       int    `env:"TIMEOUT" env-required:"true"`
	NotifyWindowMin   int    `env:"NOTIFY_WINDOW_MINUTES" env-default:"5"`
	NotifyIntervalSec int    `env:"NOTIFY_POLL_SECONDS" env-default:"60"`
}
