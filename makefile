.PHONY: migrate
migrate:
	@echo "→ Applying migrations with goose..."
	@goose -env deploy/.env up


.PHONY: up
up:
	@echo "→ Docker compose up"
	@docker compose -f deploy/docker-compose.yaml up -d

.PHONY: prepare
prepare:
	@echo "→ Starting ClickHouse..."
	@docker compose -f deploy/docker-compose.yaml up -d
	@echo "→ Waiting for ClickHouse to become ready..."
	@sleep 5
	@echo "→ Applying migrations..."
	@goose -env deploy/.env up
	@echo "→ Shutting down ClickHouse..."
	@docker compose -f deploy/docker-compose.yaml down