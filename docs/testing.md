# Testing

Run the ordinary suite:

```sh
make test
```

Run race detection and produce `coverage.out`:

```sh
make test-race
go tool cover -func=coverage.out
go tool cover -html=coverage.out
```

## PostgreSQL integration tests

Database tests require `POSTGRES_TEST_DSN`. Without it, the integration tests
explicitly skip; sink validation tests still run. If it is set and the database
cannot be reached, tests fail. CI starts PostgreSQL and sets this variable before
running the race suite.

To use a disposable local database:

```sh
docker run --detach --rm --name godex-test-postgres \
  -e POSTGRES_PASSWORD=godex-test -e POSTGRES_DB=godex_test \
  -p 127.0.0.1:55439:5432 postgres:16-alpine
docker exec godex-test-postgres pg_isready -U postgres -d godex_test
export POSTGRES_TEST_DSN='postgres://postgres:godex-test@127.0.0.1:55439/godex_test?sslmode=disable'
make test-postgres
make test-race
docker stop godex-test-postgres
```

Wait until `pg_isready` reports that PostgreSQL is accepting connections. Each
integration test creates and drops its own schema; the configured database user
must be allowed to create schemas. Test cleanup truncates only tables in those
test schemas. Pools and schemas are cleaned up through `t.Cleanup`, including on
assertion failures.

The integration suite covers INSERT and COPY, handler transaction failures,
rollback boundaries, chain isolation, cursor upserts, missing cursors, failed
migrations, failed cursor updates after deletion, and duplicate event handling.
COPY currently rejects duplicate IDs while INSERT ignores them; the COPY test
verifies that the failed batch leaves stored events and its cursor intact.

## Fuzz tests

Seed inputs run with the normal test suite. To explore beyond them:

```sh
go test ./pkg/core/decoder -run '^$' -fuzz '^FuzzDecodeDynamicData$' -fuzztime=30s
go test ./pkg/core/decoder -run '^$' -fuzz '^FuzzDecodeLog$' -fuzztime=30s
go test ./pkg/core/utils -run '^$' -fuzz '^FuzzHexQuantityRoundTrip$' -fuzztime=30s
```

The decoder targets check that arbitrary input cannot panic. The quantity target
checks exact round trips across the full `uint64` range. Go saves failing inputs
under `testdata/fuzz`; retain them as regression cases when a failure is fixed.

## Test design

Processor unit tests use controllable RPC and sink fakes. They exercise ordering,
error propagation, cancellation, confirmed planning bounds, reorg recovery,
timestamps, receipt filters, and status reads during commits. Channels coordinate
concurrent failures; bounded timeouts detect hangs.

The public SDK smoke test exercises HTTP RPC, the ABI decoder and router, event
persistence through a sink, empty tail cursor advancement, and shutdown using
only exported APIs. It complements the database tests; it uses an in-memory sink.

Coverage measures executed statements, not every possible behavior. See
[the repository review](repository-review.md) for remaining risks and test gaps.
