package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

func TestPerpxPerpsClient_GenerateTx_RetryableThenSuccess_ExponentialBackoff(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	var attempts atomic.Int32
	var sleeps []time.Duration
	client.sleepFn = func(d time.Duration) { sleeps = append(sleeps, d) }
	client.retryConfig = RetryConfig{
		MaxRetries:      3,
		RetryDelay:      5 * time.Millisecond,
		RetryableErrors: []string{"temporary"},
	}

	client.generateTxOnceFn = func() ([]byte, error) {
		n := attempts.Add(1)
		if n <= 3-1 { // fail first 2 attempts, succeed on 3rd
			return nil, fmt.Errorf("temporary failure")
		}
		return []byte{0x01, 0x02}, nil
	}

	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.Equal(t, []byte{0x01, 0x02}, txBytes)
	require.Equal(t, int32(3), attempts.Load())

	// attempt 1 sleeps RetryDelay, attempt 2 sleeps RetryDelay*2
	require.Equal(t, []time.Duration{5 * time.Millisecond, 10 * time.Millisecond}, sleeps)

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(2), metrics["retry_counts"])
	byType := metrics["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(2), byType["tx_generation_retry"])
	require.Equal(t, uint64(0), metrics["transaction_failures"])
}

func TestPerpxPerpsClient_GenerateTx_NonRetryable_NoRetry(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	var attempts atomic.Int32
	var sleeps []time.Duration
	client.sleepFn = func(d time.Duration) { sleeps = append(sleeps, d) }
	client.retryConfig = RetryConfig{
		MaxRetries:      5,
		RetryDelay:      1 * time.Millisecond,
		RetryableErrors: []string{"timeout"}, // doesn't match our error
	}

	client.generateTxOnceFn = func() ([]byte, error) {
		attempts.Add(1)
		return nil, fmt.Errorf("invalid parameter")
	}

	_, err := client.GenerateTx()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid parameter")
	require.Equal(t, int32(1), attempts.Load(), "should not retry non-retryable error")
	require.Empty(t, sleeps, "should not sleep when no retry happens")

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(0), metrics["retry_counts"])
	byType := metrics["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(1), byType["non_retryable_error"])
}

func TestPerpxPerpsClient_GenerateTx_MaxRetriesExceeded(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	var attempts atomic.Int32
	var sleeps []time.Duration
	client.sleepFn = func(d time.Duration) { sleeps = append(sleeps, d) }
	client.retryConfig = RetryConfig{
		MaxRetries:      2,
		RetryDelay:      3 * time.Millisecond,
		RetryableErrors: []string{"timeout"},
	}

	client.generateTxOnceFn = func() ([]byte, error) {
		attempts.Add(1)
		return nil, fmt.Errorf("timeout occurred")
	}

	_, err := client.GenerateTx()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to generate transaction after 2 retries")
	require.Equal(t, int32(3), attempts.Load(), "attempts should be MaxRetries+1")
	require.Equal(t, []time.Duration{3 * time.Millisecond, 6 * time.Millisecond}, sleeps)

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(2), metrics["retry_counts"])
	require.Equal(t, uint64(1), metrics["transaction_failures"])
	byType := metrics["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(1), byType["tx_generation_max_retries_exceeded"])
}

func TestPerpxPerpsClient_GenerateTx_SequenceMismatch_TriggersRecovery(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	var attempts atomic.Int32
	var recoverCalls atomic.Int32
	client.sleepFn = func(time.Duration) {}
	client.retryConfig = RetryConfig{
		MaxRetries:      2,
		RetryDelay:      1 * time.Millisecond,
		RetryableErrors: []string{"sequence"},
	}

	client.generateTxOnceFn = func() ([]byte, error) {
		n := attempts.Add(1)
		if n == 1 {
			return nil, fmt.Errorf("sequence mismatch")
		}
		return []byte{0xAA}, nil
	}
	client.recoverSequenceFn = func() error {
		recoverCalls.Add(1)
		return nil
	}

	txBytes, err := client.GenerateTx()
	require.NoError(t, err)
	require.Equal(t, []byte{0xAA}, txBytes)
	require.Equal(t, int32(2), attempts.Load())
	require.Equal(t, int32(1), recoverCalls.Load(), "should attempt recovery on sequence error before retry")
}

func TestPerpxPerpsClient_EnsureAccountQueried_RetryOnHTTPError_ExponentialBackoff(t *testing.T) {
	// Set a bech32 prefix so address derivation in SDK works as expected.
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("perpx", "perpxpub")

	var reqCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		req := reqCount.Add(1)
		if req <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		// Success response on 3rd request.
		resp := mockAccountResponse{}
		resp.Account.Type = "/cosmos.auth.v1beta1.BaseAccount"
		resp.Account.Address = "perpx1testaddressxxxxxxxxxxxxxxxxxxxxxx"
		resp.Account.AccountNumber = "10"
		resp.Account.Sequence = "7"
		require.NoError(t, json.NewEncoder(w).Encode(&resp))
	}))
	t.Cleanup(server.Close)

	market := strategies.PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}
	strategy, err := strategies.NewPerpsOrderStrategy(strategies.PerpsOrderStrategyConfig{
		ChainID:      "localperpxprotocol",
		Denom:        "aperpx",
		Markets:      []strategies.PerpsMarketConfig{market},
		ScenarioName: strategies.ScenarioSimplePerps,
	})
	require.NoError(t, err)

	client, err := NewPerpxPerpsClient(loadtest.Config{Endpoints: []string{server.URL}}, strategy, 0)
	require.NoError(t, err)
	client.restURL = server.URL

	var sleeps []time.Duration
	client.sleepFn = func(d time.Duration) { sleeps = append(sleeps, d) }
	client.retryConfig = RetryConfig{
		MaxRetries:      3,
		RetryDelay:      4 * time.Millisecond,
		RetryableErrors: []string{"does-not-matter-here"},
	}

	require.NoError(t, client.ensureAccountQueried())
	require.Equal(t, int32(3), reqCount.Load())
	require.Equal(t, uint64(10), client.accountNum)
	require.Equal(t, uint64(7), client.sequence)
	require.Equal(t, []time.Duration{4 * time.Millisecond, 8 * time.Millisecond}, sleeps)

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(2), metrics["retry_counts"])
	byType := metrics["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(2), byType["account_query_retry"])
}


