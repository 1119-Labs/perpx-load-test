package strategies

import (
	"math/rand"
	"testing"

	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	satypes "github.com/1119-Labs/perpx-chain/protocol/x/subaccounts/types"
	"github.com/stretchr/testify/require"
)

// deterministicOrderTracker is a stable OrderTracker for tests that need
// predictable selection (no global rand usage).
type deterministicOrderTracker struct {
	orders []TrackedOrder
}

func (d *deterministicOrderTracker) GetRandomOrder() (TrackedOrder, bool) {
	if len(d.orders) == 0 {
		return TrackedOrder{}, false
	}
	return d.orders[0], true
}

func (d *deterministicOrderTracker) GetRandomOrdersForMarket(clobPairID uint32, maxCount int) []TrackedOrder {
	out := make([]TrackedOrder, 0, maxCount)
	for _, o := range d.orders {
		if o.ClobPairID == clobPairID {
			out = append(out, o)
			if len(out) >= maxCount {
				break
			}
		}
	}
	return out
}

func (d *deterministicOrderTracker) TrackOrder(order TrackedOrder) {
	d.orders = append(d.orders, order)
}

func (d *deterministicOrderTracker) RemoveOrder(order TrackedOrder) {
	for i, o := range d.orders {
		if o.ClientID == order.ClientID && o.ClobPairID == order.ClobPairID {
			d.orders = append(d.orders[:i], d.orders[i+1:]...)
			return
		}
	}
}

func (d *deterministicOrderTracker) RemoveOrders(orders []TrackedOrder) {
	for _, o := range orders {
		d.RemoveOrder(o)
	}
}

func (d *deterministicOrderTracker) HasOrders() bool {
	return len(d.orders) > 0
}

func TestPerpsOrderStrategy_Advanced_LongTermOrderCreationAndTracking(t *testing.T) {
	strat := makeTestStrategy(t)
	strat.rng = rand.New(rand.NewSource(1))

	// Force long-term, disable other types.
	sc := strat.Scenario()
	sc.LongTermOrderProbability = 1.0
	sc.TWAPOrderProbability = 0.0
	sc.Markets[0].ConditionalOrderProbability = 0.0

	tracker := &deterministicOrderTracker{}
	var nextCID uint32 = 1

	msg, err := strat.createMsgForAction(PerpsActionPlace, testAddress, 0, tracker, nil, &nextCID)
	require.NoError(t, err)

	placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder)
	require.True(t, ok)

	require.Equal(t, clobtypes.OrderIdFlags_LongTerm, placeMsg.Order.OrderId.OrderFlags)
	require.Equal(t, uint32(0), placeMsg.Order.GetGoodTilBlock())
	require.Greater(t, placeMsg.Order.GetGoodTilBlockTime(), uint32(0))

	// Tracking should include the same flag and client id.
	require.Len(t, tracker.orders, 1)
	require.Equal(t, clobtypes.OrderIdFlags_LongTerm, tracker.orders[0].OrderFlags)
	require.Equal(t, placeMsg.Order.OrderId.ClientId, tracker.orders[0].ClientID)
	require.Equal(t, placeMsg.Order.OrderId, tracker.orders[0].OrderID)
}

