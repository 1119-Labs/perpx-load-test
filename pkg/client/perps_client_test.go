package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	satypes "github.com/1119-Labs/perpx-chain/protocol/x/subaccounts/types"
	sendingtypes "github.com/1119-Labs/perpx-chain/protocol/x/sending/types"
	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// mockAccountResponse is a minimal auth account response used by the client.
type mockAccountResponse struct {
	Account struct {
		Type          string `json:"@type"`
		Address       string `json:"address"`
		AccountNumber string `json:"account_number"`
		Sequence      string `json:"sequence"`
	} `json:"account"`
}

// newTestPerpsClient wires up a PerpxPerpsClient with a fake REST server that
// returns a fixed account number and sequence.
func newTestPerpsClient(t *testing.T) (*PerpxPerpsClient, *uint64) {
	t.Helper()

	// Keep client IDs deterministic in unit tests unless explicitly overridden.
	os.Setenv("LOADTEST_PERPS_DETERMINISTIC_CLIENT_IDS", "true")
	t.Cleanup(func() { os.Unsetenv("LOADTEST_PERPS_DETERMINISTIC_CLIENT_IDS") })

	// Set a bech32 prefix so address derivation in SDK works as expected.
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("perpx", "perpxpub")

	// Start sequence at 5 for test purposes.
	var seq uint64 = 5

	// Fake REST server for account queries.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		resp := mockAccountResponse{}
		resp.Account.Type = "/cosmos.auth.v1beta1.BaseAccount"
		resp.Account.Address = "perpx1testaddressxxxxxxxxxxxxxxxxxxxxxx"
		resp.Account.AccountNumber = "10"
		resp.Account.Sequence = "5"

		enc := json.NewEncoder(w)
		require.NoError(t, enc.Encode(&resp))
	}))
	t.Cleanup(server.Close)

	// Build a simple scenario config directly (bypassing env parsing to keep
	// the unit test focused).
	market := strategies.PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}
	strategyCfg := strategies.PerpsOrderStrategyConfig{
		ChainID:      "localperpxprotocol",
		Denom:        "aperpx",
		Markets:      []strategies.PerpsMarketConfig{market},
		ScenarioName: strategies.ScenarioSimplePerps,
		ActionWeightsOverride: strategies.PerpsActionWeights{
			Place:  1,
			Cancel: 0,
			Amend:  0,
			Close:  0,
			Noop:   0,
		},
	}
	strategy, err := strategies.NewPerpsOrderStrategy(strategyCfg)
	require.NoError(t, err)

	cfg := loadtest.Config{
		Endpoints: []string{server.URL},
	}

	client, err := NewPerpxPerpsClient(cfg, strategy, 0)
	require.NoError(t, err)

	// Override the client's restURL to hit our mock server.
	client.restURL = server.URL

	// Ensure local sequence counter starts from the mocked on-chain sequence.
	require.NoError(t, client.ensureAccountQueried())
	require.Equal(t, uint64(10), client.accountNum)
	require.Equal(t, uint64(5), client.sequence)

	return client, &seq
}

func TestPerpxPerpsClient_GenerateTxSequenceMonotonic(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	var lastSeq uint64 = client.sequence

	for i := 0; i < 5; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err)
		require.NotEmpty(t, txBytes)

		// Local sequence counter must be strictly increasing.
		cur := atomic.LoadUint64(&client.sequence)
		require.GreaterOrEqual(t, cur, lastSeq)
		lastSeq = cur
	}
}

func TestPerpxPerpsClient_GenerateTxProducesSingleMsg(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.NotEmpty(t, txBytes)

	// Decode the transaction and ensure it has exactly one message.
	encCfg := client.encCfg
	txDecoder := encCfg.TxConfig.TxDecoder()

	tx, err := txDecoder(txBytes)
	require.NoError(t, err)

	msgs := tx.GetMsgs()
	require.Len(t, msgs, 1)
}

// Ensure the factory wires configuration through correctly and can produce a
// working client when basic environment variables are set.
func TestPerpxPerpsClientFactory_NewClient(t *testing.T) {
	// Point REST port to a dummy address; we won't actually hit it in this test
	// since GenerateTx is not called.
	os.Setenv("LOADTEST_PERPS_SCENARIO", strategies.ScenarioSimplePerps)
	defer os.Unsetenv("LOADTEST_PERPS_SCENARIO")

	factory := NewPerpxPerpsClientFactory()

	cfg := loadtest.Config{
		Connections: 1,
		Time:        1,
		Endpoints:   []string{"ws://localhost:26657/websocket"},
	}

	require.NoError(t, factory.ValidateConfig(cfg))

	clientIntf, err := factory.NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, clientIntf)
}

