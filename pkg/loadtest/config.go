package loadtest

import (
	"encoding/json"
	"fmt"
)

const (
	SelectSuppliedEndpoints   = "supplied"   // Select only the supplied endpoint(s) for load testing (the default).
	SelectDiscoveredEndpoints = "discovered" // Select newly discovered endpoints only (excluding supplied endpoints).
	SelectAnyEndpoints        = "any"        // Select from any of supplied and/or discovered endpoints.
)

var validEndpointSelectMethods = map[string]interface{}{
	SelectSuppliedEndpoints:   nil,
	SelectDiscoveredEndpoints: nil,
	SelectAnyEndpoints:        nil,
}

// DebugConfig holds optional debug flags that can be toggled globally for a load test.
type DebugConfig struct {
	// LogWorkerIDs controls whether per-tx logs include worker IDs (used for
	// debugging worker sharding / sender-target scheduling).
	LogWorkerIDs bool `json:"log_worker_ids"`
}

// Config represents the configuration for a single client (i.e. standalone or
// worker).
type Config struct {
	ClientFactory        string   `json:"client_factory"`         // Which client factory should we use for load testing?
	Connections          int      `json:"connections"`            // The number of WebSockets connections to make to each target endpoint.
	Time                 int      `json:"time"`                   // The total time, in seconds, for which to handle the load test.
	SendPeriod           int      `json:"send_period"`            // The period (in seconds) at which to send batches of transactions.
	Rate                 int      `json:"rate"`                   // The number of transactions to generate, per send period.
	Size                 int      `json:"size"`                   // The desired size of each generated transaction, in bytes.
	Count                int      `json:"count"`                  // The maximum number of transactions to send. Set to -1 for unlimited.
	BroadcastTxMethod    string   `json:"broadcast_tx_method"`    // The broadcast_tx method to use (can be "sync", "async" or "commit").
	Endpoints            []string `json:"endpoints"`              // A list of the CometBFT node endpoints to which to connect for this load test.
	RESTURL              string   `json:"rest_url"`               // Base REST URL for account/tx queries (e.g. http://localhost:1317).
	EndpointSelectMethod string   `json:"endpoint_select_method"` // The method by which to select endpoints for load testing.
	UI                   string   `json:"ui"`                     // UI mode for standalone execution: "plain" or "tui".
	ExpectPeers          int      `json:"expect_peers"`           // The minimum number of peers to expect before starting a load test. Set to 0 by default (no minimum).
	MaxEndpoints         int      `json:"max_endpoints"`          // The maximum number of endpoints to use for load testing. Set to 0 by default (no maximum).
	MinConnectivity      int      `json:"min_connectivity"`       // The minimum number of peers to which each peer must be connected before starting the load test. Set to 0 by default (no minimum).
	PeerConnectTimeout   int      `json:"peer_connect_timeout"`   // The maximum time to wait (in seconds) for all peers to connect, if ExpectPeers > 0.
	StatsOutputFile      string   `json:"stats_output_file"`      // Where to store the final aggregate statistics file (in CSV format).
	NoTrapInterrupts     bool     `json:"no_trap_interrupts"`     // Should we avoid trapping Ctrl+Break? Only relevant for standalone execution mode.

	// Optional chain context (used by perps/bank client factories). If unset,
	// factories may fall back to environment variables and/or strategy defaults.
	ChainID string `json:"chain_id"`
	Denom   string `json:"denom"`

	// Optional perps-specific configuration (YAML-driven alternative to env-only knobs).
	Perps PerpsConfig `json:"perps"`

	// Debug holds optional debugging flags (e.g. worker ID logging).
	Debug DebugConfig `json:"debug"`

	// Derived / internal-only fields (not YAML-driven; set by ConfigFromViper / TransactorGroup).
	//
	// EndpointOrdinal is the 0-based index of the endpoint for this transactor,
	// derived from the position in the YAML endpoints list. It is used purely
	// for deterministic worker sharding and is not serialized.
	EndpointOrdinal int `json:"-"`
	// TransactorIndex is the 0-based connection index within a given endpoint
	// (per-endpoint index, in [0 .. Connections-1]). It is used to derive the
	// worker range for this connection.
	TransactorIndex int `json:"-"`
	// WorkersTotal is the total number of seeded workers available for this
	// load test (typically seed.workers from the same YAML config).
	WorkersTotal int `json:"-"`
	// WorkersPerConnection is the derived number of workers assigned to each
	// websocket connection when worker sharding is enabled:
	//   WorkersPerConnection = WorkersTotal / (len(Endpoints) * Connections).
	WorkersPerConnection int `json:"-"`
}

