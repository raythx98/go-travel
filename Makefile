BIN := bin/server
CMD := ./cmd/server

.PHONY: build run run_local dev test sqlc fmt vet up down logs create_migration

build:
	go build -o $(BIN) $(CMD)

run: build
	./$(BIN)

run_local:
	go run $(CMD)

# dev starts the required Docker services (SearXNG, Redis, LangFuse, Postgres)
# and then runs the server locally. Press Ctrl+C to stop the server; Docker
# services keep running in the background (use `make down` to stop them).
dev:
	docker compose up -d postgres redis searxng langfuse ollama
	go run $(CMD)

test:
	go test ./...

sqlc:
	sqlc generate

fmt:
	go fmt ./...

vet:
	go vet ./...

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f

create_migration:
	migrate create -ext sql -dir sqlc/migrations -seq $(name)
