package client

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// PerpsChainAPI defines the narrow surface that the perps loadtest client
// relies on from the underlying PerpX chain. Keeping this interface small and
// stable makes it easier to adapt to future chain upgrades or SDK changes.
//
// The initial implementation is intentionally minimal and focuses on tx
// encoding. Additional methods (e.g. for market data) can be added over time
// without forcing callers outside this package to change.
type PerpsChainAPI interface {
	// TxEncoder returns the SDK transaction encoder used to generate raw
	// transaction bytes for broadcasting.
	TxEncoder() sdk.TxEncoder
}

// defaultPerpsChainAPI is the production implementation of PerpsChainAPI that
// wraps the application's encoding config. It allows tests to inject their own
// implementations if they need to assert on encoded transactions directly.
type defaultPerpsChainAPI struct {
	encCfgEncodingTx sdk.TxEncoder
}

// TxEncoder implements PerpsChainAPI.
func (a defaultPerpsChainAPI) TxEncoder() sdk.TxEncoder {
	return a.encCfgEncodingTx
}

