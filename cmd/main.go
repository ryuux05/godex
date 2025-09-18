package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/ryuux05/indexer-sdk-go/pkg/core"
)

func main() {
	rpc := core.NewHTTPRPC("https://astar.stmnode.com/", 20)

	opts := core.Options{
		RangeSize:          10,
		BatchSize:          50,
		DecoderConcurrency: 4,
		FetcherConcurrency: 4,
		StartBlock:         214391,
		Confimation:        50,
		LogsBufferSize:     1024,
	}

	processor := core.NewProcessor(rpc, &opts)

	ctx, cancel := context.WithCancel(context.Background())
	go func(){ 
		err := processor.Run(ctx) 
		if err != nil {
			log.Println("run error:", err)
		}
	}()

	defer cancel()
	
	go func() {
		for {
			select {
			case <- ctx.Done():
				return
			case l, ok := <- processor.Logs():
				if !ok {
					return
				}
				log.Println(l)
			}
		}
		}()
		
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}

