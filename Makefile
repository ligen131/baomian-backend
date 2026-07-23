.PHONY: tidy fmt test race migrate-up migrate-down run compose-up compose-down smoke

tidy:
	go mod tidy

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...

race:
	go test -race ./...

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

run:
	go run ./cmd/server

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down

smoke:
	./scripts/smoke.sh
