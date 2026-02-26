package client

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// PerpxBankClientFactory implements loadtest.ClientFactory for PerpX bank send transactions
type PerpxBankClientFactory struct {
	workerCounter int64
	poolMu        sync.Mutex
	receiverPool  []string // lazy-inited when LOADTEST_RECEIVER_POOL=many; same N as sender accounts so different senders → different receivers (no sequential tx bottleneck)
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
	chainID := getEnv("LOADTEST_CHAIN_ID", "localperpxprotocol")
	denom := getEnv("LOADTEST_DENOM", "aperpx")
	sinkAddr := getEnv("LOADTEST_SINK_ADDRESS", "perpx1kyfmupa8z5jtxgf5f4gt285sepeg6eqnzvs25m")
	seedKey := getEnv("LOADTEST_SEED_KEY", "")

	// Lazy-init receiver pool when LOADTEST_RECEIVER_POOL=many so different senders send to different receivers (no sequential execution dependency)
	var receiverPool []string
	if getEnv("LOADTEST_RECEIVER_POOL", "") == "many" {
		f.poolMu.Lock()
		if len(f.receiverPool) == 0 {
			N := cfg.Connections * len(cfg.Endpoints)
			f.receiverPool = make([]string, N)
			for i := 0; i < N; i++ {
				f.receiverPool[i] = DeriveBenchAddress(i)
			}
		}
		receiverPool = f.receiverPool
		f.poolMu.Unlock()
	}

	strategy, err := strategies.NewBankSendStrategy(chainID, denom, sinkAddr, receiverPool)
	if err != nil {
		return nil, fmt.Errorf("failed to create bank send strategy: %w", err)
	}

	workerID := atomic.AddInt64(&f.workerCounter, 1) - 1
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