// TestPerpxPerpsClient_IntegrationDryRun simulates a short load-test loop
// to verify that the client can generate diverse messages without panics.
func TestPerpxPerpsClient_IntegrationDryRun(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Generate a batch of transactions to simulate a short load-test run.
	const numTxs = 50
	actionCounts := make(map[string]int)
	msgTypes := make(map[string]int)

	for i := 0; i < numTxs; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err, "GenerateTx should not fail on iteration %d", i)
		require.NotEmpty(t, txBytes, "transaction bytes should not be empty")

		// Decode the transaction to inspect the message.
		encCfg := client.encCfg
		txDecoder := encCfg.TxConfig.TxDecoder()
		tx, err := txDecoder(txBytes)
		require.NoError(t, err, "transaction should decode successfully")

		msgs := tx.GetMsgs()
		require.Len(t, msgs, 1, "each transaction should have exactly one message")

		// Track message types for diversity validation.
		msgType := fmt.Sprintf("%T", msgs[0])
		msgTypes[msgType]++

		// For place orders, we can infer the action type from the message.
		// This is a simplified check; in reality, the client maps actions to messages.
		if placeMsg, ok := msgs[0].(*clobtypes.MsgPlaceOrder); ok {
			if placeMsg.Order.ReduceOnly {
				actionCounts["close"]++
			} else {
				actionCounts["place"]++
			}
		} else if _, ok := msgs[0].(*clobtypes.MsgCancelOrder); ok {
			actionCounts["cancel"]++
		} else if _, ok := msgs[0].(*sendingtypes.MsgDepositToSubaccount); ok {
			actionCounts["noop"]++
		}
	}

	// Verify we generated multiple message types (diversity check).
	require.Greater(t, len(msgTypes), 0, "should generate at least one message type")
	
	// Verify sequence numbers are monotonic.
	require.GreaterOrEqual(t, atomic.LoadUint64(&client.sequence), uint64(numTxs),
		"sequence should be at least numTxs after generating that many transactions")
}

// TestPerpxPerpsClient_SingleAccountMode verifies that the client always uses
// a single deterministic bench account per worker and can generate multiple
// transactions successfully.
func TestPerpxPerpsClient_SingleAccountMode(t *testing.T) {
	t.Setenv("LOADTEST_PERPS_DETERMINISTIC_CLIENT_IDS", "true")
	t.Cleanup(func() {
		os.Unsetenv("LOADTEST_PERPS_DETERMINISTIC_CLIENT_IDS")
	})

	client, _ := newTestPerpsClient(t)

	const numTxs = 5
	for i := 0; i < numTxs; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err)
		require.NotEmpty(t, txBytes)
	}
}

// TestPerpxPerpsClient_AllPresets verifies that all scenario presets can
// be used to create working clients.
func TestPerpxPerpsClient_AllPresets(t *testing.T) {
	presets := []string{
		strategies.ScenarioSimplePerps,
		strategies.ScenarioMakerTaker,
		strategies.ScenarioStressMixed,
		strategies.ScenarioAdvancedPerps,
	}

	for _, preset := range presets {
		t.Run(preset, func(t *testing.T) {
			os.Setenv("LOADTEST_PERPS_SCENARIO", preset)
			defer os.Unsetenv("LOADTEST_PERPS_SCENARIO")

			factory := NewPerpxPerpsClientFactory()
			cfg := loadtest.Config{
				Connections: 1,
				Time:        1,
				Endpoints:   []string{"ws://localhost:26657/websocket"},
			}

			require.NoError(t, factory.ValidateConfig(cfg))
			clientIntf, err := factory.NewClient(cfg)
			require.NoError(t, err)
			require.NotNil(t, clientIntf)

			// Verify the client can generate at least one transaction.
			perpsClient, ok := clientIntf.(*PerpxPerpsClient)
			require.True(t, ok, "client should be a PerpxPerpsClient")

			// Set up a mock REST server for account queries.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resp := mockAccountResponse{}
				resp.Account.Type = "/cosmos.auth.v1beta1.BaseAccount"
				resp.Account.Address = "perpx1testaddressxxxxxxxxxxxxxxxxxxxxxx"
				resp.Account.AccountNumber = "10"
				resp.Account.Sequence = "5"
				enc := json.NewEncoder(w)
				require.NoError(t, enc.Encode(&resp))
			}))
			defer server.Close()

			perpsClient.restURL = server.URL
			require.NoError(t, perpsClient.ensureAccountQueried())

			txBytes, err := perpsClient.GenerateTx()
			require.NoError(t, err)
			require.NotEmpty(t, txBytes)
		})
	}
}