// PerpsConfig captures perps scenario + client tuning knobs that historically
// lived only in environment variables.
type PerpsConfig struct {
	// Scenario generation knobs (mirror LOADTEST_PERPS_* env vars)
	ScenarioName              string `json:"scenario"`
	Markets                   string `json:"markets"`        // same format as LOADTEST_PERPS_MARKETS
	ActionWeights             string `json:"action_weights"` // "place,cancel,amend,close,noop"
	MaxTrackedOrders          int    `json:"max_tracked_orders"`
	MinLeverage               string `json:"min_leverage"` // keep as string to avoid float YAML gotchas; parsed later
	MaxLeverage               string `json:"max_leverage"`
	UseMidPrice               *bool  `json:"use_mid_price"`
	PriceOffsetBps            int    `json:"price_offset_bps"`
	MarketDataURL             string `json:"market_data_url"`
	MarketDataCacheTTLSeconds int    `json:"market_data_cache_ttl_seconds"`
	BatchCancelPct            int    `json:"batch_cancel_pct"`
	LongTermPct               int    `json:"long_term_pct"`
	ConditionalPct            int    `json:"conditional_pct"`
	TWAPPct                   int    `json:"twap_pct"`
	TWAPIntervalSeconds       int    `json:"twap_interval_seconds"`
	TWAPNumIntervals          int    `json:"twap_num_intervals"`

	// Perps client behavior knobs (mirror LOADTEST_PERPS_* env vars)
	DeterministicClientIDs bool   `json:"deterministic_client_ids"`
	ClientIDStart          uint32 `json:"client_id_start"`
	UseTimestampNonce      bool   `json:"use_timestamp_nonce"`

	// Tx monitoring knobs (mirror LOADTEST_PERPS_* env vars)
	MonitorTxStatus     bool `json:"monitor_tx_status"`
	TxCheckDelayMs      int  `json:"tx_check_delay_ms"`
	TxMaxChecks         int  `json:"tx_max_checks"`
	TxMonitorSampleRate int  `json:"tx_monitor_sample_rate"`

	// Retry knobs (mirror LOADTEST_PERPS_* env vars)
	MaxRetries   int `json:"max_retries"`
	RetryDelayMs int `json:"retry_delay_ms"`

	// Margin-check knobs (mirror LOADTEST_PERPS_* env vars)
	EnableMarginCheck    bool   `json:"enable_margin_check"`
	MinMarginRatio       string `json:"min_margin_ratio"`
	OnInsufficientMargin string `json:"on_insufficient_margin"` // skip|deposit|close

	// Chain/sampling knobs (mirror LOADTEST_PERPS_SUBTICKS_PER_TICK / STEP_BASE_QUANTUMS). 0 = use default.
	SubticksPerTick  int `json:"subticks_per_tick"`
	StepBaseQuantums int `json:"step_base_quantums"`

	// RNGSeed is the base seed for per-worker RNG (0 = time-based). Set via Viper for reproducible runs.
	RNGSeed int64 `json:"rng_seed"`

	// Fee/gas knobs for perps tx building (optional). If unset/zero, perps client uses defaults.
	// Values are in "base units per unit of gas" (e.g. 25000000000 for 25e9 aperpx per gas).
	FeeGasLimit       uint64 `json:"fee_gas_limit"`
	FeeMinGasPrice    string `json:"fee_min_gas_price"`
	FeeAltDenom       string `json:"fee_alt_denom"`
	FeeAltMinGasPrice string `json:"fee_alt_min_gas_price"`
}

