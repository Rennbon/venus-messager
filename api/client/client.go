package client

import (
	"context"
	"github.com/filecoin-project/go-jsonrpc"
	"net/http"
)

// NewCommonRPC creates a new http jsonrpc client.
// addr must start with http or https
func NewMessageRPC(ctx context.Context, addr string, requestHeader http.Header) (IMessager, jsonrpc.ClientCloser, error) {
	var res Message
	closer, err := jsonrpc.NewMergeClient(ctx, addr, "Message",
		[]interface{}{
			&res.Internal,
		},
		requestHeader,
	)

	return &res, closer, err
}
