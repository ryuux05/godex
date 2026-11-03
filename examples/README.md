# Godex examples

Run examples from the repository root.

| Example | Use case | Run |
| --- | --- | --- |
| [Service integration](../docs/sdk.md#runnable-service-example) | Store selected contract events using an application's PostgreSQL pool and HTTP client. | `go run ./examples/service` |
| [ERC20 indexer](erc20-indexer/) | Track transfers, approvals, transfer statistics, and holder activity. | `go run ./examples/erc20-indexer` |
| [Swap indexer](swap-indexer/) | Index Uniswap pool and swap activity across chains. | `go run ./examples/swap-indexer` |
| [Delegation tracker](delegation-tracker/) | Track governance delegates, voting-power rankings, and delegation history with reorg recovery. | `go run ./examples/delegation-tracker` |
| [NFT indexer](nft-indexer/) | Track ERC721 owners, wallet inventories, holder balances, and mint/burn history with reorg recovery. | `go run ./examples/nft-indexer` |
| [Payment reconciler](payment-reconciler/) | Match stablecoin receipts to checkout invoices, including partial payments, overpayments, and reorg recovery. | `go run ./examples/payment-reconciler watch` |

The delegation tracker, NFT indexer, and payment reconciler include PostgreSQL
setup and SQL queries. The payment reconciler also includes commands to register
invoices and read their status. See [SDK configuration](../docs/sdk.md) to adapt
an example to your service and [testing](../docs/testing.md) to run the integration
suite.