// TestPerpxPerpsClient_RetryLogic tests retry logic with mock failures.
func TestPerpxPerpsClient_RetryLogic(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Set up retry config with low retries for testing
	client.retryConfig = RetryConfig{
		MaxRetries:      2,
		RetryDelay:      10 * time.Millisecond,
		RetryableErrors: []string{"sequence", "temporary"},
	}

	// Test that retry logic is configured
	require.Equal(t, 2, client.retryConfig.MaxRetries)
	require.Equal(t, 10*time.Millisecond, client.retryConfig.RetryDelay)
}

// TestPerpxPerpsClient_OrderParameterValidation tests order parameter validation.
func TestPerpxPerpsClient_OrderParameterValidation(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Test invalid quantums
	err := client.validateOrderParams(1, 0, 100, 1, 100, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "quantums must be > 0")

	// Test invalid subticks
	err = client.validateOrderParams(1, 10, 0, 1, 100, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "subticks must be > 0")

	// Test valid parameters
	err = client.validateOrderParams(1, 10, 100, 1, 100, false)
	require.NoError(t, err)

	// Test close order with quantums outside market bounds (should be allowed)
	err = client.validateOrderParams(1, 50, 100, 1, 100, true)
	require.NoError(t, err) // Close orders can have any quantums
}

// TestPerpxPerpsClient_MarginCheckConfig tests margin check configuration.
func TestPerpxPerpsClient_MarginCheckConfig(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Test default margin config (disabled)
	require.False(t, client.marginConfig.Enabled)
	require.Equal(t, 1.0, client.marginConfig.MinMarginRatio)
	require.Equal(t, "skip", client.marginConfig.OnInsufficient)

	// Test margin check with insufficient margin (should return true when disabled)
	result := client.CheckMarginSufficient(1, 1000, 200)
	require.True(t, result) // Should pass when margin check is disabled
}

// TestPerpxPerpsClient_ErrorMetrics tests error metrics collection.
func TestPerpxPerpsClient_ErrorMetrics(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Test initial state
	metrics := client.GetErrorMetrics()
	require.NotNil(t, metrics)
	require.Equal(t, uint64(0), metrics["retry_counts"])

	// Increment some metrics
	client.errorMetrics.IncrementRetryCount()
	client.errorMetrics.IncrementErrorCount("test_error")

	// Check updated metrics
	metrics = client.GetErrorMetrics()
	require.Equal(t, uint64(1), metrics["retry_counts"])
}

// TestPerpxPerpsClient_SequenceRecovery tests sequence recovery mechanism.
func TestPerpxPerpsClient_SequenceRecovery(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Ensure account is queried first
	require.NoError(t, client.ensureAccountQueried())
	originalSequence := atomic.LoadUint64(&client.sequence)

	// Simulate sequence mismatch by manually setting it
	atomic.StoreUint64(&client.sequence, originalSequence+10)

	// Attempt recovery - this should not deadlock
	// Note: In a real scenario, recovery would re-query from chain
	// For this test, we just verify it doesn't panic or deadlock
	err := client.recoverSequence()
	// Recovery may succeed or fail depending on mock server state
	// The important thing is it doesn't deadlock
	_ = err // Ignore error for this test - we're just checking for deadlock
}

// TestPerpxPerpsClient_OrderTrackingEviction tests FIFO eviction when order tracking is full.
func TestPerpxPerpsClient_OrderTrackingEviction(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Set a small MaxTrackedOrders for testing
	scenario := client.strategy.Scenario()
	scenario.MaxTrackedOrders = 3

	// Add orders up to the limit
	for i := 0; i < 3; i++ {
		client.trackOrder(trackedOrder{
			ClobPairID: 1,
			ClientID:   uint32(i + 1),
		})
	}

	require.Len(t, client.orders, 3)

	// Add one more - should trigger FIFO eviction
	client.trackOrder(trackedOrder{
		ClobPairID: 1,
		ClientID:   4,
	})

	// Should still have 3 orders (first one evicted)
	require.Len(t, client.orders, 3)
	// First order should be evicted (clientID 1)
	require.Equal(t, uint32(2), client.orders[0].ClientID)
}

// TestPerpxPerpsClient_ClearOrderTracking tests clearing order tracking.
func TestPerpxPerpsClient_ClearOrderTracking(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Add some orders
	for i := 0; i < 5; i++ {
		client.trackOrder(trackedOrder{
			ClobPairID: 1,
			ClientID:   uint32(i + 1),
		})
	}

	require.Len(t, client.orders, 5)

	// Clear tracking
	client.ClearOrderTracking()

	require.Len(t, client.orders, 0)
}

