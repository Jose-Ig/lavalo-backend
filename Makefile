.PHONY: run migrate tidy

# Run the application
run:
	go run ./cmd/lavalo-api/main.go

# Run database migrations (auto-migrate is done on startup)
migrate:
	go run ./cmd/lavalo-api/main.go --migrate-only

# Tidy up dependencies
tidy:
	go mod tidy

