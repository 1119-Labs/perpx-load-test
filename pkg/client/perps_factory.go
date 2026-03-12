package client

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// PerpxPerpsClientFactory implements loadtest.ClientFactory for PerpX perps
// order scenarios.
//
// In the legacy single-worker-per-connection mode, the factory created a
// PerpxPerpsClient per connection using an atomic worker counter.
//
// With worker sharding enabled (WorkersTotal/WorkersPerConnection set by
// ConfigFromViper from seed.workers), the factory instead constructs a
// MultiWorkerPerpsClient per connection that rotates across a deterministic
// slice of underlying workers for that connection.
//
// Example usage:
//
//	factory := NewPerpxPerpsClientFactory()
//	cfg := loadtest.Config{
//		Connections: 10,
//		Time:        60,
//		Endpoints:   []string{"ws://localhost:36657/websocket"},
//	}
//	if err := factory.ValidateConfig(cfg); err != nil {
//		return err
//	}
//	client, err := factory.NewClient(cfg)
type PerpxPerpsClientFactory struct {
	workerCounter int64
}

var _ loadtest.ClientFactory = (*PerpxPerpsClientFactory)(nil)

// NewPerpxPerpsClientFactory creates a new factory instance.
//
// The factory uses an atomic counter to assign unique worker IDs to each client,
// ensuring deterministic account generation across test runs.
func NewPerpxPerpsClientFactory() *PerpxPerpsClientFactory {
	return &PerpxPerpsClientFactory{}
}

// ValidateConfig validates the configuration for PerpX perps client.
//
// It checks that:
//   - Connections is greater than 0
//   - Either time or count is greater than 0
//   - At least one endpoint is specified
func (f *PerpxPerpsClientFactory) ValidateConfig(cfg loadtest.Config) error {
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

// NewClient creates a new PerpX perps client.
//
// Configuration comes from loadtest.Config (Viper: CLI, YAML, env, defaults).
// Perps scenario is built from cfg.Perps via DefaultPerpsScenarioConfigFromInputs.
//
// Each client is assigned a unique worker ID using an atomic counter, ensuring
// deterministic account generation that matches the seed command.
func (f *PerpxPerpsClientFactory) NewClient(cfg loadtest.Config) (loadtest.Client, error) {
	chainID := strings.TrimSpace(cfg.ChainID)
	if chainID == "" {
		chainID = "localperpxprotocol"
	}
	denom := strings.TrimSpace(cfg.Denom)
	if denom == "" {
		denom = "aperpx"
	}

	// Build scenario from config only (Viper: CLI, YAML, env, defaults).
	scenarioCfg, err := strategies.DefaultPerpsScenarioConfigFromInputs(
		cfg.Perps.ScenarioName,
		cfg.Perps.Markets,
		cfg.Perps.ActionWeights,
		cfg.Perps.MaxTrackedOrders,
		cfg.Perps.MinLeverage,
		cfg.Perps.MaxLeverage,
		cfg.Perps.UseMidPrice,
		cfg.Perps.PriceOffsetBps,
		cfg.Perps.MarketDataURL,
		cfg.Perps.MarketDataCacheTTLSeconds,
		cfg.Perps.BatchCancelPct,
		cfg.Perps.LongTermPct,
		cfg.Perps.ConditionalPct,
		cfg.Perps.TWAPPct,
		cfg.Perps.TWAPIntervalSeconds,
		cfg.Perps.TWAPNumIntervals,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build perps scenario config: %w", err)
	}

	// Build PerpsOrderStrategyConfig from scenario config
	strategyCfg := strategies.PerpsOrderStrategyConfig{
		ChainID:             chainID,
		Denom:               denom,
		Markets:             scenarioCfg.Markets,
		ScenarioName:        scenarioCfg.Name,
		MinLeverageOverride: scenarioCfg.MinLeverage,
		MaxLeverageOverride: scenarioCfg.MaxLeverage,
		ActionWeightsOverride: strategies.PerpsActionWeights{
			Place:  scenarioCfg.Actions.Place,
			Cancel: scenarioCfg.Actions.Cancel,
			Amend:  scenarioCfg.Actions.Amend,
			Close:  scenarioCfg.Actions.Close,
			Noop:   scenarioCfg.Actions.Noop,
		},
	}

	strategy, err := strategies.NewPerpsOrderStrategy(strategyCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create perps order strategy: %w", err)
	}
	if cfg.Perps.SubticksPerTick > 0 {
		strategy.Scenario().SubticksPerTick = uint64(cfg.Perps.SubticksPerTick)
	}
	if cfg.Perps.StepBaseQuantums > 0 {
		strategy.Scenario().StepBaseQuantums = uint64(cfg.Perps.StepBaseQuantums)
	}

	// If worker sharding is configured (WorkersPerConnection > 0 and
	// WorkersTotal > 0), build a MultiWorkerPerpsClient whose worker group is
	// derived deterministically from EndpointOrdinal and TransactorIndex.
	if cfg.WorkersTotal > 0 && cfg.WorkersPerConnection > 0 && len(cfg.Endpoints) > 0 && cfg.Connections > 0 {
		E := len(cfg.Endpoints)
		C := cfg.Connections
		workersPerConn := cfg.WorkersPerConnection
		workersPerEndpoint := cfg.WorkersTotal / E
		if workersPerEndpoint <= 0 {
			return nil, fmt.Errorf("invalid WorkersPerEndpoint derived from config")
		}
		if cfg.EndpointOrdinal < 0 || cfg.EndpointOrdinal >= E {
			return nil, fmt.Errorf("invalid EndpointOrdinal %d (expected 0..%d)", cfg.EndpointOrdinal, E-1)
		}
		if cfg.TransactorIndex < 0 || cfg.TransactorIndex >= C {
			return nil, fmt.Errorf("invalid TransactorIndex %d (expected 0..%d)", cfg.TransactorIndex, C-1)
		}
		endpointBase := cfg.EndpointOrdinal * workersPerEndpoint
		connBase := endpointBase + cfg.TransactorIndex*workersPerConn
		client, err := NewMultiWorkerPerpsClient(cfg, strategy, connBase, workersPerConn)
		if err != nil {
			return nil, fmt.Errorf("failed to create multi-worker perps client: %w", err)
		}
		return client, nil
	}

	// Fallback: legacy single-worker-per-connection factory using atomic
	// workerCounter. This remains for compatibility when worker sharding is
	// not configured.
	workerID := atomic.AddInt64(&f.workerCounter, 1) - 1
	client, err := NewPerpxPerpsClient(cfg, strategy, int(workerID))
	if err != nil {
		return nil, fmt.Errorf("failed to create PerpX perps client: %w", err)
	}
	return client, nil
}
