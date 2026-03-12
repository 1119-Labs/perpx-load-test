package loadtest

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/1119-Labs/perpx-load-test/internal/logging"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// CLIVersion must be manually updated as new versions are released.
const CLIVersion = "v0.3.0"

// cliVersionCommitID must be set through linker settings. See
// https://stackoverflow.com/a/11355611/1156132 for details.
var cliVersionCommitID string

// CLIConfig allows developers to customize their own load testing tool.
type CLIConfig struct {
	AppName              string
	AppShortDesc         string
	AppLongDesc          string
	DefaultClientFactory string
}

var flagVerbose bool

// BuildCLI returns the loadtest root command (with coordinator, worker, version subcommands).
// Callers may add more subcommands (e.g. seed) before executing.
func BuildCLI(cli *CLIConfig, logger logging.Logger) *cobra.Command {
	cobra.OnInitialize(func() { initLogLevel(logger) })
	var configPath string
	rootCmd := &cobra.Command{
		Use:   cli.AppName,
		Short: cli.AppShortDesc,
		Long:  cli.AppLongDesc,
		Run: func(cmd *cobra.Command, args []string) {
			v := viper.GetViper()
			if configPath != "" {
				v.SetConfigFile(configPath)
				if err := v.ReadInConfig(); err != nil {
					logger.Error(fmt.Sprintf("reading config %s: %v", configPath, err))
					os.Exit(1)
				}
			}
			_ = v.BindPFlags(cmd.PersistentFlags())
			cfg := ConfigFromViper(v)
			logger.Debug(fmt.Sprintf("Configuration: %s", cfg.ToJSON()))
			if err := cfg.Validate(); err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}

			if err := ExecuteStandalone(cfg); err != nil {
				os.Exit(1)
			}
		},
	}
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Path to YAML config file (optional). Values under loadtest.* will be used as defaults unless an equivalent CLI flag is explicitly set.")
	rootCmd.PersistentFlags().String("client-factory", cli.DefaultClientFactory, "The identifier of the client factory to use for generating load testing transactions")
	rootCmd.PersistentFlags().IntP("connections", "c", 1, "The number of connections to open to each endpoint simultaneously")
	rootCmd.PersistentFlags().IntP("time", "T", 60, "The duration (in seconds) for which to handle the load test")
	rootCmd.PersistentFlags().IntP("send-period", "p", 1, "The period (in seconds) at which to send batches of transactions")
	rootCmd.PersistentFlags().IntP("rate", "r", 1000, "The number of transactions to generate each second on each connection, to each endpoint")
	rootCmd.PersistentFlags().IntP("size", "s", 250, "The size of each transaction, in bytes - must be greater than 40")
	rootCmd.PersistentFlags().IntP("count", "N", -1, "The maximum number of transactions to send - set to -1 to turn off this limit")
	rootCmd.PersistentFlags().String("broadcast-tx-method", "async", "The broadcast_tx method to use when submitting transactions - can be async, sync or commit")
	rootCmd.PersistentFlags().StringSlice("endpoints", []string{}, "A comma-separated list of URLs indicating CometBFT WebSockets RPC endpoints to which to connect")
	rootCmd.PersistentFlags().String("rest-url", "", "Base REST URL for account/tx queries (e.g. http://localhost:1317). If unset, clients may derive it from endpoints or environment.")
	rootCmd.PersistentFlags().String("chain-id", "localperpxprotocol", "Chain ID for signing and client context")
	rootCmd.PersistentFlags().String("denom", "aperpx", "Fee denomination")
	rootCmd.PersistentFlags().String("perps-scenario", "simple_perps", "Perps scenario preset (simple_perps, maker_taker, stress_mixed)")
	rootCmd.PersistentFlags().String("bank-sink-address", "perpx1kyfmupa8z5jtxgf5f4gt285sepeg6eqnzvs25m", "Bank load test sink address for transfers")
	rootCmd.PersistentFlags().String("ui", "plain", "UI mode for standalone execution: plain or tui")
	rootCmd.PersistentFlags().String("endpoint-select-method", SelectSuppliedEndpoints, "The method by which to select endpoints")
	rootCmd.PersistentFlags().Int("expect-peers", 0, "The minimum number of peers to expect when crawling the P2P network from the specified endpoint(s) prior to waiting for workers to connect")
	rootCmd.PersistentFlags().Int("max-endpoints", 0, "The maximum number of endpoints to use for testing, where 0 means unlimited")
	rootCmd.PersistentFlags().Int("peer-connect-timeout", 600, "The number of seconds to wait for all required peers to connect if expect-peers > 0")
	rootCmd.PersistentFlags().Int("min-peer-connectivity", 0, "The minimum number of peers to which each peer must be connected before starting the load test")
	rootCmd.PersistentFlags().String("stats-output", "", "Where to store aggregate statistics (in CSV format) for the load test")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Increase output logging verbosity to DEBUG level")

	var coordCfg CoordinatorConfig
	coordCmd := &cobra.Command{
		Use:   "coordinator",
		Short: "Start load test application in COORDINATOR mode",
		Run: func(cmd *cobra.Command, args []string) {
			v := viper.GetViper()
			if configPath != "" {
				v.SetConfigFile(configPath)
				if err := v.ReadInConfig(); err != nil {
					logger.Error(fmt.Sprintf("reading config %s: %v", configPath, err))
					os.Exit(1)
				}
			}
			_ = v.BindPFlags(cmd.Root().PersistentFlags())
			cfg := ConfigFromViper(v)
			logger.Debug(fmt.Sprintf("Configuration: %s", cfg.ToJSON()))
			logger.Debug(fmt.Sprintf("Coordinator configuration: %s", coordCfg.ToJSON()))
			if err := cfg.Validate(); err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			if err := coordCfg.Validate(); err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			coord := NewCoordinator(&cfg, &coordCfg)
			if err := coord.Run(); err != nil {
				os.Exit(1)
			}
		},
	}
	coordCmd.PersistentFlags().StringVar(&coordCfg.BindAddr, "bind", "localhost:26670", "A host:port combination to which to bind the coordinator on which to listen for worker connections")
	coordCmd.PersistentFlags().IntVar(&coordCfg.ExpectWorkers, "expect-workers", 2, "The number of workers to expect to connect to the coordinator before starting load testing")
	coordCmd.PersistentFlags().IntVar(&coordCfg.WorkerConnectTimeout, "connect-timeout", 180, "The maximum number of seconds to wait for all workers to connect")
	coordCmd.PersistentFlags().IntVar(&coordCfg.ShutdownWait, "shutdown-wait", 0, "The number of seconds to wait after testing completes prior to shutting down the web server")
	coordCmd.PersistentFlags().IntVar(&coordCfg.LoadTestID, "load-test-id", 0, "The ID of the load test currently underway")

	var workerCfg WorkerConfig
	workerCmd := &cobra.Command{
		Use:   "worker",
		Short: "Start load test application in WORKER mode",
		Run: func(cmd *cobra.Command, args []string) {
			logger.Debug(fmt.Sprintf("Worker configuration: %s", workerCfg.ToJSON()))
			if err := workerCfg.Validate(); err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			worker, err := NewWorker(&workerCfg)
			if err != nil {
				logger.Error("Failed to create new worker", "err", err)
				os.Exit(1)
			}
			if err := worker.Run(); err != nil {
				os.Exit(1)
			}
		},
	}
	workerCmd.PersistentFlags().StringVar(&workerCfg.ID, "id", "", "An optional unique ID for this worker. Will show up in metrics and logs. If not specified, a UUID will be generated.")
	workerCmd.PersistentFlags().StringVar(&workerCfg.CoordAddr, "coordinator", "ws://localhost:26670", "The WebSockets URL on which to find the coordinator node")
	workerCmd.PersistentFlags().IntVar(&workerCfg.CoordConnectTimeout, "connect-timeout", 180, "The maximum number of seconds to keep trying to connect to the coordinator")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Display the version of cometbft-load-test and exit",
		Run: func(cmd *cobra.Command, args []string) {
			version := CLIVersion
			if len(cliVersionCommitID) > 0 {
				version = fmt.Sprintf("%s-%s", version, cliVersionCommitID)
			}
			fmt.Println("cometbft-load-test", version)
		},
	}

	rootCmd.AddCommand(coordCmd)
	rootCmd.AddCommand(workerCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(NewConfigCommand())
	// Single source for loadtest config: defaults on Viper, then env bound, then YAML/flags applied at run.
	v := viper.GetViper()
	SetLoadtestViperDefaults(v)
	BindLoadtestEnvToViper(v)
	return rootCmd
}

