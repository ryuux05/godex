package core


type Processor struct {
	rpc *RPC `json:"rpc"` // Node where the indexer going to query
	opts *Options
}

func NewProcessor(rpc *RPC, opts *Options) *Processor {
	return &Processor{
		rpc: rpc,
		opts: opts,
	}
}


func 