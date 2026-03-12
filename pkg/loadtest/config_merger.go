package loadtest

import (
	"strings"

	"github.com/spf13/viper"
)

// ConfigFromViper builds a full Config from a single Viper instance.
// Viper must already have: SetLoadtestViperDefaults, BindLoadtestEnvToViper,
// optional config file (when --config) via ReadInConfig, and BindPFlags(cmd.PersistentFlags()).
// Precedence: CLI flags > YAML (loadtest.*) > env > Viper defaults.
func ConfigFromViper(v *viper.Viper) Config {
	var cfg Config
	if v == nil {
		return cfg
	}

	strOr := func(flagKey, loadtestKey string) string {
		if s := strings.TrimSpace(v.GetString(flagKey)); s != "" {
			return s
		}
		return strings.TrimSpace(v.GetString(loadtestKey))
	}
	strOnly := func(key string) string { return strings.TrimSpace(v.GetString(key)) }
	intOr := func(flagKey, loadtestKey string, defaultVal int) int {
		if n := v.GetInt(flagKey); n != 0 || v.IsSet(flagKey) {
			return n
		}
		if n := v.GetInt(loadtestKey); n != 0 {
			return n
		}
		return defaultVal
	}
	intOnly := func(key string, defaultVal int) int {
		if n := v.GetInt(key); n != 0 {
			return n
		}
		return defaultVal
	}

	// Root flags / loadtest.*
	cfg.ClientFactory = strOr("client-factory", "loadtest.clientFactory")
	if cfg.ClientFactory == "" {
		cfg.ClientFactory = "perpx-bank"
	}
	cfg.Connections = intOr("connections", "loadtest.connections", 1)
	cfg.Time = intOr("time", "loadtest.time", 60)
	cfg.SendPeriod = intOr("send-period", "loadtest.sendPeriod", 1)
	cfg.Rate = intOr("rate", "loadtest.rate", 1000)
	cfg.Size = intOr("size", "loadtest.size", 250)
	if n := v.GetInt("count"); n != 0 {
		cfg.Count = n
	} else if n := v.GetInt("loadtest.count"); n != 0 {
		cfg.Count = n
	} else {
		cfg.Count = -1
	}
	cfg.BroadcastTxMethod = strOr("broadcast-tx-method", "loadtest.broadcastTxMethod")
	if cfg.BroadcastTxMethod == "" {
		cfg.BroadcastTxMethod = "async"
	}
	cfg.Endpoints = v.GetStringSlice("endpoints")
	if len(cfg.Endpoints) == 0 {
		cfg.Endpoints = v.GetStringSlice("loadtest.endpoints")
	}
	if len(cfg.Endpoints) == 0 {
		if s := strOnly("loadtest.wsEndpoints"); s != "" {
			for _, part := range strings.Split(s, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					cfg.Endpoints = append(cfg.Endpoints, part)
				}
			}
		}
		if len(cfg.Endpoints) == 0 {
			if s := strOnly("loadtest.wsUrl"); s != "" {
				cfg.Endpoints = []string{s}
			}
		}
	}
	cfg.RESTURL = strOr("rest-url", "loadtest.restUrl")
	cfg.ChainID = strOr("chain-id", "loadtest.chainId")
	if cfg.ChainID == "" {
		cfg.ChainID = "localperpxprotocol"
	}
	cfg.Denom = strOr("denom", "loadtest.denom")
	if cfg.Denom == "" {
		cfg.Denom = "aperpx"
	}
	cfg.UI = strOr("ui", "loadtest.ui")
	if cfg.UI == "" {
		cfg.UI = "plain"
	}
	cfg.EndpointSelectMethod = strOr("endpoint-select-method", "loadtest.endpointSelectMethod")
	if cfg.EndpointSelectMethod == "" {
		cfg.EndpointSelectMethod = SelectSuppliedEndpoints
	}
	cfg.ExpectPeers = intOr("expect-peers", "loadtest.expectPeers", 0)
	cfg.MaxEndpoints = intOr("max-endpoints", "loadtest.maxEndpoints", 0)
	cfg.PeerConnectTimeout = intOr("peer-connect-timeout", "loadtest.peerConnectTimeout", 600)
	cfg.MinConnectivity = intOr("min-peer-connectivity", "loadtest.minConnectivity", 0)
	cfg.StatsOutputFile = strOr("stats-output", "loadtest.statsOutputFile")

	// Debug
	cfg.Debug.LogWorkerIDs = v.GetBool("loadtest.debug.logWorkerIDs")

	// Perps
	p := &cfg.Perps
	p.ScenarioName = strOr("perps-scenario", "loadtest.perps.scenario")
	if p.ScenarioName == "" {
		p.ScenarioName = "simple_perps"
	}
	p.Markets = strOnly("loadtest.perps.markets")
	p.ActionWeights = strOnly("loadtest.perps.actionWeights")
	p.MaxTrackedOrders = intOnly("loadtest.perps.maxTrackedOrders", 0)
	p.MinLeverage = strOnly("loadtest.perps.minLeverage")
	p.MaxLeverage = strOnly("loadtest.perps.maxLeverage")
	if v.GetBool("loadtest.perps.useMidPrice") {
		t := true
		p.UseMidPrice = &t
	}
	p.PriceOffsetBps = intOnly("loadtest.perps.priceOffsetBps", 0)
	p.MarketDataURL = strOnly("loadtest.perps.marketDataUrl")
	p.MarketDataCacheTTLSeconds = intOnly("loadtest.perps.marketDataCacheTTLSeconds", 0)
	p.BatchCancelPct = intOnly("loadtest.perps.batchCancelPct", 0)
	p.LongTermPct = intOnly("loadtest.perps.longTermPct", 0)
	p.ConditionalPct = intOnly("loadtest.perps.conditionalPct", 0)
	p.TWAPPct = intOnly("loadtest.perps.twapPct", 0)
	p.TWAPIntervalSeconds = intOnly("loadtest.perps.twapIntervalSeconds", 0)
	p.TWAPNumIntervals = intOnly("loadtest.perps.twapNumIntervals", 0)
	p.DeterministicClientIDs = v.GetBool("loadtest.perps.deterministicClientIds")
	if n := v.GetUint("loadtest.perps.clientIdStart"); n > 0 {
		p.ClientIDStart = uint32(n)
	}
	p.UseTimestampNonce = v.GetBool("loadtest.perps.useTimestampNonce")
	p.MonitorTxStatus = v.GetBool("loadtest.perps.monitorTxStatus")
	p.TxCheckDelayMs = intOnly("loadtest.perps.txCheckDelayMs", 0)
	p.TxMaxChecks = intOnly("loadtest.perps.txMaxChecks", 0)
	p.TxMonitorSampleRate = intOnly("loadtest.perps.txMonitorSampleRate", 0)
	p.MaxRetries = intOnly("loadtest.perps.maxRetries", 0)
	p.RetryDelayMs = intOnly("loadtest.perps.retryDelayMs", 0)
	p.EnableMarginCheck = v.GetBool("loadtest.perps.enableMarginCheck")
	p.MinMarginRatio = strOnly("loadtest.perps.minMarginRatio")
	s := strings.ToLower(strOnly("loadtest.perps.onInsufficientMargin"))
	if s == "skip" || s == "deposit" || s == "close" {
		p.OnInsufficientMargin = s
	}
	p.SubticksPerTick = intOnly("loadtest.perps.subticksPerTick", 0)
	p.StepBaseQuantums = intOnly("loadtest.perps.stepBaseQuantums", 0)
	if n := v.GetInt64("loadtest.perps.rngSeed"); n != 0 {
		p.RNGSeed = n
	}

	// Perps fee/gas knobs (optional).
	if n := v.GetUint64("loadtest.perps.feeGasLimit"); n > 0 {
		p.FeeGasLimit = n
	}
	p.FeeMinGasPrice = strOnly("loadtest.perps.feeMinGasPrice")
	p.FeeAltDenom = strOnly("loadtest.perps.feeAltDenom")
	p.FeeAltMinGasPrice = strOnly("loadtest.perps.feeAltMinGasPrice")

	// Derived worker-sharding fields.
	// WorkersTotal comes from the seed section of the same YAML config when present.
	if wt := v.GetInt("seed.workers"); wt > 0 {
		cfg.WorkersTotal = wt
	}
	if cfg.WorkersTotal > 0 && len(cfg.Endpoints) > 0 && cfg.Connections > 0 {
		cfg.WorkersPerConnection = cfg.WorkersTotal / (len(cfg.Endpoints) * cfg.Connections)
	}

	return cfg
}
