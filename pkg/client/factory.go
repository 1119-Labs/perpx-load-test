package client

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// PerpxBankClientFactory implements loadtest.ClientFactory for PerpX bank send transactions
type PerpxBankClientFactory struct {
	// workerCounter assigns a unique, monotonically increasing ID to each
	// client instance so that each worker derives a distinct key.
	workerCounter int64
}

// Ensure PerpxBankClientFactory implements ClientFactory
var _ loadtest.ClientFactory = (*PerpxBankClientFactory)(nil)

// NewPerpxBankClientFactory creates a new factory instance
func NewPerpxBankClientFactory() *PerpxBankClientFactory {
	return &PerpxBankClientFactory{}
}

// ValidateConfig validates the configuration for PerpX bank client
func (f *PerpxBankClientFactory) ValidateConfig(cfg loadtest.Config) error {
	if cfg.Connections <= 0 {
		return fmt.Errorf("connections must be > 0")
	}
	if cfg.Time <= 0 && cfg.Count <= 0 {
		return fmt.Errorf("either time or count must be > 0")
	}
	if len(cfg.Endpoints) == 0 {
		return fmt.Errorf("at least one endpoint must be specified")
	}
	return nil
}

// NewClient creates a new PerpX bank client
func (f *PerpxBankClientFactory) NewClient(cfg loadtest.Config) (loadtest.Client, error) {
	// Get chain configuration from environment or use defaults
	chainID := getEnv("LOADTEST_CHAIN_ID", "localperpxprotocol")
	denom := getEnv("LOADTEST_DENOM", "aperpx")
	sinkAddr := getEnv("LOADTEST_SINK_ADDRESS", "perpx1kyfmupa8z5jtxgf5f4gt285sepeg6eqnzvs25m") // Faucet address
	seedKey := getEnv("LOADTEST_SEED_KEY", "")

	// Create bank send strategy
	strategy, err := strategies.NewBankSendStrategy(chainID, denom, sinkAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create bank send strategy: %w", err)
	}

	// Assign a unique worker ID for this client so each worker uses a distinct account.
	workerID := atomic.AddInt64(&f.workerCounter, 1) - 1

	// Create client with strategy and worker ID
	client, err := NewPerpxBankClient(cfg, strategy, seedKey, int(workerID))
	if err != nil {
		return nil, fmt.Errorf("failed to create PerpX bank client: %w", err)
	}

	return client, nil
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
