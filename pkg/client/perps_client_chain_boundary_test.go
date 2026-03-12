package client

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

// spyChainAPI wraps an underlying encoder and records whether it was used.
type spyChainAPI struct {
	inner  sdk.TxEncoder
	called bool
}

func (s *spyChainAPI) TxEncoder() sdk.TxEncoder {
	return func(tx sdk.Tx) ([]byte, error) {
		s.called = true
		return s.inner(tx)
	}
}

// TestPerpxPerpsClient_UsesChainBoundaryForEncoding ensures that transaction
// encoding goes through the PerpsChainAPI boundary rather than calling the
// encoding config directly.
func TestPerpxPerpsClient_UsesChainBoundaryForEncoding(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Replace the client's chainAPI with a spy that wraps the real encoder.
	encCfg := client.encCfg
	spy := &spyChainAPI{inner: encCfg.TxConfig.TxEncoder()}
	client.chainAPI = spy

	// Generate a single transaction.
	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.NotEmpty(t, txBytes)

	// The spy must have been invoked, proving the boundary was used.
	require.True(t, spy.called, "expected PerpxPerpsClient to use PerpsChainAPI.TxEncoder for encoding")
}