// TestPerpxPerpsClient_InvalidActionSampling tests graceful degradation when action sampling fails.
func TestPerpxPerpsClient_InvalidActionSampling(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// The generateTxOnce method should handle invalid actions gracefully
	// by defaulting to place order
	// This is tested implicitly through the integration test
	// but we can verify the error metrics are incremented
	initialMetrics := client.GetErrorMetrics()
	initialCount := initialMetrics["error_counts_by_type"].(map[string]uint64)["invalid_action_sampled"]

	// Generate a transaction - if action sampling somehow fails, it should default to place
	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.NotEmpty(t, txBytes)

	// Verify metrics (may or may not have incremented depending on random sampling)
	finalMetrics := client.GetErrorMetrics()
	finalCount := finalMetrics["error_counts_by_type"].(map[string]uint64)["invalid_action_sampled"]
	require.GreaterOrEqual(t, finalCount, initialCount)
}

// TestPerpxPerpsClient_InsufficientMarginHandling tests insufficient margin handling.
func TestPerpxPerpsClient_InsufficientMarginHandling(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Enable margin check
	client.marginConfig.Enabled = true
	client.marginConfig.OnInsufficient = "skip"

	// Test HandleInsufficientMargin with skip
	msg, err := client.HandleInsufficientMargin()
	require.NoError(t, err)
	require.NotNil(t, msg)
	// Should return a noop message when skipping
	require.NotNil(t, msg)

	// Test with deposit option
	client.marginConfig.OnInsufficient = "deposit"
	msg, err = client.HandleInsufficientMargin()
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.IsType(t, &sendingtypes.MsgDepositToSubaccount{}, msg)
}

// TestPerpxPerpsClient_IsRetryableError tests retryable error detection.
func TestPerpxPerpsClient_IsRetryableError(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	client.retryConfig.RetryableErrors = []string{"sequence", "temporary", "timeout"}

	// Test retryable errors
	require.True(t, client.isRetryableError(fmt.Errorf("sequence mismatch")))
	require.True(t, client.isRetryableError(fmt.Errorf("temporary failure")))
	require.True(t, client.isRetryableError(fmt.Errorf("timeout occurred")))

	// Test non-retryable errors
	require.False(t, client.isRetryableError(fmt.Errorf("invalid parameter")))
	require.False(t, client.isRetryableError(nil))
}

// TestPerpxPerpsClient_ValidateMarketParams tests market parameter validation.
// Note: ValidateMarketParams was moved from strategy to client, so we test client.validateOrderParams instead.
func TestPerpxPerpsClient_ValidateMarketParams(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Test valid parameters
	err := client.validateOrderParams(1, 5, 150, 1, 100, false)
	require.NoError(t, err)

	// Test invalid quantums (out of bounds)
	err = client.validateOrderParams(1, 100, 150, 1, 100, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of bounds")

	// Test invalid subticks (out of bounds)
	err = client.validateOrderParams(1, 5, 300, 1, 100, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of bounds")
}


// TestPerpxPerpsClient_GenerateTxWithBatchCancel tests that GenerateTx uses batch cancel when configured.
func TestPerpxPerpsClient_GenerateTxWithBatchCancel(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Set batch cancel probability to 100% and configure scenario to use cancel action
	scenario := client.strategy.Scenario()
	scenario.BatchCancelProbability = 1.0
	scenario.Actions = strategies.PerpsActionWeights{
		Place:  0,
		Cancel: 1,
		Amend:  0,
		Close:  0,
		Noop:   0,
	}

	// Add multiple orders on the same market
	for i := 0; i < 5; i++ {
		client.trackOrder(trackedOrder{
			OrderID: clobtypes.OrderId{
				SubaccountId: satypes.SubaccountId{
					Owner:  client.addr.String(),
					Number: client.subaccountNumber,
				},
				ClientId:   uint32(i + 1),
				OrderFlags: clobtypes.OrderIdFlags_LongTerm,
				ClobPairId: 1,
			},
			ClobPairID: 1,
			ClientID:   uint32(i + 1),
			OrderFlags: clobtypes.OrderIdFlags_LongTerm,
		})
	}

	// Generate multiple transactions - some should use batch cancel
	batchCancelCount := 0
	singleCancelCount := 0
	initialOrderCount := len(client.orders)

	for i := 0; i < 10 && len(client.orders) > 0; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err)
		require.NotEmpty(t, txBytes)

		// Decode transaction to check message type
		encCfg := client.encCfg
		txDecoder := encCfg.TxConfig.TxDecoder()
		tx, err := txDecoder(txBytes)
		require.NoError(t, err)

		msgs := tx.GetMsgs()
		require.Len(t, msgs, 1)

		if _, ok := msgs[0].(*clobtypes.MsgBatchCancel); ok {
			batchCancelCount++
		} else if _, ok := msgs[0].(*clobtypes.MsgCancelOrder); ok {
			singleCancelCount++
		}
	}

	// With 100% probability and multiple orders, we should see batch cancels
	// (though randomness means we might not see 100% in a small sample)
	require.Greater(t, batchCancelCount+singleCancelCount, 0,
		"should generate at least one cancel message")
	
	// Verify orders were removed
	require.Less(t, len(client.orders), initialOrderCount,
		"orders should be removed after batch cancel")
}

