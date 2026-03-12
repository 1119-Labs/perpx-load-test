package strategies

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	satypes "github.com/1119-Labs/perpx-chain/protocol/x/subaccounts/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// mockOrderTracker is a simple mock implementation of OrderTracker for testing.
type mockOrderTracker struct {
	orders []TrackedOrder
}

func (m *mockOrderTracker) GetRandomOrder() (TrackedOrder, bool) {
	if len(m.orders) == 0 {
		return TrackedOrder{}, false
	}
	idx := rand.Intn(len(m.orders))
	return m.orders[idx], true
}

func (m *mockOrderTracker) GetRandomOrdersForMarket(clobPairID uint32, maxCount int) []TrackedOrder {
	result := make([]TrackedOrder, 0, maxCount)
	for _, o := range m.orders {
		if o.ClobPairID == clobPairID {
			result = append(result, o)
			if len(result) >= maxCount {
				break
			}
		}
	}
	return result
}

func (m *mockOrderTracker) TrackOrder(order TrackedOrder) {
	m.orders = append(m.orders, order)
}

func (m *mockOrderTracker) RemoveOrder(order TrackedOrder) {
	for i, o := range m.orders {
		if o.ClientID == order.ClientID && o.ClobPairID == order.ClobPairID {
			m.orders = append(m.orders[:i], m.orders[i+1:]...)
			return
		}
	}
}

func (m *mockOrderTracker) RemoveOrders(orders []TrackedOrder) {
	for _, toRemove := range orders {
		m.RemoveOrder(toRemove)
	}
}

func (m *mockOrderTracker) HasOrders() bool {
	return len(m.orders) > 0
}

// testAddress is a known-valid Cosmos bech32 address for testing purposes.
// We keep this independent from any chain-specific bech32 prefix configuration.
const testAddress = "cosmos1pjtgu0vau2m52nrykdpztrt887aykue0hq7dfh"

func makeTestMarket() PerpsMarketConfig {
	return PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}
}

func makeTestStrategy(t *testing.T) *PerpsOrderStrategy {
	t.Helper()

	cfg := PerpsOrderStrategyConfig{
		ChainID:      "localperpxprotocol",
		Denom:        "aperpx",
		Markets:      []PerpsMarketConfig{makeTestMarket()},
		ScenarioName: ScenarioSimplePerps,
	}

	strat, err := NewPerpsOrderStrategy(cfg)
	require.NoError(t, err)
	return strat
}

func TestPerpsOrderStrategy_CreateMsg_ValidAddress(t *testing.T) {
	strat := makeTestStrategy(t)

	var nextCID uint32 = 1
	msg, err := strat.CreateMsg(testAddress, 0, nil, nil, &nextCID)
	require.NoError(t, err)
	require.NotNil(t, msg)

	// Ensure the address is considered valid by the SDK.
	_, err = sdk.AccAddressFromBech32(testAddress)
	require.NoError(t, err)
}

func TestPerpsOrderStrategy_PlaceOrderMessageValid(t *testing.T) {
	strat := makeTestStrategy(t)

	var nextCID uint32 = 1
	msg, err := strat.createMsgForAction(PerpsActionPlace, testAddress, 0, nil, nil, &nextCID)
	require.NoError(t, err)
	require.NotNil(t, msg)

	placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder)
	require.True(t, ok, "expected MsgPlaceOrder")
	require.NoError(t, placeMsg.ValidateBasic())
}

func TestPerpsOrderStrategy_CancelOrderMessageValid(t *testing.T) {
	strat := makeTestStrategy(t)

	// For cancel, we need an order tracker with at least one order
	// Create a simple mock tracker
	tracker := &mockOrderTracker{
		orders: []TrackedOrder{
			{
				OrderID: clobtypes.OrderId{
					SubaccountId: satypes.SubaccountId{
						Owner:  testAddress,
						Number: 0,
					},
					ClientId:   1,
					OrderFlags: clobtypes.OrderIdFlags_LongTerm,
					ClobPairId: 1,
				},
				ClobPairID: 1,
				ClientID:   1,
				OrderFlags: clobtypes.OrderIdFlags_LongTerm,
			},
		},
	}

	var nextCID uint32 = 1
	msg, err := strat.createMsgForAction(PerpsActionCancel, testAddress, 0, tracker, nil, &nextCID)
	require.NoError(t, err)
	require.NotNil(t, msg)

	cancelMsg, ok := msg.(*clobtypes.MsgCancelOrder)
	require.True(t, ok, "expected MsgCancelOrder")
	require.NoError(t, cancelMsg.ValidateBasic())
}

func TestPerpsOrderStrategy_CloseOrderMessageValid(t *testing.T) {
	strat := makeTestStrategy(t)

	var nextCID uint32 = 1
	msg, err := strat.createMsgForAction(PerpsActionClose, testAddress, 0, nil, nil, &nextCID)
	require.NoError(t, err)
	require.NotNil(t, msg)

	placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder)
	require.True(t, ok, "expected MsgPlaceOrder for close action")
	require.False(t, placeMsg.Order.ReduceOnly, "strategy avoids reduce-only for long-term/stateful close orders")
	require.NoError(t, placeMsg.ValidateBasic())
}
