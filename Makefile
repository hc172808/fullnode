BINARY    := gyds-litenode
VERSION   := 1.0.0
BUILD_DIR := bin

.PHONY: all build run clean tidy docker

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY) .

run: build
	./$(BUILD_DIR)/$(BINARY) start

tidy:
	go mod tidy

clean:
	rm -rf $(BUILD_DIR)

docker:
	docker build -t gyds-litenode:$(VERSION) -f ../../docker/Dockerfile.litenode .
	docker build -t gyds-litenode:latest -f ../../docker/Dockerfile.litenode .

test:
	go test ./...
