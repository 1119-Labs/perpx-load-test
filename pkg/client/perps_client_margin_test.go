package client

import (
	"testing"

	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	sendingtypes "github.com/1119-Labs/perpx-chain/protocol/x/sending/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

func TestPerpxPerpsClient_CheckMarginSufficient_Disabled(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	client.marginConfig.Enabled = false

	require.True(t, client.CheckMarginSufficient(1, 999999, 999999))
}

func TestPerpxPerpsClient_CheckMarginSufficient_Enabled_NoPosition(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	client.marginConfig.Enabled = true

	// With no tracked position, heuristic returns sufficient.
	require.True(t, client.CheckMarginSufficient(1, 1000, 200))
}

func TestPerpxPerpsClient_CheckMarginSufficient_Enabled_HeuristicInsufficient(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	client.marginConfig.Enabled = true

	// Seed a small position value.
	client.posMtx.Lock()
	client.positions[1] = &trackedPosition{
		ClobPairID: 1,
		Side:       clobtypes.Order_SIDE_BUY,
		Size:       10,
		EntryPrice: 100, // positionValue=1000
	}
	client.posMtx.Unlock()

	// orderValue = 20000 > positionValue*10 (=10000) => insufficient
	require.False(t, client.CheckMarginSufficient(1, 100, 200))
}

func TestPerpxPerpsClient_HandleInsufficientMargin_Skip(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	client.marginConfig.Enabled = true
	client.marginConfig.OnInsufficient = "skip"

	msg, err := client.HandleInsufficientMargin()
	require.NoError(t, err)
	require.IsType(t, &banktypes.MsgSend{}, msg)
}

func TestPerpxPerpsClient_HandleInsufficientMargin_Deposit(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	client.marginConfig.Enabled = true
	client.marginConfig.OnInsufficient = "deposit"

	msg, err := client.HandleInsufficientMargin()
	require.NoError(t, err)
	require.IsType(t, &sendingtypes.MsgDepositToSubaccount{}, msg)
}

func TestPerpxPerpsClient_HandleInsufficientMargin_ClosePosition(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	client.marginConfig.Enabled = true
	client.marginConfig.OnInsufficient = "close"

	// Seed a position so "close" has something to act on.
	client.posMtx.Lock()
	client.positions[1] = &trackedPosition{
		ClobPairID: 1,
		Side:       clobtypes.Order_SIDE_BUY,
		Size:       5,
		EntryPrice: 150,
	}
	client.posMtx.Unlock()

	msg, err := client.HandleInsufficientMargin()
	require.NoError(t, err)
	require.IsType(t, &clobtypes.MsgPlaceOrder{}, msg)

	place := msg.(*clobtypes.MsgPlaceOrder)
	require.False(t, place.Order.ReduceOnly, "strategy uses non-reduce-only close orders to satisfy long-term/stateful validation")
}


