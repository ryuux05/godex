PKG := ./...

.PHONY: all build run test test-race test-postgres fmt vet clean
all: build

# Godex is a library; compile it and all example applications.
build:
	go build $(PKG)

# Requires the environment and database setup described by the example.
run:
	go run ./examples/erc20-indexer

test:
	go test -v -timeout=2m $(PKG)

# CI supplies POSTGRES_TEST_DSN so integration tests cannot silently skip.
test-race:
	go test -race -timeout=2m -coverprofile=coverage.out -covermode=atomic $(PKG)

test-postgres:
	@test -n "$$POSTGRES_TEST_DSN" || (echo "Set POSTGRES_TEST_DSN to an isolated test database"; exit 1)
	go test -count=1 -timeout=2m -v ./adapters/sink/postgres

fmt:
	go fmt $(PKG)

vet:
	go vet $(PKG)

clean:
	rm -f coverage.out
