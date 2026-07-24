.PHONY: tidy fmt test race migrate-up migrate-down run restart reset-test-session compose-up compose-down smoke backup restore

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

restart:
	./scripts/restart-from-source.sh

reset-test-session:
	./scripts/reset-test-session.sh --user "$(USER_ID)" --device "$(DEVICE_ID)"

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down

smoke:
	./scripts/smoke.sh

backup:
	./scripts/backup.sh

restore:
	./scripts/restore.sh $(BACKUP)
