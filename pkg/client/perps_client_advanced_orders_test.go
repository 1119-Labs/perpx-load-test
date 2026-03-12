package client

import (
	"testing"

	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
	"github.com/stretchr/testify/require"
)

func TestPerpxPerpsClient_TWAPOrder_IncludesTwapParametersAndUsesTwapFlag(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	scenario := client.strategy.Scenario()
	scenario.Actions = strategies.PerpsActionWeights{Place: 1}
	scenario.TWAPOrderProbability = 1.0
	scenario.TWAPIntervalSeconds = 60
	scenario.TWAPNumIntervals = 9
	// Make sure other branches can't steal this.
	scenario.LongTermOrderProbability = 1.0
	scenario.Markets[0].ConditionalOrderProbability = 1.0

	txBytes, err := client.GenerateTx()
	require.NoError(t, err)

	tx, err := client.encCfg.TxConfig.TxDecoder()(txBytes)
	require.NoError(t, err)
	require.Len(t, tx.GetMsgs(), 1)

	placeMsg, ok := tx.GetMsgs()[0].(*clobtypes.MsgPlaceOrder)
	require.True(t, ok)

	require.Equal(t, clobtypes.OrderIdFlags_Twap, placeMsg.Order.OrderId.OrderFlags)
	require.NotNil(t, placeMsg.Order.TwapParameters)
	require.Equal(t, uint32(60), placeMsg.Order.TwapParameters.Interval)
	require.Equal(t, uint32(60*9), placeMsg.Order.TwapParameters.Duration)
	require.Equal(t, uint32(0), placeMsg.Order.GetGoodTilBlock())
	require.Greater(t, placeMsg.Order.GetGoodTilBlockTime(), uint32(0))
}

func TestPerpxPerpsClient_AdvancedPreset_GeneratesAllAdvancedOrderTypesEventually(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Switch to advanced preset and bias to placing orders so we sample order-type selection.
	market := client.strategy.Scenario().Markets
	strategyCfg := strategies.PerpsOrderStrategyConfig{
		ChainID:      client.strategy.ChainID(),
		Denom:        client.strategy.Denom(),
		Markets:      market,
		ScenarioName: strategies.ScenarioAdvancedPerps,
		ActionWeightsOverride: strategies.PerpsActionWeights{
			Place: 1,
		},
	}
	strat, err := strategies.NewPerpsOrderStrategy(strategyCfg)
	require.NoError(t, err)
	client.strategy = strat

	seen := map[uint32]bool{}
	const n = 400
	for i := 0; i < n; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err)

		tx, err := client.encCfg.TxConfig.TxDecoder()(txBytes)
		require.NoError(t, err)
		require.Len(t, tx.GetMsgs(), 1)

		if placeMsg, ok := tx.GetMsgs()[0].(*clobtypes.MsgPlaceOrder); ok {
			seen[placeMsg.Order.OrderId.OrderFlags] = true
		}
	}

	require.True(t, seen[clobtypes.OrderIdFlags_LongTerm], "expected to eventually see long-term orders")
	require.True(t, seen[clobtypes.OrderIdFlags_Conditional], "expected to eventually see conditional orders")
	require.True(t, seen[clobtypes.OrderIdFlags_Twap], "expected to eventually see TWAP orders")
}


