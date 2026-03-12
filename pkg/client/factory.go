package client

import (
	"fmt"
	"strings"
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

// NewClient creates a new PerpX bank client. All config comes from cfg (Viper: YAML, env, CLI); code defaults only when still empty.
func (f *PerpxBankClientFactory) NewClient(cfg loadtest.Config) (loadtest.Client, error) {
	chainID := strings.TrimSpace(cfg.ChainID)
	if chainID == "" {
		return nil, fmt.Errorf("chain ID is required")
	}
	denom := strings.TrimSpace(cfg.Denom)
	if denom == "" {
		return nil, fmt.Errorf("denom is required")
	}
	strategy, err := strategies.NewBankSendStrategy(chainID, denom)
	if err != nil {
		return nil, fmt.Errorf("failed to create bank send strategy: %w", err)
	}

	// If worker sharding is configured, derive worker ranges deterministically
	// from EndpointOrdinal / TransactorIndex instead of using an atomic counter.
	if cfg.WorkersTotal > 0 && cfg.WorkersPerConnection > 0 && len(cfg.Endpoints) > 0 && cfg.Connections > 0 {
		E := len(cfg.Endpoints)
		C := cfg.Connections
		G := cfg.WorkersPerConnection
		if cfg.EndpointOrdinal < 0 || cfg.EndpointOrdinal >= E {
			return nil, fmt.Errorf("invalid EndpointOrdinal %d (expected 0..%d)", cfg.EndpointOrdinal, E-1)
		}
		if cfg.TransactorIndex < 0 || cfg.TransactorIndex >= C {
			return nil, fmt.Errorf("invalid TransactorIndex %d (expected 0..%d)", cfg.TransactorIndex, C-1)
		}
		globalConnIndex := cfg.EndpointOrdinal*C + cfg.TransactorIndex
		workerBase := globalConnIndex * G
		connectionIndex := globalConnIndex
		workersPerConnection := G

		client, err := NewPerpxBankClient(cfg, strategy, workerBase, connectionIndex, workersPerConnection)
		if err != nil {
			return nil, fmt.Errorf("failed to create deterministic PerpX bank client: %w", err)
		}
		return client, nil
	}

	// Fallback: legacy mapping using an atomic worker counter and treating each
	// endpoint as a "worker" slot for connectionIndex derivation.
	workerID := atomic.AddInt64(&f.workerCounter, 1) - 1
	workersPerConnection := len(cfg.Endpoints)
	if workersPerConnection <= 0 {
		workersPerConnection = 1
	}
	connectionIndex := int(workerID) / workersPerConnection

	client, err := NewPerpxBankClient(cfg, strategy, int(workerID), connectionIndex, workersPerConnection)
	if err != nil {
		return nil, fmt.Errorf("failed to create PerpX bank client: %w", err)
	}
	return client, nil
}
