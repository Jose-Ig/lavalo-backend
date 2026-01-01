.PHONY: run migrate-only tidy db-inspect

# Run the application
run:
	go run ./cmd/lavalo-api

# Run database migrations only and exit
migrate-only:
	go run ./cmd/lavalo-api -migrate-only

# Tidy up dependencies
tidy:
	go mod tidy

# Inspect database (requires running server)
db-inspect:
	@echo "Run: curl localhost:8080/api/v1/_debug/db"