// CoordinatorConfig is the configuration options specific to a coordinator node.
type CoordinatorConfig struct {
	BindAddr             string `json:"bind_addr"`       // The "host:port" to which to bind the coordinator node to listen for incoming workers.
	ExpectWorkers        int    `json:"expect_workers"`  // The number of workers to expect before starting the load test.
	WorkerConnectTimeout int    `json:"connect_timeout"` // The number of seconds to wait for all workers to connect.
	ShutdownWait         int    `json:"shutdown_wait"`   // The number of seconds to wait at shutdown (while keeping the HTTP server running - primarily to allow Prometheus to keep polling).
	LoadTestID           int    `json:"load_test_id"`    // An integer greater than 0 that will be exposed via a Prometheus gauge while the load test is underway.
}

// WorkerConfig is the configuration options specific to a worker node.
type WorkerConfig struct {
	ID                  string `json:"id"`              // A unique ID for this worker instance. Will show up in the metrics reported by the coordinator for this worker.
	CoordAddr           string `json:"coord_addr"`      // The address at which to find the coordinator node.
	CoordConnectTimeout int    `json:"connect_timeout"` // The maximum amount of time, in seconds, to allow for the coordinator to become available.
}

var validBroadcastTxMethods = map[string]interface{}{
	"async":  nil,
	"sync":   nil,
	"commit": nil,
}

var validUIModes = map[string]interface{}{
	"plain": nil,
	"tui":   nil,
}

func (c Config) Validate() error {
	if len(c.ClientFactory) == 0 {
		return fmt.Errorf("client factory name must be specified")
	}
	factory, factoryExists := clientFactories[c.ClientFactory]
	if !factoryExists {
		return fmt.Errorf("client factory \"%s\" does not exist", c.ClientFactory)
	}
	// client factory-specific configuration validation
	if err := factory.ValidateConfig(c); err != nil {
		return fmt.Errorf("invalid configuration for client factory \"%s\": %v", c.ClientFactory, err)
	}
	if c.Connections < 1 {
		return fmt.Errorf("expected connections to be >= 1, but was %d", c.Connections)
	}
	if c.Time < 1 {
		return fmt.Errorf("expected load test time to be >= 1 second, but was %d", c.Time)
	}
	if c.SendPeriod < 1 {
		return fmt.Errorf("expected transaction send period to be >= 1 second, but was %d", c.SendPeriod)
	}
	if c.Rate < 1 {
		return fmt.Errorf("expected transaction rate to be >= 1, but was %d", c.Rate)
	}
	if c.Count < 1 && c.Count != -1 {
		return fmt.Errorf("expected max transaction count to either be -1 or >= 1, but was %d", c.Count)
	}
	if _, ok := validBroadcastTxMethods[c.BroadcastTxMethod]; !ok {
		return fmt.Errorf("expected broadcast_tx method to be one of \"sync\", \"async\" or \"commit\", but was %s", c.BroadcastTxMethod)
	}
	if len(c.Endpoints) == 0 {
		return fmt.Errorf("expected at least one endpoint to conduct load test against, but found none")
	}
	if _, ok := validEndpointSelectMethods[c.EndpointSelectMethod]; !ok {
		return fmt.Errorf("invalid endpoint-select-method: %s", c.EndpointSelectMethod)
	}
	if len(c.UI) == 0 {
		// default UI mode if not set by older configs/CLI
		c.UI = "plain"
	}
	if _, ok := validUIModes[c.UI]; !ok {
		return fmt.Errorf("invalid ui mode: %s (expected \"plain\" or \"tui\")", c.UI)
	}
	if c.ExpectPeers < 0 {
		return fmt.Errorf("expect-peers must be at least 0, but got %d", c.ExpectPeers)
	}
	if c.ExpectPeers > 0 && c.PeerConnectTimeout < 1 {
		return fmt.Errorf("peer-connect-timeout must be at least 1 if expect-peers is non-zero, but got %d", c.PeerConnectTimeout)
	}
	if c.MaxEndpoints < 0 {
		return fmt.Errorf("invalid value for max-endpoints: %d", c.MaxEndpoints)
	}
	if c.MinConnectivity < 0 {
		return fmt.Errorf("invalid value for min-peer-connectivity: %d", c.MinConnectivity)
	}

	// Optional worker sharding validation. Only applies when WorkersTotal has
	// been populated (e.g. from seed.workers in YAML).
	if c.WorkersTotal > 0 {
		E := len(c.Endpoints)
		C := c.Connections
		R := c.Rate
		if E <= 0 {
			return fmt.Errorf("worker sharding validation: expected at least one endpoint, but found none")
		}
		if C <= 0 {
			return fmt.Errorf("worker sharding validation: expected connections to be >= 1, but was %d", C)
		}
		totalConns := E * C
		if totalConns > c.WorkersTotal {
			// We still require at least one worker per connection; otherwise many
			// connections would have no funded account to drive.
			return fmt.Errorf("worker sharding validation: total connections E*C=%d exceeds WorkersTotal=%d (need at least one worker per connection)", totalConns, c.WorkersTotal)
		}

		// Allow non‑divisible worker counts by using the integer part of
		// WorkersTotal/(E*C). Some workers may remain idle, but every connection
		// will have at least workersPerConn funded accounts to drive.
		workersPerConn := c.WorkersTotal / totalConns
		if workersPerConn <= 0 {
			return fmt.Errorf("worker sharding validation: derived WorkersPerConnection=%d must be > 0", workersPerConn)
		}
		if 2*R > workersPerConn {
			return fmt.Errorf("worker sharding validation: require 2*Rate <= WorkersPerConnection, but 2*Rate=%d, WorkersPerConnection=%d", 2*R, workersPerConn)
		}
	}
	return nil
}

