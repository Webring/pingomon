package config

type Config struct {
	ClickHouseDSN    string   `env:"CLICKHOUSE_DSN" env-required:"true"`
	PostgresDSN      string   `env:"POSTGRES_DSN" env-required:"true"`
	Targets          []string `env:"TARGETS" env-separator:","` // optional legacy seed list
	TelegramBotToken string   `env:"TELEGRAM_BOT_TOKEN" env-required:"true"`
	PingTimeout      int      `env:"TIMEOUT" env-required:"true"`
}