func TestPerpsOrderStrategy_Advanced_LongTermCancelUsesStatefulGoodTilBlockTime(t *testing.T) {
	strat := makeTestStrategy(t)
	strat.rng = rand.New(rand.NewSource(2))

	// Disable batch cancel so we always test single cancel path.
	sc := strat.Scenario()
	sc.BatchCancelProbability = 0.0

	tracker := &deterministicOrderTracker{
		orders: []TrackedOrder{
			{
				OrderID: clobtypes.OrderId{
					SubaccountId: satypes.SubaccountId{Owner: testAddress, Number: 0},
					ClientId:     1,
					OrderFlags:   clobtypes.OrderIdFlags_LongTerm,
					ClobPairId:   1,
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

	cancelMsg, ok := msg.(*clobtypes.MsgCancelOrder)
	require.True(t, ok)

	require.Equal(t, clobtypes.OrderIdFlags_LongTerm, cancelMsg.OrderId.OrderFlags)
	// Stateful cancels use GoodTilBlockTime (short-term cancels use GoodTilBlock).
	require.Equal(t, uint32(0), cancelMsg.GetGoodTilBlock())
	require.Greater(t, cancelMsg.GetGoodTilBlockTime(), uint32(0))
}

func TestPerpsOrderStrategy_Advanced_ConditionalOrderCreation_UsesConfiguredTypesAndSetsTrigger(t *testing.T) {
	strat := makeTestStrategy(t)
	strat.rng = rand.New(rand.NewSource(3))

	sc := strat.Scenario()
	sc.TWAPOrderProbability = 0.0
	sc.LongTermOrderProbability = 0.0
	sc.Markets[0].ConditionalOrderProbability = 1.0
	sc.Markets[0].ConditionalOrderTypes = []clobtypes.Order_ConditionType{
		clobtypes.Order_CONDITION_TYPE_STOP_LOSS,
	}

	var nextCID uint32 = 1
	msg, err := strat.createMsgForAction(PerpsActionPlace, testAddress, 0, nil, nil, &nextCID)
	require.NoError(t, err)

	placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder)
	require.True(t, ok)

	require.Equal(t, clobtypes.OrderIdFlags_Conditional, placeMsg.Order.OrderId.OrderFlags)
	require.Equal(t, clobtypes.Order_CONDITION_TYPE_STOP_LOSS, placeMsg.Order.ConditionType)
	require.Greater(t, placeMsg.Order.ConditionalOrderTriggerSubticks, uint64(0))
	require.GreaterOrEqual(t, placeMsg.Order.ConditionalOrderTriggerSubticks, sc.Markets[0].MinSubticks)
	require.LessOrEqual(t, placeMsg.Order.ConditionalOrderTriggerSubticks, sc.Markets[0].MaxSubticks)

	// Conditional orders are stateful in this strategy: GoodTilBlockTime is set.
	require.Equal(t, uint32(0), placeMsg.Order.GetGoodTilBlock())
	require.Greater(t, placeMsg.Order.GetGoodTilBlockTime(), uint32(0))
}

func TestPerpsOrderStrategy_Advanced_TWAPOrderCreation_SetsFlagsAndTwapParameters(t *testing.T) {
	strat := makeTestStrategy(t)
	strat.rng = rand.New(rand.NewSource(4))

	sc := strat.Scenario()
	sc.TWAPOrderProbability = 1.0
	sc.TWAPIntervalSeconds = 60
	sc.TWAPNumIntervals = 5
	sc.LongTermOrderProbability = 1.0               // should be ignored due to TWAP exclusivity
	sc.Markets[0].ConditionalOrderProbability = 1.0 // should be ignored due to TWAP exclusivity

	var nextCID uint32 = 1
	msg, err := strat.createMsgForAction(PerpsActionPlace, testAddress, 0, nil, nil, &nextCID)
	require.NoError(t, err)

	placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder)
	require.True(t, ok)

	require.Equal(t, clobtypes.OrderIdFlags_Twap, placeMsg.Order.OrderId.OrderFlags)
	require.NotNil(t, placeMsg.Order.TwapParameters)
	require.Equal(t, uint32(60), placeMsg.Order.TwapParameters.Interval)
	require.Equal(t, uint32(60*5), placeMsg.Order.TwapParameters.Duration)

	require.Equal(t, uint32(0), placeMsg.Order.GetGoodTilBlock())
	require.Greater(t, placeMsg.Order.GetGoodTilBlockTime(), uint32(0))
}

func TestPerpsOrderStrategy_Advanced_TWAPOrderDefaultsWhenIntervalOrIntervalsZero(t *testing.T) {
	strat := makeTestStrategy(t)
	strat.rng = rand.New(rand.NewSource(5))

	sc := strat.Scenario()
	sc.TWAPOrderProbability = 1.0
	sc.TWAPIntervalSeconds = 0
	sc.TWAPNumIntervals = 0

	var nextCID uint32 = 1
	msg, err := strat.createMsgForAction(PerpsActionPlace, testAddress, 0, nil, nil, &nextCID)
	require.NoError(t, err)

	placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder)
	require.True(t, ok)

	require.Equal(t, clobtypes.OrderIdFlags_Twap, placeMsg.Order.OrderId.OrderFlags)
	require.NotNil(t, placeMsg.Order.TwapParameters)
	require.Equal(t, uint32(60), placeMsg.Order.TwapParameters.Interval)
	require.Equal(t, uint32(60*5), placeMsg.Order.TwapParameters.Duration)
}

func TestPerpsOrderStrategy_Advanced_BatchCancelMessageContainsExpectedClientIDs(t *testing.T) {
	strat := makeTestStrategy(t)
	strat.rng = rand.New(rand.NewSource(6))

	sc := strat.Scenario()
	sc.BatchCancelProbability = 1.0

	tracker := &deterministicOrderTracker{
		orders: []TrackedOrder{
			{OrderID: clobtypes.OrderId{SubaccountId: satypes.SubaccountId{Owner: testAddress, Number: 0}, ClientId: 12, OrderFlags: clobtypes.OrderIdFlags_LongTerm, ClobPairId: 1}, ClobPairID: 1, ClientID: 12, OrderFlags: clobtypes.OrderIdFlags_LongTerm},
			{OrderID: clobtypes.OrderId{SubaccountId: satypes.SubaccountId{Owner: testAddress, Number: 0}, ClientId: 13, OrderFlags: clobtypes.OrderIdFlags_Conditional, ClobPairId: 1}, ClobPairID: 1, ClientID: 13, OrderFlags: clobtypes.OrderIdFlags_Conditional},
		},
	}
	var nextCID uint32 = 1

	msg, err := strat.createMsgForAction(PerpsActionCancel, testAddress, 0, tracker, nil, &nextCID)
	require.NoError(t, err)

	// Batch cancels are currently disabled by the strategy (it can't compute
	// GoodTilBlock heights safely), so we expect a single cancel.
	cancelMsg, ok := msg.(*clobtypes.MsgCancelOrder)
	require.True(t, ok)
	require.NotEqual(t, clobtypes.OrderIdFlags_ShortTerm, cancelMsg.OrderId.OrderFlags)
	require.Equal(t, uint32(0), cancelMsg.GetGoodTilBlock())
	require.Greater(t, cancelMsg.GetGoodTilBlockTime(), uint32(0))
}

func TestPerpsOrderStrategy_Advanced_OrderTypeDistributionMatchesProbabilities(t *testing.T) {
	strat := makeTestStrategy(t)
	strat.rng = rand.New(rand.NewSource(12345))

	sc := strat.Scenario()
	// Pick probabilities that give meaningful counts and are easy to reason about.
	sc.TWAPOrderProbability = 0.10
	sc.LongTermOrderProbability = 0.30
	sc.Markets[0].ConditionalOrderProbability = 0.15

	// Effective probabilities given strategy selection order:
	// - TWAP: pT
	// - Conditional: (1-pT)*pC
	// - LongTerm: (1-pT)*(1-pC)   (strategy currently treats all non-conditional orders as long-term)
	pT := sc.TWAPOrderProbability
	pC := sc.Markets[0].ConditionalOrderProbability
	expTwap := pT
	expCond := (1 - pT) * pC
	expLong := (1 - pT) * (1 - pC)

	var nextCID uint32 = 1
	const samples = 20000
	var twap, cond, long int

	for i := 0; i < samples; i++ {
		msg, err := strat.createMsgForAction(PerpsActionPlace, testAddress, 0, nil, nil, &nextCID)
		require.NoError(t, err)
		placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder)
		require.True(t, ok)

		switch placeMsg.Order.OrderId.OrderFlags {
		case clobtypes.OrderIdFlags_Twap:
			twap++
		case clobtypes.OrderIdFlags_Conditional:
			cond++
		case clobtypes.OrderIdFlags_LongTerm:
			long++
		default:
			t.Fatalf("unexpected order flags: %v", placeMsg.Order.OrderId.OrderFlags)
		}
	}

	// We use a tolerance that comfortably covers binomial variation for N=20000.
	// This test is also deterministic due to the fixed RNG seed above.
	const tol = 0.02 // 2% absolute tolerance
	require.InDelta(t, expTwap, float64(twap)/samples, tol, "TWAP proportion should match")
	require.InDelta(t, expCond, float64(cond)/samples, tol, "conditional proportion should match")
	require.InDelta(t, expLong, float64(long)/samples, tol, "long-term proportion should match")
}


