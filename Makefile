.PHONY: build run test docker-up docker-down

build:
	go build -o bin/goqueue ./cmd/goqueue

run: build
	./bin/goqueue

test:
	go test ./... -v

docker-up:
	docker compose up -d

docker-down:
	docker compose down