// MaxTxsPerEndpoint estimates the maximum number of transactions that this
// configuration would generate for a single endpoint.
func (c Config) MaxTxsPerEndpoint() uint64 {
	if c.Count > -1 {
		return uint64(c.Count)
	}
	return uint64(c.Rate) * uint64(c.Time)
}

func (c CoordinatorConfig) ToJSON() string {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("%v", c)
	}
	return string(b)
}

func (c CoordinatorConfig) Validate() error {
	if len(c.BindAddr) == 0 {
		return fmt.Errorf("coordinator bind address must be specified")
	}
	if c.ExpectWorkers < 1 {
		return fmt.Errorf("coordinator expect-workers must be at least 1, but got %d", c.ExpectWorkers)
	}
	if c.WorkerConnectTimeout < 1 {
		return fmt.Errorf("coordinator connect-timeout must be at least 1 second")
	}
	if c.LoadTestID < 0 {
		return fmt.Errorf("coordinator load-test-id must be 0 or greater")
	}
	return nil
}

func (c Config) ToJSON() string {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("%v", c)
	}
	return string(b)
}

func (c WorkerConfig) Validate() error {
	if len(c.ID) > 0 && !isValidWorkerID(c.ID) {
		return fmt.Errorf("Invalid worker ID \"%s\": worker IDs can only be lowercase alphanumeric characters", c.ID)
	}
	if len(c.CoordAddr) == 0 {
		return fmt.Errorf("coordinator address must be specified")
	}
	if c.CoordConnectTimeout < 1 {
		return fmt.Errorf("expected connect-timeout to be >= 1, but was %d", c.CoordConnectTimeout)
	}
	return nil
}

func (c WorkerConfig) ToJSON() string {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("%v", c)
	}
	return string(b)
}
