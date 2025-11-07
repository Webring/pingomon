package storage

import (
	"context"
	"net"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type CheckResult struct {
	TS        time.Time `ch:"ts"`
	Addr      string    `ch:"addr"`
	IP4       net.IP    `ch:"ip4"`
	Kind      string    `ch:"kind"`
	Success   bool      `ch:"success"`
	LatencyMS float64   `ch:"latency_ms"`
	HTTPCode  uint16    `ch:"http_code"`
	Err       string    `ch:"err"`
	Agent     string    `ch:"agent"`
}

type CheckRepository struct {
	conn clickhouse.Conn
}

func NewCheckRepository(conn clickhouse.Conn) *CheckRepository {
	return &CheckRepository{conn: conn}
}

func (r *CheckRepository) InsertCheck(
	ts time.Time,
	addr string,
	ip4 net.IPAddr,
	kind int8,
	success bool,
	latency float64,
	httpCode uint16,
	errMsg, agent string) error {
	ctx := context.Background()

	batch, err := r.conn.PrepareBatch(ctx, `
		INSERT INTO pingomon.checks 
		(ts, addr, ip4, kind, success, latency_ms, http_code, err, agent)
	`)

	if err != nil {
		return err
	}

	if err := batch.Append(
		ts,
		addr,
		ip4.IP.To4(),
		kind,
		success,
		latency,
		httpCode,
		errMsg,
		agent,
	); err != nil {
		return err
	}

	return batch.Send()
}
