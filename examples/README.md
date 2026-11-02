# Godex examples

Run examples from the repository root.

| Example | Use case | Run |
| --- | --- | --- |
| [Service integration](../docs/sdk.md#runnable-service-example) | Store selected contract events using an application's PostgreSQL pool and HTTP client. | `go run ./examples/service` |
| [ERC20 indexer](erc20-indexer/) | Track transfers, approvals, transfer statistics, and holder activity. | `go run ./examples/erc20-indexer` |
| [Swap indexer](swap-indexer/) | Index Uniswap pool and swap activity across chains. | `go run ./examples/swap-indexer` |
| [Delegation tracker](delegation-tracker/) | Track governance delegates, voting-power rankings, and delegation history with reorg recovery. | `go run ./examples/delegation-tracker` |

The delegation tracker includes PostgreSQL setup and SQL queries for current and
historical state. See [SDK configuration](../docs/sdk.md) to adapt an example to
your service and [testing](../docs/testing.md) to run the integration suite.
