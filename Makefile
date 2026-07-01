## VMS Backend Makefile

.PHONY: run build tidy migrate seed

run:
	go run ./cmd/server

build:
	go build -o bin/vms ./cmd/server

tidy:
	go mod tidy

# docker-compose shorthand (requires docker-compose.yml in repo root)
db-up:
	docker compose up -d postgres

db-down:
	docker compose down

# Format + vet
lint:
	gofmt -l -w .
	go vet ./...
