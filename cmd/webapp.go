package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"pingomon/internal/config"
	"pingomon/internal/storage"
	"pingomon/internal/webapp"

	"github.com/ilyakaznacheev/cleanenv"
)

//go:embed webapp_static/index.html
var staticFiles embed.FS

type dashboardSummary struct {
	AvailabilityPct  float64  `json:"availability_pct"`
	AvgLatencyMS     *float64 `json:"avg_latency_ms,omitempty"`
	IncidentCount    int      `json:"incident_count"`
	OngoingIncidents int      `json:"ongoing_incidents"`
}

type dashboardResponse struct {
	UserLabel      string                `json:"user_label"`
	Targets        []string              `json:"targets"`
	SelectedTarget string                `json:"selected_target"`
	Series         []storage.MetricPoint `json:"series"`
	Incidents      []storage.Incident    `json:"incidents"`
	Summary        dashboardSummary      `json:"summary"`
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

	checkRepo := storage.NewCheckRepository(chConn)
	apiBaseURL := strings.TrimRight(strings.TrimSpace(cfg.WebAppAPIBaseURL), "/")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w, r, apiBaseURL)
	})
	mux.HandleFunc("/api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		handleDashboard(w, r, cfg, targetRepo, checkRepo)
	})

	server := &http.Server{
		Addr:              cfg.WebAppListenAddr,
		Handler:           logMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("web app listening", "addr", cfg.WebAppListenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("web app stopped", "err", err)
		os.Exit(1)
	}
}

func serveIndex(w http.ResponseWriter, _ *http.Request, apiBaseURL string) {
	content, err := staticFiles.ReadFile("webapp_static/index.html")
	if err != nil {
		http.Error(w, "failed to load web app", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.ReplaceAll(string(content), "__API_BASE_URL__", apiBaseURL)
	_, _ = w.Write([]byte(page))
}

func handleDashboard(w http.ResponseWriter, r *http.Request, cfg config.Config, targetRepo *storage.TargetRepository, checkRepo *storage.CheckRepository) {
	if !allowCORS(w, r, cfg.WebAppAllowedOrigin) {
		return
	}

	user, err := resolveUser(r, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	targets, err := targetRepo.ListUserSubscriptions(ctx, user.ID)
	if err != nil {
		http.Error(w, "failed to load subscriptions", http.StatusInternalServerError)
		return
	}

	selectedTarget := strings.TrimSpace(r.URL.Query().Get("target"))
	if selectedTarget == "" && len(targets) > 0 {
		selectedTarget = targets[0]
	}
	if selectedTarget != "" && !contains(targets, selectedTarget) {
		http.Error(w, "target is not in your subscriptions", http.StatusForbidden)
		return
	}

	hours := clampHours(r.URL.Query().Get("hours"))
	from := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	filterTargets := targets
	if selectedTarget != "" {
		filterTargets = []string{selectedTarget}
	}

	seriesByAddr, err := checkRepo.ListMetricSeries(ctx, filterTargets, from)
	if err != nil {
		http.Error(w, "failed to load metrics", http.StatusInternalServerError)
		return
	}

	incidentsByAddr, err := checkRepo.ListIncidents(ctx, filterTargets, from)
	if err != nil {
		http.Error(w, "failed to load incidents", http.StatusInternalServerError)
		return
	}

	series := seriesByAddr[selectedTarget]
	incidents := incidentsByAddr[selectedTarget]

	payload := dashboardResponse{
		UserLabel:      formatUserLabel(user),
		Targets:        targets,
		SelectedTarget: selectedTarget,
		Series:         series,
		Incidents:      incidents,
		Summary:        buildSummary(series, incidents),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode dashboard response", "err", err)
	}
}

func allowCORS(w http.ResponseWriter, r *http.Request, allowedOrigin string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	allowedOrigin = strings.TrimSpace(allowedOrigin)

	if r.Method == http.MethodOptions {
		if allowedOrigin != "" && origin == allowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Telegram-Init-Data")
			w.WriteHeader(http.StatusNoContent)
			return false
		}

		http.Error(w, "origin is not allowed", http.StatusForbidden)
		return false
	}

	if allowedOrigin != "" && origin != "" {
		if origin != allowedOrigin {
			http.Error(w, "origin is not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Vary", "Origin")
	}

	return true
}

func resolveUser(r *http.Request, cfg config.Config) (*webapp.TelegramUser, error) {
	initData := r.Header.Get("X-Telegram-Init-Data")
	if initData != "" {
		return webapp.Authenticate(initData, cfg.TelegramBotToken)
	}

	if cfg.WebAppDevUserID > 0 {
		return &webapp.TelegramUser{ID: cfg.WebAppDevUserID, Username: "dev-user"}, nil
	}

	return nil, fmt.Errorf("telegram init data is required")
}

func clampHours(raw string) int {
	if raw == "" {
		return 24
	}

	hours, err := strconv.Atoi(raw)
	if err != nil {
		return 24
	}
	if hours < 1 {
		return 1
	}
	if hours > 24*7 {
		return 24 * 7
	}
	return hours
}

func contains(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}

func buildSummary(series []storage.MetricPoint, incidents []storage.Incident) dashboardSummary {
	summary := dashboardSummary{
		IncidentCount: len(incidents),
	}

	if len(series) == 0 {
		return summary
	}

	var successCount int
	var latencySum float64
	var latencyCount int
	for _, point := range series {
		if point.Success {
			successCount++
			latencySum += point.LatencyMS
			latencyCount++
		}
	}

	summary.AvailabilityPct = float64(successCount) / float64(len(series)) * 100
	if latencyCount > 0 {
		avg := latencySum / float64(latencyCount)
		summary.AvgLatencyMS = &avg
	}

	for _, incident := range incidents {
		if incident.Ongoing {
			summary.OngoingIncidents++
		}
	}

	return summary
}

func formatUserLabel(user *webapp.TelegramUser) string {
	switch {
	case user.Username != "":
		return "@" + user.Username
	case strings.TrimSpace(user.FirstName+" "+user.LastName) != "":
		return strings.TrimSpace(user.FirstName + " " + user.LastName)
	default:
		return strconv.FormatInt(user.ID, 10)
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"duration", time.Since(start),
		)
	})
}
