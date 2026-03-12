package client

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorMetrics_InitAndSnapshot(t *testing.T) {
	m := NewErrorMetrics()
	require.NotNil(t, m)
	require.NotNil(t, m.ErrorCountsByType)
	require.Len(t, m.ErrorCountsByType, 0)

	snap := m.GetSnapshot()
	require.NotNil(t, snap)

	byType, ok := snap["error_counts_by_type"].(map[string]uint64)
	require.True(t, ok)
	require.Len(t, byType, 0)

	require.Equal(t, uint64(0), snap["retry_counts"])
	require.Equal(t, uint64(0), snap["transaction_failures"])
	require.Equal(t, uint64(0), snap["invalid_order_params"])
	require.Equal(t, uint64(0), snap["sequence_mismatches"])
	require.Equal(t, uint64(0), snap["account_query_failures"])
	require.Equal(t, uint64(0), snap["market_data_failures"])
}

func TestErrorMetrics_IncrementAndSnapshotIsCopy(t *testing.T) {
	m := NewErrorMetrics()

	m.IncrementErrorCount("foo")
	m.IncrementErrorCount("foo")
	m.IncrementErrorCount("bar")
	m.IncrementRetryCount()
	m.IncrementTransactionFailures()

	snap1 := m.GetSnapshot()
	byType1 := snap1["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(2), byType1["foo"])
	require.Equal(t, uint64(1), byType1["bar"])
	require.Equal(t, uint64(1), snap1["retry_counts"])
	require.Equal(t, uint64(1), snap1["transaction_failures"])

	// Mutate snapshot map; internal state must not change.
	byType1["foo"] = 999
	byType1["new"] = 123

	snap2 := m.GetSnapshot()
	byType2 := snap2["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(2), byType2["foo"])
	require.Equal(t, uint64(0), byType2["new"])
}

func TestErrorMetrics_ConcurrentAccess(t *testing.T) {
	m := NewErrorMetrics()

	const goroutines = 32
	const perG = 2000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			// Mix of operations to exercise locking.
			for j := 0; j < perG; j++ {
				if (i+j)%3 == 0 {
					m.IncrementErrorCount("e1")
				} else {
					m.IncrementErrorCount("e2")
				}
				if j%10 == 0 {
					m.IncrementRetryCount()
				}
				if j%25 == 0 {
					m.IncrementInvalidOrderParams()
				}
			}
			_ = m.GetSnapshot()
		}(i)
	}
	wg.Wait()

	snap := m.GetSnapshot()
	byType := snap["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(goroutines*perG), byType["e1"]+byType["e2"])
	require.Greater(t, snap["retry_counts"].(uint64), uint64(0))
	require.Greater(t, snap["invalid_order_params"].(uint64), uint64(0))
}

func TestPerpxPerpsClient_GetErrorMetricsReturnsSnapshot(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	client.errorMetrics.IncrementErrorCount("x")
	client.errorMetrics.IncrementRetryCount()

	snap := client.GetErrorMetrics()
	require.NotNil(t, snap)
	byType := snap["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(1), byType["x"])
	require.Equal(t, uint64(1), snap["retry_counts"])
}

func TestErrorMetrics_FlattenForStats(t *testing.T) {
	m := NewErrorMetrics()
	m.IncrementErrorCount("sequence_recovered")
	m.IncrementRetryCount()
	m.IncrementTransactionFailures()
	m.IncrementInvalidOrderParams()
	m.IncrementSequenceMismatches()
	m.IncrementAccountQueryFailures()
	m.IncrementMarketDataFailures()

	stats := m.FlattenForStats()
	require.Equal(t, float64(1), stats["error_type.sequence_recovered"])
	require.Equal(t, float64(1), stats["retry_counts"])
	require.Equal(t, float64(1), stats["transaction_failures"])
	require.Equal(t, float64(1), stats["invalid_order_params"])
	require.Equal(t, float64(1), stats["sequence_mismatches"])
	require.Equal(t, float64(1), stats["account_query_failures"])
	require.Equal(t, float64(1), stats["market_data_failures"])
}


