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

	pgPool, err := storage.NewPostgresPool(cfg.PostgresDSN)
	if err != nil {
		slog.Error("failed to connect Postgres", "err", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	targetRepo := storage.NewTargetRepository(pgPool)
	if err := targetRepo.EnsureSchema(context.Background()); err != nil {
		slog.Error("failed to ensure targets table", "err", err)
		os.Exit(1)
	}

	if len(cfg.Targets) > 0 {
		seedTargets(context.Background(), targetRepo, cfg.Targets)
	}

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
			lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			targets, err := targetRepo.ListTargets(lookupCtx)
			cancel()

			if err != nil {
				slog.Error("failed to fetch targets", "err", err)
				continue
			}

			if len(targets) == 0 {
				slog.Warn("no targets to ping")
				continue
			}

			for _, targetURL := range targets {
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

func seedTargets(ctx context.Context, repo *storage.TargetRepository, seeds []string) {
	for _, raw := range seeds {
		normalized, err := normalizeURL(raw)
		if err != nil {
			slog.Warn("skip invalid seed target", "url", raw, "err", err)
			continue
		}

		_ = repo.AddTarget(ctx, normalized)
	}
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