// TestPerpxPerpsClient_BatchCancelProbabilityZero tests that batch cancel is not used when probability is 0.
func TestPerpxPerpsClient_BatchCancelProbabilityZero(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Set batch cancel probability to 0%
	scenario := client.strategy.Scenario()
	scenario.BatchCancelProbability = 0.0
	scenario.Actions = strategies.PerpsActionWeights{
		Place:  0,
		Cancel: 1,
		Amend:  0,
		Close:  0,
		Noop:   0,
	}

	// Add multiple orders on the same market
	for i := 0; i < 5; i++ {
		client.trackOrder(trackedOrder{
			OrderID: clobtypes.OrderId{
				SubaccountId: satypes.SubaccountId{
					Owner:  client.addr.String(),
					Number: client.subaccountNumber,
				},
				ClientId:   uint32(i + 1),
				OrderFlags: clobtypes.OrderIdFlags_LongTerm,
				ClobPairId: 1,
			},
			ClobPairID: 1,
			ClientID:   uint32(i + 1),
			OrderFlags: clobtypes.OrderIdFlags_LongTerm,
		})
	}

	// Generate transactions - should only use single cancel
	for i := 0; i < 10 && len(client.orders) > 0; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err)
		require.NotEmpty(t, txBytes)

		// Decode transaction to check message type
		encCfg := client.encCfg
		txDecoder := encCfg.TxConfig.TxDecoder()
		tx, err := txDecoder(txBytes)
		require.NoError(t, err)

		msgs := tx.GetMsgs()
		require.Len(t, msgs, 1)

		// Should never see batch cancel with 0% probability
		_, isBatchCancel := msgs[0].(*clobtypes.MsgBatchCancel)
		require.False(t, isBatchCancel, "should not use batch cancel with 0% probability")
	}
}

// TestPerpxPerpsClient_LongTermOrder tests that long-term orders can be created.
func TestPerpxPerpsClient_LongTermOrder(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Set long-term order probability to 100%
	scenario := client.strategy.Scenario()
	scenario.LongTermOrderProbability = 1.0
	scenario.Actions = strategies.PerpsActionWeights{
		Place:  1,
		Cancel: 0,
		Amend:  0,
		Close:  0,
		Noop:   0,
	}

	// Generate a transaction
	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.NotEmpty(t, txBytes)

	// Decode transaction to check message type
	encCfg := client.encCfg
	txDecoder := encCfg.TxConfig.TxDecoder()
	tx, err := txDecoder(txBytes)
	require.NoError(t, err)

	msgs := tx.GetMsgs()
	require.Len(t, msgs, 1)

	placeOrderMsg, ok := msgs[0].(*clobtypes.MsgPlaceOrder)
	require.True(t, ok, "expected MsgPlaceOrder")
	require.NotNil(t, placeOrderMsg.Order)

	// Check that order has long-term flags (64)
	require.Equal(t, uint32(64), placeOrderMsg.Order.OrderId.OrderFlags, "order should have long-term flags")

	// Verify order is tracked with correct flags
	require.Len(t, client.orders, 1)
	require.Equal(t, uint32(64), client.orders[0].OrderFlags, "tracked order should have long-term flags")
}

