package processor
import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
)


// Arbiter will process the fetch result from fetcher, and pass it to processWindow in orders
func (p *Processor) arbiter(ctx context.Context, chain*chainState, results <-chan FetchResult, head uint64) (<- chan struct{}, <- chan error) {
	// arbiter process the results in order concurrently as fetcher sends result
	// cancel the batch context when reorg happen
	// channel to block arbiter
	done := make(chan struct{})

	errCh := make(chan error, 1)
	
	go func() {
		defer func(){
			if r := recover(); r != nil {
                p.logger.Error("arbiter panic", slog.Any("recover", r))
                select {
                case errCh <- fmt.Errorf("arbiter panic: %v", r):
                default:
                }
            }
			close(done)
		}()

		// Window storing "from" block with result
		window := make(map[uint64]FetchResult)

		// Next is pointing the next block after the cursor
		next := chain.cursor.BlockNum + 1
		 
		// Progress logging ticker
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		
		// helper to emit error without blocking
        emitErr := func(e error) {
            select {
            case errCh <- e:
            default:
            }
        }

        // helper to process all contiguous windows starting at `next`
        drain := func() bool {
            for {
                // Respect cancellation while draining
                select {
                case <-ctx.Done():
                    return false
                default:
                }

                r, exists := window[next]
                if !exists {
                    return true
                }

                if err := p.processWindow(ctx, chain, r, head); err != nil {
                    emitErr(err)
                    return false
                }

                delete(window, next)
                next = r.Range.To + 1
            }
        }

		for {
			select {
			case <-ctx.Done():
				p.logger.Info("arbiter exit: context canceled")
				return 
			case <-ticker.C:
				p.logProgress(chain)
			case result, ok := <-results:
				if !ok {
					_ = drain()
					return
				}

				window[result.Range.From] = result
				
				if ok := drain(); !ok {
					return
				}
			}
		}
	}()
	return done, errCh
}

// Process the window that was created by arbiter.
// In here decoding, storing, reorg handling will happen.
func (p *Processor) processWindow(ctx context.Context, chain *chainState, result FetchResult, head uint64) error {
	from := result.Range.From
	to := result.Range.To

	// Reorg check
	// Get the blockhash and compare it with the stored blockhash
	expectedNext := chain.cursor.BlockNum + 1
	if from > 0 && from == expectedNext{
		fromHex := utils.Uint64ToHexQty(from)
		var block types.Block
		err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
			// make request context for each attempts
			reqCtx, cancel := context.WithTimeout(ctx, chain.opts.RetryConfig.PerRequestTimeout)
			defer cancel()

			b, err := chain.chainInfo.RPC.GetBlock(reqCtx, fromHex)
			if err != nil {
				return err
			}

			block = b
			return nil
		})

		if err != nil {
			return err
		}

		if err := p.detectReorg(ctx, chain, from, block); err != nil {
			return err
		}

	}

	// Decode logs
	events := p.decodeLogs(ctx, chain, result)

	// get the endblock to store in the window
	endBlock, err := p.getBlockWithRetry(ctx, to, chain)
	if err != nil {
    	return err
	}
	// Store if there is event
	// Else update the cursor
	if len(events) > 0 {
		if err := p.sink.Store(ctx, events); err != nil {
			return err
		} 

	} else {
		if err := p.sink.UpdateCursor(ctx, chain.chainInfo.ChainId, to, endBlock.Hash); err != nil {
			return err
		}
	}

	// update progress
	chain.progress.Update(to, chain.progress.eventsStored + uint64(len(events)))

	// store the end of window for reorg check
	chain.blockHashCache.Set(to, endBlock.Hash)
	
	// update the in-memory cursor
	chain.cursor.BlockNum = to
	chain.cursor.BlockHash = endBlock.Hash

	return nil
}

// function to decode log into events along with timestamp
// return empty events if there is no log
func (p *Processor) decodeLogs(ctx context.Context, chain *chainState, fetchResult FetchResult) []types.Event {
	// return immediately if there is no logs
	if len(fetchResult.Logs) <= 0 {
		return []types.Event{}
	}

	// storage to store decoded events
	events := make([]types.Event, 0, len(fetchResult.Logs))

	for _,l := range fetchResult.Logs {
		topic0 := ""
		if len(l.Topics) > 0 {
			topic0 = l.Topics[0]
		}

		p.logger.Debug("attempting decode",
			slog.String("address", l.Address),
			slog.String("topic0", topic0))

		// Decode
		event, err := p.router.Decode(chain.chainInfo.ChainId, l)
		if err != nil {
			p.logger.Warn("failed to decode log", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
			continue
		}

		// Get block timestamps
		if event != nil {
			blockNum, err := utils.HexQtyToUint64(l.BlockNumber)
			if err != nil {
				p.logger.Warn("failed to parse block number for timestamp", slog.Any("error", err))
			}
			// Check if timestamp is available
			if chain.opts.EnableTimestamps {
    			if ts, ok := fetchResult.Timestamps[blockNum]; ok {
        			event.Timestamp = ts
    			}
			}
			events = append(events, *event)
		}
	}

	return events
}

func (p *Processor) logProgress(chain *chainState) {

	// Take snapshot and log
	snapshot := chain.progress.Snapshot()
	status := "syncing"

	if chain.isLive {
		status = "live"
	}

	p.logger.Info(fmt.Sprintf("[%s] Block %s | %.1f%% | %.0f blk/s | ETA %s | %s events",
		chain.chainInfo.ChainId,
		utils.FormatNumber(snapshot.current),
		snapshot.progressPct,
		snapshot.blockPerSec,
		snapshot.eta,
		utils.FormatNumber(snapshot.events),
	), slog.String("status", status))

	// Reset window for next calculation
	chain.progress.ResetLogWindow()
}

