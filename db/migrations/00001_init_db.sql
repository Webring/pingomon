-- +goose NO TRANSACTION
-- +goose Up
CREATE DATABASE IF NOT EXISTS pingomon;

-- +goose Down
DROP DATABASE IF EXISTS pingomon;