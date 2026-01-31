.PHONY: build run clean test fmt lint

BINARY_NAME=groq-go
BUILD_DIR=bin

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) .

run: build
	$(BUILD_DIR)/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)
	go clean

test:
	go test -v ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run

deps:
	go mod tidy

install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/
