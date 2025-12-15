package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPostgresPool(dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}

type TargetRepository struct {
	pool *pgxpool.Pool
}

var createTargetsTableQuery = `
CREATE TABLE IF NOT EXISTS targets (
	id SERIAL PRIMARY KEY,
	url TEXT UNIQUE NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS subscriptions (
	tg_user_id BIGINT NOT NULL,
	target_id  INT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (tg_user_id, target_id)
);`

var ErrTargetExists = errors.New("target already exists")
var ErrSubscriptionExists = errors.New("subscription already exists")

func NewTargetRepository(pool *pgxpool.Pool) *TargetRepository {
	return &TargetRepository{pool: pool}
}

func (r *TargetRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, createTargetsTableQuery)
	if err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	return nil
}

func (r *TargetRepository) AddTarget(ctx context.Context, url string) error {
	_, err := r.ensureTarget(ctx, url)
	if errors.Is(err, ErrTargetExists) {
		return nil
	}
	return err
}

func (r *TargetRepository) ListTargets(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT url FROM targets ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("select targets: %w", err)
	}
	defer rows.Close()

	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("scan target: %w", err)
		}
		urls = append(urls, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate targets: %w", err)
	}

	return urls, nil
}

func (r *TargetRepository) AddSubscription(ctx context.Context, tgUserID int64, url string) error {
	targetID, err := r.ensureTarget(ctx, url)
	if err != nil && !errors.Is(err, ErrTargetExists) {
		return err
	}

	tag, err := r.pool.Exec(ctx, `
		INSERT INTO subscriptions (tg_user_id, target_id)
		VALUES ($1, $2)
		ON CONFLICT (tg_user_id, target_id) DO NOTHING
	`, tgUserID, targetID)
	if err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrSubscriptionExists
	}
	return nil
}

func (r *TargetRepository) ListUserSubscriptions(ctx context.Context, tgUserID int64) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.url
		FROM subscriptions s
		JOIN targets t ON t.id = s.target_id
		WHERE s.tg_user_id = $1
		ORDER BY t.url ASC
	`, tgUserID)
	if err != nil {
		return nil, fmt.Errorf("select subscriptions: %w", err)
	}
	defer rows.Close()

	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		urls = append(urls, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions: %w", err)
	}
	return urls, nil
}

func (r *TargetRepository) RemoveSubscription(ctx context.Context, tgUserID int64, url string) (bool, error) {
	targetID, err := r.findTargetID(ctx, url)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	tag, err := r.pool.Exec(ctx, `
		DELETE FROM subscriptions WHERE tg_user_id = $1 AND target_id = $2
	`, tgUserID, targetID)
	if err != nil {
		return false, fmt.Errorf("delete subscription: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *TargetRepository) ensureTarget(ctx context.Context, url string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO targets (url) VALUES ($1)
		ON CONFLICT (url) DO NOTHING
		RETURNING id
	`, url).Scan(&id)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, ErrTargetExists
		}
		return 0, fmt.Errorf("ensure target insert: %w", err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		if err := r.pool.QueryRow(ctx, `SELECT id FROM targets WHERE url = $1`, url).Scan(&id); err != nil {
			return 0, fmt.Errorf("lookup target id: %w", err)
		}
		return id, ErrTargetExists
	}

	return id, nil
}

func (r *TargetRepository) findTargetID(ctx context.Context, url string) (int64, error) {
	var id int64
	if err := r.pool.QueryRow(ctx, `SELECT id FROM targets WHERE url = $1`, url).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}