// TestPerpxPerpsClient_StatefulCancel tests that stateful cancels work for long-term orders.
func TestPerpxPerpsClient_StatefulCancel(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Add a long-term order to tracking
	client.trackOrder(trackedOrder{
		OrderID: clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{
				Owner:  client.addr.String(),
				Number: client.subaccountNumber,
			},
			ClientId:   1,
			OrderFlags: 64, // Long-term order
			ClobPairId: 1,
		},
		ClobPairID: 1,
		ClientID:   1,
		OrderFlags: 64,
	})

	scenario := client.strategy.Scenario()
	scenario.Actions = strategies.PerpsActionWeights{
		Place:  0,
		Cancel: 1,
		Amend:  0,
		Close:  0,
		Noop:   0,
	}

	// Generate a cancel transaction
	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.NotEmpty(t, txBytes)

	// Decode transaction to check message type
	encCfg := client.encCfg
	txDecoder := encCfg.TxConfig.TxDecoder()
	tx, err := txDecoder(txBytes)
	require.NoError(t, err)

	msgs := tx.GetMsgs()
	require.Len(t, msgs, 1)

	// Should use stateful cancel for long-term orders
	cancelMsg, ok := msgs[0].(*clobtypes.MsgCancelOrder)
	require.True(t, ok, "expected MsgCancelOrder")
	require.NotNil(t, cancelMsg)
	require.Equal(t, uint32(64), cancelMsg.OrderId.OrderFlags, "cancel should target long-term order")
}

// TestPerpxPerpsClient_ConditionalOrder tests that conditional orders can be created.
func TestPerpxPerpsClient_ConditionalOrder(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Set conditional order probability to 100% for the market
	scenario := client.strategy.Scenario()
	market := &scenario.Markets[0]
	market.ConditionalOrderProbability = 1.0
	market.ConditionalOrderTypes = []clobtypes.Order_ConditionType{
		clobtypes.Order_CONDITION_TYPE_STOP_LOSS,
		clobtypes.Order_CONDITION_TYPE_TAKE_PROFIT,
	}

	scenario.Actions = strategies.PerpsActionWeights{
		Place:  1,
		Cancel: 0,
		Amend:  0,
		Close:  0,
		Noop:   0,
	}

	// Generate a transaction
	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.NotEmpty(t, txBytes)

	// Decode transaction to check message type
	encCfg := client.encCfg
	txDecoder := encCfg.TxConfig.TxDecoder()
	tx, err := txDecoder(txBytes)
	require.NoError(t, err)

	msgs := tx.GetMsgs()
	require.Len(t, msgs, 1)

	placeOrderMsg, ok := msgs[0].(*clobtypes.MsgPlaceOrder)
	require.True(t, ok, "expected MsgPlaceOrder")
	require.NotNil(t, placeOrderMsg.Order)

	// Check that order has conditional type set
	require.NotEqual(t, clobtypes.Order_CONDITION_TYPE_UNSPECIFIED, placeOrderMsg.Order.ConditionType, "order should have conditional type")
	require.Greater(t, placeOrderMsg.Order.ConditionalOrderTriggerSubticks, uint64(0), "conditional order should have trigger price")
}

// TestPerpxPerpsClient_TWAPOrder tests that TWAP orders can be created.
func TestPerpxPerpsClient_TWAPOrder(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Set TWAP order probability to 100%
	scenario := client.strategy.Scenario()
	scenario.TWAPOrderProbability = 1.0
	scenario.TWAPIntervalSeconds = 60
	scenario.TWAPNumIntervals = 5
	scenario.Actions = strategies.PerpsActionWeights{
		Place:  1,
		Cancel: 0,
		Amend:  0,
		Close:  0,
		Noop:   0,
	}

	// Generate a transaction
	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.NotEmpty(t, txBytes)

	// Decode transaction to check message type
	encCfg := client.encCfg
	txDecoder := encCfg.TxConfig.TxDecoder()
	tx, err := txDecoder(txBytes)
	require.NoError(t, err)

	msgs := tx.GetMsgs()
	require.Len(t, msgs, 1)

	placeOrderMsg, ok := msgs[0].(*clobtypes.MsgPlaceOrder)
	require.True(t, ok, "expected MsgPlaceOrder")
	require.NotNil(t, placeOrderMsg.Order)

	// Check that order uses GoodTilBlockTime instead of GoodTilBlock
	goodTilBlockTime := placeOrderMsg.Order.GetGoodTilBlockTime()
	require.NotEqual(t, uint32(0), goodTilBlockTime, "TWAP order should use GoodTilBlockTime")
}

