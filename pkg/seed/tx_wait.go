package seed

import (
	"context"
	"fmt"
	"time"

	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func waitForTxInclusionGRPC(ctx context.Context, txClient txtypes.ServiceClient, txHash string, maxWait time.Duration) (*txtypes.GetTxResponse, error) {
	start := time.Now()
	for time.Since(start) < maxWait {
		resp, err := txClient.GetTx(ctx, &txtypes.GetTxRequest{Hash: txHash})
		if err == nil && resp != nil && resp.TxResponse != nil {
			return resp, nil
		}
		if err != nil {
			// Not found yet: keep polling.
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			// Some nodes return Unknown with "not found"; tolerate and keep polling.
			time.Sleep(500 * time.Millisecond)
			continue
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("transaction %s was not included in a block within %v (transaction may have failed or been rejected)", txHash, maxWait)
}

