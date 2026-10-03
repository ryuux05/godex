# Variables
BINARY_NAME := godex
BINARY_PATH := bin/$(BINARY_NAME)
CMD_DIR := ./cmd/main
PKG := ./...

# Default target: build the project.
.PHONY: all
all: build

# Build the binary from the main package.
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	@go build -o $(BINARY_PATH) $(CMD_DIR) 
	@echo "Build complete: $(BINARY_PATH)"

# Run the built binary. If it doesn't exist, build it first.
.PHONY: run
	@go run ./cmd/main.go

# Run tests across all packages.
.PHONY: test
test:
	@echo "Running tests..."
	@go test -v $(PKG)

# CI uses an explicit POSTGRES_TEST_DSN so database tests cannot silently skip.
.PHONY: test-race test-postgres
test-race:
	@go test -race -coverprofile=coverage.out -covermode=atomic $(PKG)

test-postgres:
	@test -n "$$POSTGRES_TEST_DSN" || (echo "Set POSTGRES_TEST_DSN to an isolated test database"; exit 1)
	@go test -count=1 -v ./adapters/sink/postgres

# Format the code using go fmt.
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	@go fmt $(PKG)

# Run go vet for static analysis.
.PHONY: vet
vet:
	@echo "Running go vet..."
	@go vet $(PKG)

# Clean up build artifacts.
.PHONY: clean
clean:
	@echo "Cleaning up..."
	@rm -rf bin
	@echo "Clean complete."

# Database Migrate 
.PHONY: migrate
migrate:
	@echo "Running database migration..."
	@go run cmd/migrate/migrate.go -f $(f)
## ---------- Cross‑compile helpers ---------- ##
# Usage: make release (outputs binaries into build/)

PLATFORMS := linux/amd64 windows/amd64 darwin/amd64
release:
	@mkdir -p build
	@for platform in $(PLATFORMS); do \
	  OS=$${platform%/*}; ARCH=$${platform#*/}; \
	  OUT="build/$(APP)-$${OS}-$${ARCH}"; \
	  [ $$OS = windows ] && OUT="$$OUT.exe"; \
	  echo " → $$OS/$$ARCH"; \
	  GOOS=$$OS GOARCH=$$ARCH go build -ldflags "$(LDFLAGS)" -o $$OUT; \
	done

.PHONY: build run lint deps clean release
