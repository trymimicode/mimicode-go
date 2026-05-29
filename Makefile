.PHONY: build install test clean dev lint fmt vet check-deps help

# Build variables
BINARY_NAME=mimicode
CMD_PATH=./cmd/mimicode
INSTALL_PATH=$(shell go env GOPATH)/bin
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"

# Default target
all: build

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	go build $(LDFLAGS) -o $(BINARY_NAME) $(CMD_PATH)
	@echo "✓ Binary created: ./$(BINARY_NAME)"

## install: Install to $GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME) to $(INSTALL_PATH)..."
	go install $(LDFLAGS) $(CMD_PATH)
	@echo "✓ Installed to $(INSTALL_PATH)/$(BINARY_NAME)"

## test: Run all tests
test:
	@echo "Running tests..."
	go test -v -race -cover ./...

## test-short: Run tests without race detector (faster)
test-short:
	@echo "Running tests (short)..."
	go test -v -short ./...

## bench: Run benchmarks
bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

## coverage: Generate coverage report
coverage:
	@echo "Generating coverage report..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"

## lint: Run golangci-lint (requires golangci-lint installed)
lint:
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Get it: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "✓ Code formatted"

## vet: Run go vet
vet:
	@echo "Running go vet..."
	go vet ./...
	@echo "✓ Vet passed"

## check-deps: Verify dependencies
check-deps:
	@echo "Checking dependencies..."
	@which rg > /dev/null || (echo "❌ ripgrep (rg) not found. Install: https://github.com/BurntSushi/ripgrep" && exit 1)
	@echo "✓ ripgrep installed"
	@[ -n "$$ANTHROPIC_API_KEY" ] || echo "⚠️  ANTHROPIC_API_KEY not set"

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html
	@echo "✓ Clean complete"

## dev: Build and run in TUI mode
dev: build
	./$(BINARY_NAME) --tui

## release: Build optimized release binary
release:
	@echo "Building release binary $(VERSION)..."
	CGO_ENABLED=1 go build -trimpath $(LDFLAGS) -o $(BINARY_NAME) $(CMD_PATH)
	@echo "✓ Release binary created: ./$(BINARY_NAME)"

## help: Show this help
help:
	@echo "mimicode build targets:"
	@echo ""
	@sed -n 's/^## //p' Makefile | column -t -s ':' | sed 's/^/  /'

.DEFAULT_GOAL := help
