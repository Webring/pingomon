package storage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type MetricPoint struct {
	TS        time.Time `ch:"ts" json:"ts"`
	Addr      string    `ch:"addr" json:"addr"`
	LatencyMS float64   `ch:"latency_ms" json:"latency_ms"`
	Success   bool      `ch:"success" json:"success"`
	HTTPCode  uint16    `ch:"http_code" json:"http_code"`
	Err       string    `ch:"err" json:"err"`
}

type Incident struct {
	Addr         string     `json:"addr"`
	StartedAt    time.Time  `json:"started_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	FailedChecks int        `json:"failed_checks"`
	LastHTTPCode uint16     `json:"last_http_code"`
	LastError    string     `json:"last_error"`
	Ongoing      bool       `json:"ongoing"`
}

func (r *CheckRepository) ListMetricSeries(ctx context.Context, addrs []string, from time.Time) (map[string][]MetricPoint, error) {
	if len(addrs) == 0 {
		return map[string][]MetricPoint{}, nil
	}

	query := `
		SELECT
			ts,
			addr,
			latency_ms / 1000000.0 AS latency_ms,
			success,
			http_code,
			err
		FROM pingomon.checks
		WHERE addr IN @addrs AND ts >= @from
		ORDER BY addr ASC, ts ASC
	`

	rows, err := r.conn.Query(ctx, query,
		clickhouse.Named("addrs", addrs),
		clickhouse.Named("from", from),
	)
	if err != nil {
		return nil, fmt.Errorf("query metric series: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]MetricPoint, len(addrs))
	for rows.Next() {
		var point MetricPoint
		if err := rows.ScanStruct(&point); err != nil {
			return nil, fmt.Errorf("scan metric point: %w", err)
		}
		result[point.Addr] = append(result[point.Addr], point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric series: %w", err)
	}

	for _, addr := range addrs {
		if _, ok := result[addr]; !ok {
			result[addr] = []MetricPoint{}
		}
	}

	return result, nil
}

func (r *CheckRepository) ListIncidents(ctx context.Context, addrs []string, from time.Time) (map[string][]Incident, error) {
	seriesByAddr, err := r.ListMetricSeries(ctx, addrs, from)
	if err != nil {
		return nil, err
	}

	incidentsByAddr := make(map[string][]Incident, len(addrs))
	for _, addr := range addrs {
		points := seriesByAddr[addr]
		incidents := make([]Incident, 0)

		var current *Incident
		for _, point := range points {
			if !point.Success {
				if current == nil {
					current = &Incident{
						Addr:      addr,
						StartedAt: point.TS,
						Ongoing:   true,
					}
				}

				current.LastSeenAt = point.TS
				current.FailedChecks++
				current.LastHTTPCode = point.HTTPCode
				current.LastError = point.Err
				if current.LastError == "" && point.HTTPCode > 0 {
					current.LastError = fmt.Sprintf("HTTP %d", point.HTTPCode)
				}
				continue
			}

			if current == nil {
				continue
			}

			resolvedAt := point.TS
			current.ResolvedAt = &resolvedAt
			current.Ongoing = false
			incidents = append(incidents, *current)
			current = nil
		}

		if current != nil {
			incidents = append(incidents, *current)
		}

		sort.Slice(incidents, func(i, j int) bool {
			return incidents[i].StartedAt.After(incidents[j].StartedAt)
		})
		incidentsByAddr[addr] = incidents
	}

	return incidentsByAddr, nil
}
