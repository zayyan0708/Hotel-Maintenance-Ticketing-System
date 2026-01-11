.PHONY: help tidy build run-gateway run-notifier run-auth clean

help:
	@echo "SmartHotel commands:"
	@echo "  make tidy         - go mod tidy"
	@echo "  make build        - build gateway + notifier + auth into ./bin"
	@echo "  make run-auth     - run auth locally"
	@echo "  make run-gateway  - run gateway locally"
	@echo "  make run-notifier - run notifier locally"
	@echo "  make clean        - remove ./bin"

tidy:
	go mod tidy

build:
	mkdir -p bin
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/notifier ./cmd/notifier
	go build -o bin/auth ./cmd/auth

run-auth:
	go run ./cmd/auth

run-gateway:
	go run ./cmd/gateway

run-notifier:
	go run ./cmd/notifier

clean:
	rm -rf bin