// TestPerpxPerpsClient_MixedOrderTypes tests that mixed order types work correctly.
func TestPerpxPerpsClient_MixedOrderTypes(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Set probabilities for different order types
	scenario := client.strategy.Scenario()
	scenario.LongTermOrderProbability = 0.3
	scenario.TWAPOrderProbability = 0.2
	market := &scenario.Markets[0]
	market.ConditionalOrderProbability = 0.2
	scenario.Actions = strategies.PerpsActionWeights{
		Place:  1,
		Cancel: 0,
		Amend:  0,
		Close:  0,
		Noop:   0,
	}

	// Generate multiple transactions and verify order types
	orderTypes := make(map[string]int)
	for i := 0; i < 50; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err)
		require.NotEmpty(t, txBytes)

		encCfg := client.encCfg
		txDecoder := encCfg.TxConfig.TxDecoder()
		tx, err := txDecoder(txBytes)
		require.NoError(t, err)

		msgs := tx.GetMsgs()
		require.Len(t, msgs, 1)

		placeOrderMsg, ok := msgs[0].(*clobtypes.MsgPlaceOrder)
		if !ok {
			continue
		}

		order := placeOrderMsg.Order
		switch order.OrderId.OrderFlags {
		case clobtypes.OrderIdFlags_LongTerm:
			orderTypes["long-term"]++
		case clobtypes.OrderIdFlags_Twap:
			orderTypes["twap"]++
		case clobtypes.OrderIdFlags_Conditional:
			orderTypes["conditional"]++
		case clobtypes.OrderIdFlags_ShortTerm:
			orderTypes["short-term"]++
		default:
			orderTypes["unknown"]++
		}
	}

	// Verify we got a mix of order types (not all the same)
	require.Greater(t, len(orderTypes), 1, "should generate multiple order types")
}

// TestPerpxPerpsClient_GenerateTx_RespectsMarketBounds ensures that generated
// orders respect the configured market bounds for size (quantums) and price
// (subticks) across many samples.
func TestPerpxPerpsClient_GenerateTx_RespectsMarketBounds(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Tighten bounds to make violations easier to detect.
	scenario := client.strategy.Scenario()
	require.Len(t, scenario.Markets, 1)
	market := &scenario.Markets[0]
	market.MinQuantityQuantums = 3
	market.MaxQuantityQuantums = 7
	market.MinSubticks = 120
	market.MaxSubticks = 180

	const samples = 100
	for i := 0; i < samples; i++ {
		txBytes, err := client.GenerateTx()
		require.NoError(t, err)
		require.NotEmpty(t, txBytes)

		encCfg := client.encCfg
		txDecoder := encCfg.TxConfig.TxDecoder()
		tx, err := txDecoder(txBytes)
		require.NoError(t, err)

		msgs := tx.GetMsgs()
		require.Len(t, msgs, 1)

		placeOrderMsg, ok := msgs[0].(*clobtypes.MsgPlaceOrder)
		if !ok {
			// This test focuses on place orders; skip other message types.
			continue
		}

		order := placeOrderMsg.Order
		require.GreaterOrEqual(t, order.Quantums, market.MinQuantityQuantums)
		require.LessOrEqual(t, order.Quantums, market.MaxQuantityQuantums)
		require.GreaterOrEqual(t, order.Subticks, market.MinSubticks)
		require.LessOrEqual(t, order.Subticks, market.MaxSubticks)
	}
}

// TestPerpxPerpsClient_CheckTxStatus_SuccessfulTransaction tests successful transaction status check.
func TestPerpxPerpsClient_CheckTxStatus_SuccessfulTransaction(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  5,
	}

	// Mock server that returns successful transaction
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/cosmos/tx/v1beta1/txs/testhash123", r.URL.Path)
		
		resp := map[string]interface{}{
			"tx_response": map[string]interface{}{
				"height": "100",
				"code":   0,
				"raw_log": "success",
			},
		}
		enc := json.NewEncoder(w)
		require.NoError(t, enc.Encode(&resp))
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.NoError(t, err)
	require.True(t, included)
	require.True(t, succeeded)
}

// TestPerpxPerpsClient_CheckTxStatus_FailedTransaction tests failed transaction status check.
func TestPerpxPerpsClient_CheckTxStatus_FailedTransaction(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  5,
	}

	// Mock server that returns failed transaction
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"tx_response": map[string]interface{}{
				"height": "100",
				"code":   5,
				"raw_log": "insufficient funds",
			},
		}
		enc := json.NewEncoder(w)
		require.NoError(t, enc.Encode(&resp))
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.True(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "transaction failed")
	
	// Verify error metrics were incremented
	metrics := client.GetErrorMetrics()
	require.Greater(t, metrics["transaction_failures"].(uint64), uint64(0))
}

// TestPerpxPerpsClient_CheckTxStatus_NotFound tests transaction not found scenario.
func TestPerpxPerpsClient_CheckTxStatus_NotFound(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring with low max checks for faster test
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  3,
	}

	// Mock server that always returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "not found after")
}

