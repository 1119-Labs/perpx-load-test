package loadtest

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	loadtestConfigPrefix = "loadtest."
)

// NewConfigCommand returns the config subcommand (e.g. config print-loadtest).
// Used by scripts to read loadtest.rate, loadtest.connections, etc. from YAML
// when running with --config.
func NewConfigCommand() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or print configuration",
	}
	configCmd.AddCommand(newPrintLoadtestCommand())
	return configCmd
}

func newPrintLoadtestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "print-loadtest",
		Short: "Print loadtest section from YAML as KEY=value for scripting",
		Long:  "Reads the given config file and prints loadtest.rate, loadtest.connections, loadtest.time, etc. as shell-exportable KEY=value lines. Used by run_perps_load.sh when --config is set.",
		RunE:  runPrintLoadtest,
	}
	cmd.Flags().String("config", "", "Path to YAML config file (required)")
	return cmd
}

func runPrintLoadtest(cmd *cobra.Command, _ []string) error {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		return fmt.Errorf("--config is required")
	}
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	rate := 50
	if v.IsSet(loadtestConfigPrefix + "rate") {
		rate = v.GetInt(loadtestConfigPrefix + "rate")
	}
	connections := 5
	if v.IsSet(loadtestConfigPrefix + "connections") {
		connections = v.GetInt(loadtestConfigPrefix + "connections")
	}
	duration := 60
	if v.IsSet(loadtestConfigPrefix + "time") {
		duration = v.GetInt(loadtestConfigPrefix + "time")
	}
	broadcastTxMethod := "async"
	if v.IsSet(loadtestConfigPrefix + "broadcastTxMethod") {
		broadcastTxMethod = v.GetString(loadtestConfigPrefix + "broadcastTxMethod")
	}

	fmt.Printf("RATE=%d\nCONNECTIONS=%d\nDURATION=%d\nBROADCAST_TX_METHOD=%s\n", rate, connections, duration, broadcastTxMethod)

	// Optional: expose seed.workers so scripts can keep their summary and
	// worker-sharding math in sync with the actual seeded worker count.
	if v.IsSet("seed.workers") {
		workers := v.GetInt("seed.workers")
		fmt.Printf("SEED_WORKERS=%d\n", workers)
	}

	// Optional: expose perps scenario + useMidPrice so scripts can honour
	// YAML-driven behavior when constructing LOADTEST_PERPS_* env vars and
	// when printing run summaries.
	if v.IsSet(loadtestConfigPrefix + "perps.scenario") {
		scenario := v.GetString(loadtestConfigPrefix + "perps.scenario")
		if scenario != "" {
			fmt.Printf("PERPS_SCENARIO=%s\n", scenario)
		}
	}
	if v.IsSet(loadtestConfigPrefix + "perps.useMidPrice") {
		useMidPrice := v.GetBool(loadtestConfigPrefix + "perps.useMidPrice")
		fmt.Printf("PERPS_USE_MID_PRICE=%t\n", useMidPrice)
	}

	if v.IsSet(loadtestConfigPrefix + "restUrl") {
		restURL := v.GetString(loadtestConfigPrefix + "restUrl")
		if restURL != "" {
			fmt.Printf("REST_URL=%s\n", restURL)
		}
	}

	if v.IsSet(loadtestConfigPrefix + "endpoints") {
		endpoints := v.GetStringSlice(loadtestConfigPrefix + "endpoints")
		if len(endpoints) > 0 {
			fmt.Printf("WS_URL=%s\n", endpoints[0])
		}
	}

	// If seed.rpc is set, expose it as RPC_URL so scripts can respect
	// precedence CLI > config file > env > defaults when selecting RPC.
	if v.IsSet("seed.rpc") {
		rpcURL := v.GetString("seed.rpc")
		if rpcURL != "" {
			fmt.Printf("RPC_URL=%s\n", rpcURL)
		}
	}

	// Expose CHAIN_ID so scripts can honour YAML-driven chain IDs instead of
	// always inferring them from RPC /status.
	//
	// Preference order:
	//   1. loadtest.chainId
	//   2. seed.chainId
	if v.IsSet(loadtestConfigPrefix + "chainId") {
		chainID := v.GetString(loadtestConfigPrefix + "chainId")
		if chainID != "" {
			fmt.Printf("CHAIN_ID=%s\n", chainID)
		}
	} else if v.IsSet("seed.chainId") {
		chainID := v.GetString("seed.chainId")
		if chainID != "" {
			fmt.Printf("CHAIN_ID=%s\n", chainID)
		}
	}
	return nil
}

