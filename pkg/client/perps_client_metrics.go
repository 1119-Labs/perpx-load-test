package client

import "sync"

// ErrorMetrics tracks error statistics.
type ErrorMetrics struct {
	mu                   sync.RWMutex
	ErrorCountsByType    map[string]uint64
	RetryCounts          uint64
	TransactionFailures  uint64
	InvalidOrderParams   uint64
	SequenceMismatches   uint64
	AccountQueryFailures uint64
	MarketDataFailures   uint64
}

// NewErrorMetrics creates a new ErrorMetrics instance.
func NewErrorMetrics() *ErrorMetrics {
	return &ErrorMetrics{
		ErrorCountsByType: make(map[string]uint64),
	}
}

// IncrementErrorCount increments the count for a specific error type.
func (m *ErrorMetrics) IncrementErrorCount(errorType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ErrorCountsByType[errorType]++
}

// incrementErrorCountNoLock increments the count for a specific error type
// without taking the mutex. This is only safe to call from contexts where the
// caller already holds m.mu.
func (m *ErrorMetrics) incrementErrorCountNoLock(errorType string) {
	m.ErrorCountsByType[errorType]++
}

// IncrementRetryCount increments the retry counter.
func (m *ErrorMetrics) IncrementRetryCount() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RetryCounts++
}

// IncrementTransactionFailures increments the transaction failure counter.
func (m *ErrorMetrics) IncrementTransactionFailures() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TransactionFailures++
}

// IncrementInvalidOrderParams increments the invalid order params counter.
func (m *ErrorMetrics) IncrementInvalidOrderParams() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InvalidOrderParams++
}

// IncrementSequenceMismatches increments the sequence mismatch counter.
func (m *ErrorMetrics) IncrementSequenceMismatches() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SequenceMismatches++
}

// IncrementAccountQueryFailures increments the account query failure counter.
func (m *ErrorMetrics) IncrementAccountQueryFailures() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AccountQueryFailures++
}

// IncrementMarketDataFailures increments the market data failure counter.
func (m *ErrorMetrics) IncrementMarketDataFailures() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MarketDataFailures++
}

// GetSnapshot returns a snapshot of current metrics.
func (m *ErrorMetrics) GetSnapshot() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := make(map[string]interface{})
	snapshot["error_counts_by_type"] = make(map[string]uint64)
	for k, v := range m.ErrorCountsByType {
		snapshot["error_counts_by_type"].(map[string]uint64)[k] = v
	}
	snapshot["retry_counts"] = m.RetryCounts
	snapshot["transaction_failures"] = m.TransactionFailures
	snapshot["invalid_order_params"] = m.InvalidOrderParams
	snapshot["sequence_mismatches"] = m.SequenceMismatches
	snapshot["account_query_failures"] = m.AccountQueryFailures
	snapshot["market_data_failures"] = m.MarketDataFailures
	return snapshot
}

// FlattenForStats flattens the snapshot into a map[string]float64 that is
// friendlier for CSV/JSON stats export. It preserves the top-level counters and
// expands per-error-type counts under "error_type.<name>" keys.
func (m *ErrorMetrics) FlattenForStats() map[string]float64 {
	snap := m.GetSnapshot()
	out := make(map[string]float64, len(snap)+len(m.ErrorCountsByType))

	if byType, ok := snap["error_counts_by_type"].(map[string]uint64); ok {
		for k, v := range byType {
			out["error_type."+k] = float64(v)
		}
	}

	if v, ok := snap["retry_counts"].(uint64); ok {
		out["retry_counts"] = float64(v)
	}
	if v, ok := snap["transaction_failures"].(uint64); ok {
		out["transaction_failures"] = float64(v)
	}
	if v, ok := snap["invalid_order_params"].(uint64); ok {
		out["invalid_order_params"] = float64(v)
	}
	if v, ok := snap["sequence_mismatches"].(uint64); ok {
		out["sequence_mismatches"] = float64(v)
	}
	if v, ok := snap["account_query_failures"].(uint64); ok {
		out["account_query_failures"] = float64(v)
	}
	if v, ok := snap["market_data_failures"].(uint64); ok {
		out["market_data_failures"] = float64(v)
	}

	return out
}


// GetErrorMetrics returns a snapshot of current error metrics for the client.
func (c *PerpxPerpsClient) GetErrorMetrics() map[string]interface{} {
	return c.errorMetrics.GetSnapshot()
}

