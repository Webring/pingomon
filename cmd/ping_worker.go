package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"pingomon/internal/check"
	"pingomon/internal/config"
	"pingomon/internal/storage"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

func resolveURL(raw string) (*net.IPAddr, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	host := u.Host
	if strings.Contains(host, ":") {
		host, _, _ = strings.Cut(host, ":")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("lookup ip: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IPs found for host: %s", host)
	}

	slog.Debug("resolved host", "host", host, "ip", ips[0])
	return &net.IPAddr{IP: ips[0]}, nil
}

func runPing(ctx context.Context, repo *storage.CheckRepository, targetURL string) {
	log := slog.With("url", targetURL)

	ipAddr, resolveErr := resolveURL(targetURL)
	if resolveErr != nil {
		log.Error("failed to resolve", "err", resolveErr)
		// Можно всё равно пинговать, но IPAddr будет nil
	}

	res := check.HttpPing(targetURL)

	log.Info("ping result",
		"status", res.StatusCode,
		"duration", res.Duration,
		"err", res.Err,
	)

	ip := net.IPAddr{}
	if ipAddr != nil {
		ip = *ipAddr
	}

	var errMsg string
	if res.Err != nil {
		errMsg = res.Err.Error()
	}

	if insertErr := repo.InsertCheck(
		time.Now().UTC(),
		targetURL,
		ip,
		2,
		true,
		float64(res.Duration),
		uint16(res.StatusCode),
		errMsg,
		"net/http",
	); insertErr != nil {
		log.Error("failed to insert check", "err", insertErr)
	}
}

func main() {
	var cfg config.Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	clickHouseConnection, err := storage.NewClickhouse(cfg.ClickHouseDSN)
	if err != nil {
		slog.Error("failed to connect ClickHouse", "err", err)
		os.Exit(1)
	}
	defer clickHouseConnection.Close()

	repository := storage.NewCheckRepository(clickHouseConnection)

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	slog.Info("Service PingWorker started")

	ticker := time.NewTicker(time.Duration(cfg.PingTimeout) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			var wg sync.WaitGroup
			for _, targetURL := range cfg.Targets {
				wg.Add(1)
				go func(url string) {
					defer wg.Done()
					runPing(ctx, repository, url)
				}(targetURL)
			}
			wg.Wait()

		case <-ctx.Done():
			slog.Info("Service PingWorker stopped")
			return
		}
	}
}
