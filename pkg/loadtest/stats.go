package loadtest

import (
	"encoding/csv"
	"fmt"
	"os"

	"github.com/prometheus/client_golang/prometheus"
)

type AggregateStats struct {
	TotalTxs         int     // The total number of transactions sent.
	TotalTimeSeconds float64 // The total time taken to send `TotalTxs` transactions.
	TotalBytes       int64   // The cumulative number of bytes sent as transactions.

	// Computed statistics
	AvgTxRate   float64 // The rate at which transactions were submitted (tx/sec).
	AvgDataRate float64 // The rate at which data was transmitted in transactions (bytes/sec).
	AvgTxSize   float64 // The average size of each transaction (bytes/tx).
}

func (s *AggregateStats) String() string {
	return fmt.Sprintf(
		"AggregateStats{TotalTimeSeconds: %.3f, TotalTxs: %d, TotalBytes: %d, AvgTxRate: %.6f, AvgDataRate: %.6f, AvgTxSize: %.2f}",
		s.TotalTimeSeconds,
		s.TotalTxs,
		s.TotalBytes,
		s.AvgTxRate,
		s.AvgDataRate,
		s.AvgTxSize,
	)
}

func (s *AggregateStats) Compute() {
	s.AvgTxRate = 0
	s.AvgDataRate = 0
	s.AvgTxSize = 0
	if s.TotalTimeSeconds > 0.0 {
		s.AvgTxRate = float64(s.TotalTxs) / s.TotalTimeSeconds
		s.AvgDataRate = float64(s.TotalBytes) / s.TotalTimeSeconds
	}
	if s.TotalTxs > 0 {
		s.AvgTxSize = float64(s.TotalBytes) / float64(s.TotalTxs)
	}
}

// resetDefaultPrometheusRegistry is a test helper that allows tests to safely
// construct multiple Coordinators that register Prometheus metrics using the
// global promauto factory. It is intentionally kept in this file to avoid
// adding a separate test-only file that would require build tags.
//
// NOTE: This helper must only be used from tests.
func resetDefaultPrometheusRegistry() {
	// This mirrors the behavior of prometheus.MustRegister on the default
	// registry, but swaps the registry instance entirely. It is safe in tests
	// where no long-lived metrics exporters are running.
	r := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = r
	prometheus.DefaultGatherer = r
}

func writeAggregateStats(filename string, stats AggregateStats) error {
	stats.Compute()
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	records := [][]string{
		{"Parameter", "Value", "Units"},
		{"total_time", fmt.Sprintf("%.3f", stats.TotalTimeSeconds), "seconds"},
		{"total_txs", fmt.Sprintf("%d", stats.TotalTxs), "count"},
		{"total_bytes", fmt.Sprintf("%d", stats.TotalBytes), "bytes"},
		{"avg_tx_rate", fmt.Sprintf("%.6f", stats.AvgTxRate), "transactions per second"},
		{"avg_data_rate", fmt.Sprintf("%.6f", stats.AvgDataRate), "bytes per second"},
		{"avg_tx_size", fmt.Sprintf("%.2f", stats.AvgTxSize), "bytes per transaction"},
	}
	return w.WriteAll(records)
}

// writeAggregateAndPerWorkerStats extends the standard aggregate CSV with a
// per-worker breakdown. The aggregate rows remain identical to
// writeAggregateStats so existing consumers continue to work.
func writeAggregateAndPerWorkerStats(
	filename string,
	total AggregateStats,
	perWorker map[string]AggregateStats,
) error {
	total.Compute()
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	records := [][]string{
		{"Parameter", "Value", "Units"},
		{"total_time", fmt.Sprintf("%.3f", total.TotalTimeSeconds), "seconds"},
		{"total_txs", fmt.Sprintf("%d", total.TotalTxs), "count"},
		{"total_bytes", fmt.Sprintf("%d", total.TotalBytes), "bytes"},
		{"avg_tx_rate", fmt.Sprintf("%.6f", total.AvgTxRate), "transactions per second"},
		{"avg_data_rate", fmt.Sprintf("%.6f", total.AvgDataRate), "bytes per second"},
		{"avg_tx_size", fmt.Sprintf("%.2f", total.AvgTxSize), "bytes per transaction"},
		{},
		// Per-worker header
		{"worker_id", "total_txs", "total_bytes", "avg_tx_rate", "avg_tx_size"},
	}

	for id, ws := range perWorker {
		ws.Compute()
		records = append(records, []string{
			id,
			fmt.Sprintf("%d", ws.TotalTxs),
			fmt.Sprintf("%d", ws.TotalBytes),
			fmt.Sprintf("%.6f", ws.AvgTxRate),
			fmt.Sprintf("%.2f", ws.AvgTxSize),
		})
	}

	return w.WriteAll(records)
}