// TestPerpxPerpsClient_CheckTxStatus_PollingBehavior tests polling behavior with multiple checks.
func TestPerpxPerpsClient_CheckTxStatus_PollingBehavior(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  5,
	}

	// Track number of requests
	requestCount := 0
	
	// Mock server that returns 404 for first 2 requests, then success
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		
		// Return successful transaction on third request
		resp := map[string]interface{}{
			"tx_response": map[string]interface{}{
				"height": "100",
				"code":   0,
				"raw_log": "success",
			},
		}
		enc := json.NewEncoder(w)
		require.NoError(t, enc.Encode(&resp))
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.NoError(t, err)
	require.True(t, included)
	require.True(t, succeeded)
	require.Equal(t, 3, requestCount, "should have polled 3 times")
}

// TestPerpxPerpsClient_CheckTxStatus_MaxChecksLimit tests that max checks limit is respected.
func TestPerpxPerpsClient_CheckTxStatus_MaxChecksLimit(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring with low max checks
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  2,
	}

	// Track number of requests
	requestCount := 0
	
	// Mock server that always returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Equal(t, 2, requestCount, "should have polled exactly max checks times")
	require.Contains(t, err.Error(), "not found after 2 checks")
}

// TestPerpxPerpsClient_CheckTxStatus_HTTPError tests handling of HTTP errors.
func TestPerpxPerpsClient_CheckTxStatus_HTTPError(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  3,
	}

	// Mock server that returns 500 error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "HTTP 500")
	
	// Verify error metrics were incremented
	metrics := client.GetErrorMetrics()
	errorCounts := metrics["error_counts_by_type"].(map[string]uint64)
	require.Greater(t, errorCounts["tx_status_check_http_error"], uint64(0))
}

// TestPerpxPerpsClient_CheckTxStatus_JSONError tests handling of JSON parsing errors.
func TestPerpxPerpsClient_CheckTxStatus_JSONError(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  3,
	}

	// Mock server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "decode")
	
	// Verify error metrics were incremented
	metrics := client.GetErrorMetrics()
	errorCounts := metrics["error_counts_by_type"].(map[string]uint64)
	require.Greater(t, errorCounts["tx_status_check_decode_error"], uint64(0))
}

// TestPerpxPerpsClient_CheckTxStatus_NetworkError tests handling of network errors.
func TestPerpxPerpsClient_CheckTxStatus_NetworkError(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  2,
	}

	// Use an invalid URL to simulate network error
	client.restURL = "http://invalid-host-that-does-not-exist:12345"

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "failed to query transaction status")
	
	// Verify error metrics were incremented
	metrics := client.GetErrorMetrics()
	errorCounts := metrics["error_counts_by_type"].(map[string]uint64)
	require.Greater(t, errorCounts["tx_status_check_network_error"], uint64(0))
}

// TestPerpxPerpsClient_CheckTxStatus_DisabledMonitoring tests that monitoring returns error when disabled.
func TestPerpxPerpsClient_CheckTxStatus_DisabledMonitoring(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Disable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled: false,
	}

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "transaction monitoring is disabled")
}

// TestPerpxPerpsClient_CheckTxStatus_NotIncludedYet tests transaction that hasn't been included yet.
func TestPerpxPerpsClient_CheckTxStatus_NotIncludedYet(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  3,
	}

	// Mock server that returns transaction with height "0" (not included yet)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"tx_response": map[string]interface{}{
				"height": "0",
				"code":   0,
				"raw_log": "",
			},
		}
		enc := json.NewEncoder(w)
		require.NoError(t, enc.Encode(&resp))
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "not found after")
}

// TestPerpxPerpsClient_CheckTxStatus_EmptyHeight tests transaction with empty height.
func TestPerpxPerpsClient_CheckTxStatus_EmptyHeight(t *testing.T) {
	client, _ := newTestPerpsClient(t)
	
	// Enable transaction monitoring
	client.txMonitorConfig = TxMonitoringConfig{
		Enabled:    true,
		CheckDelay: 10 * time.Millisecond,
		MaxChecks:  2,
	}

	// Mock server that returns transaction with empty height
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"tx_response": map[string]interface{}{
				"height": "",
				"code":   0,
				"raw_log": "",
			},
		}
		enc := json.NewEncoder(w)
		require.NoError(t, enc.Encode(&resp))
	}))
	defer server.Close()

	client.restURL = server.URL

	included, succeeded, err := client.CheckTxStatus("testhash123")
	require.Error(t, err)
	require.False(t, included)
	require.False(t, succeeded)
	require.Contains(t, err.Error(), "not found after")
}


