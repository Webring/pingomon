package config

type Config struct {
	ClickHouseDSN     string `env:"CLICKHOUSE_DSN" env-required:"true"`
	PostgresDSN       string `env:"POSTGRES_DSN" env-required:"true"`
	TelegramBotToken  string `env:"TELEGRAM_BOT_TOKEN" env-required:"true"`
	PingTimeout       int    `env:"TIMEOUT" env-required:"true"`
	NotifyWindowMin   int    `env:"NOTIFY_WINDOW_MINUTES" env-default:"5"`
	NotifyIntervalSec int    `env:"NOTIFY_POLL_SECONDS" env-default:"60"`
	WebAppBaseURL     string `env:"WEBAPP_BASE_URL"`
	WebAppAPIBaseURL  string `env:"WEBAPP_API_BASE_URL"`
	WebAppAllowedOrigin string `env:"WEBAPP_ALLOWED_ORIGIN"`
	WebAppListenAddr  string `env:"WEBAPP_LISTEN_ADDR" env-default:":8080"`
	WebAppDevUserID   int64  `env:"WEBAPP_DEV_USER_ID" env-default:"0"`
}
