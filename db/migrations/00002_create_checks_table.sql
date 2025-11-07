-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS pingomon.checks (
    ts         DateTime('UTC'),
    addr       LowCardinality(String),
    ip4        IPv4,
    kind       Enum8('ping' = 1, 'http' = 2),
    success    UInt8,
    latency_ms Float64,
    http_code  UInt16,
    err        String,
    agent      LowCardinality(String) DEFAULT 'unknown'
)

ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (addr, kind, ts)
TTL ts + INTERVAL 32 DAY DELETE
SETTINGS index_granularity = 8192;

-- +goose Down
DROP TABLE IF EXISTS pingomon.checks;