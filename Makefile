.PHONY: build test run tidy up down logs rebuild

build:
	go build -o nadia ./cmd/bot

test:
	go test ./...

tidy:
	go mod tidy && gofmt -w .

# Локально без docker: нужен только .env, база создастся в data/nadia.db
run:
	set -a; . ./.env; set +a; go run ./cmd/bot

up:
	docker compose up -d --build

rebuild:
	docker compose up -d --build --force-recreate bot

down:
	docker compose down

logs:
	docker compose logs -f bot