// SetLoadtestViperDefaults sets default values on Viper for loadtest.* keys.
// Precedence remains: CLI flags > YAML > env > these defaults.
func SetLoadtestViperDefaults(v *viper.Viper) {
	v.SetDefault("loadtest.chainId", "localperpxprotocol")
	v.SetDefault("loadtest.denom", "aperpx")
	v.SetDefault("loadtest.perps.scenario", "simple_perps")
}

// BindLoadtestEnvToViper binds LOADTEST_* and WS_* env vars to Viper keys so
// ConfigFromViper sees them (env overrides Viper defaults). Key names match YAML (loadtest.restUrl, etc.).
func BindLoadtestEnvToViper(v *viper.Viper) {
	_ = v.BindEnv("loadtest.chainId", "LOADTEST_CHAIN_ID")
	_ = v.BindEnv("loadtest.denom", "LOADTEST_DENOM")
	_ = v.BindEnv("loadtest.restUrl", "LOADTEST_REST_URL")
	_ = v.BindEnv("loadtest.perps.scenario", "LOADTEST_PERPS_SCENARIO")
	_ = v.BindEnv("loadtest.perps.markets", "LOADTEST_PERPS_MARKETS")
	_ = v.BindEnv("loadtest.perps.actionWeights", "LOADTEST_PERPS_ACTION_WEIGHTS")
	_ = v.BindEnv("loadtest.perps.maxTrackedOrders", "LOADTEST_PERPS_MAX_TRACKED_ORDERS")
	_ = v.BindEnv("loadtest.perps.minLeverage", "LOADTEST_PERPS_MIN_LEVERAGE")
	_ = v.BindEnv("loadtest.perps.maxLeverage", "LOADTEST_PERPS_MAX_LEVERAGE")
	_ = v.BindEnv("loadtest.perps.useMidPrice", "LOADTEST_PERPS_USE_MID_PRICE")
	_ = v.BindEnv("loadtest.perps.priceOffsetBps", "LOADTEST_PERPS_PRICE_OFFSET_BPS")
	_ = v.BindEnv("loadtest.perps.marketDataUrl", "LOADTEST_PERPS_MARKET_DATA_URL")
	_ = v.BindEnv("loadtest.perps.marketDataCacheTTLSeconds", "LOADTEST_PERPS_MARKET_DATA_CACHE_TTL_SECONDS")
	_ = v.BindEnv("loadtest.perps.deterministicClientIds", "LOADTEST_PERPS_DETERMINISTIC_CLIENT_IDS")
	_ = v.BindEnv("loadtest.perps.clientIdStart", "LOADTEST_PERPS_CLIENT_ID_START")
	_ = v.BindEnv("loadtest.perps.useTimestampNonce", "LOADTEST_PERPS_USE_TIMESTAMP_NONCE")
	_ = v.BindEnv("loadtest.perps.monitorTxStatus", "LOADTEST_PERPS_MONITOR_TX_STATUS")
	_ = v.BindEnv("loadtest.perps.txCheckDelayMs", "LOADTEST_PERPS_TX_CHECK_DELAY_MS")
	_ = v.BindEnv("loadtest.perps.txMaxChecks", "LOADTEST_PERPS_TX_MAX_CHECKS")
	_ = v.BindEnv("loadtest.perps.txMonitorSampleRate", "LOADTEST_PERPS_TX_MONITOR_SAMPLE_RATE")
	_ = v.BindEnv("loadtest.perps.maxRetries", "LOADTEST_PERPS_MAX_RETRIES")
	_ = v.BindEnv("loadtest.perps.retryDelayMs", "LOADTEST_PERPS_RETRY_DELAY_MS")
	_ = v.BindEnv("loadtest.perps.enableMarginCheck", "LOADTEST_PERPS_ENABLE_MARGIN_CHECK")
	_ = v.BindEnv("loadtest.perps.minMarginRatio", "LOADTEST_PERPS_MIN_MARGIN_RATIO")
	_ = v.BindEnv("loadtest.perps.onInsufficientMargin", "LOADTEST_PERPS_ON_INSUFFICIENT_MARGIN")
	_ = v.BindEnv("loadtest.perps.subticksPerTick", "LOADTEST_PERPS_SUBTICKS_PER_TICK")
	_ = v.BindEnv("loadtest.perps.stepBaseQuantums", "LOADTEST_PERPS_STEP_BASE_QUANTUMS")
	_ = v.BindEnv("loadtest.perps.rngSeed", "LOADTEST_PERPS_RNG_SEED")
	_ = v.BindEnv("loadtest.perps.feeGasLimit", "LOADTEST_PERPS_FEE_GAS_LIMIT")
	_ = v.BindEnv("loadtest.perps.feeMinGasPrice", "LOADTEST_PERPS_FEE_MIN_GAS_PRICE")
	_ = v.BindEnv("loadtest.perps.feeAltDenom", "LOADTEST_PERPS_FEE_ALT_DENOM")
	_ = v.BindEnv("loadtest.perps.feeAltMinGasPrice", "LOADTEST_PERPS_FEE_ALT_MIN_GAS_PRICE")
	_ = v.BindEnv("loadtest.wsEndpoints", "WS_ENDPOINTS")
	_ = v.BindEnv("loadtest.wsUrl", "WS_URL")
	_ = v.BindEnv("loadtest.debug.logWorkerIDs", "LOADTEST_DEBUG_LOG_WORKER_IDS")
}

func initLogLevel(logger logging.Logger) {
	if flagVerbose {
		logrus.SetLevel(logrus.DebugLevel)
		logger.Debug("Set logging level to DEBUG")
	}
}

// Run must be executed from your `main` function in your Go code. This can be
// used to fast-track the construction of your own load testing tool for your
// CometBFT ABCI application.
func Run(cli *CLIConfig) {
	logger := logging.NewLogrusLogger("main")

	if err := BuildCLI(cli, logger).Execute(); err != nil {
		logger.Error("Error", "err", err)
	}
}

func trapInterrupts(onKill func(), logger logging.Logger) chan struct{} {
	sigc := make(chan os.Signal, 1)
	cancelTrap := make(chan struct{})
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigc:
			logger.Info("Caught kill signal")
			onKill()
		case <-cancelTrap:
			logger.Debug("Interrupt trap cancelled")
		}
	}()
	return cancelTrap
}
