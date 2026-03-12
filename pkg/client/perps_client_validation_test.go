package client

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

func TestPerpxPerpsClient_ValidateOrderParams_Valid(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	err := client.validateOrderParams(1, 1, 100, 123, 100, false)
	require.NoError(t, err)

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(0), metrics["invalid_order_params"])
}

func TestPerpxPerpsClient_ValidateOrderParams_FailuresIncrementMetric(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	cases := []struct {
		name string
		err  error
	}{
		{"zero_quantums", client.validateOrderParams(1, 0, 100, 1, 100, false)},
		{"zero_subticks", client.validateOrderParams(1, 1, 0, 1, 100, false)},
		{"subticks_oob_low", client.validateOrderParams(1, 1, 99, 1, 100, false)},
		{"subticks_oob_high", client.validateOrderParams(1, 1, 201, 1, 100, false)},
		{"quantums_oob_high_non_close", client.validateOrderParams(1, 11, 100, 1, 100, false)},
		{"good_til_block_zero", client.validateOrderParams(1, 1, 100, 1, 0, false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.err)
		})
	}

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(len(cases)), metrics["invalid_order_params"])
}

func TestPerpxPerpsClient_ValidateOrderParams_DuplicateClientID(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Track an order with clientID 7 on market 1.
	client.trackOrder(trackedOrder{ClobPairID: 1, ClientID: 7})

	err := client.validateOrderParams(1, 1, 100, 7, 100, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already in use")
}

func TestPerpxPerpsClient_ValidateOrderParams_CloseOrder_AllowsQuantumsOutsideBounds(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Market 1 bounds are [1,10]; close orders allow quantums outside bounds (but still >0).
	err := client.validateOrderParams(1, 50, 100, 999, 100, true)
	require.NoError(t, err)
}

func TestPerpxPerpsClient_ValidateOrderParams_MultipleMarketConfigs(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Add a second market with different bounds.
	scenario := client.strategy.Scenario()
	scenario.Markets = append(scenario.Markets, strategies.PerpsMarketConfig{
		ClobPairID:          2,
		Symbol:              "BTC-PERP",
		MinQuantityQuantums: 5,
		MaxQuantityQuantums: 6,
		MinSubticks:         1000,
		MaxSubticks:         1001,
	})

	// Validate against market 2 bounds.
	require.NoError(t, client.validateOrderParams(2, 5, 1000, 1, 100, false))
	require.Error(t, client.validateOrderParams(2, 4, 1000, 2, 100, false), "quantums too low")
	require.Error(t, client.validateOrderParams(2, 5, 999, 3, 100, false), "subticks too low")
}
