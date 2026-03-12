package seed

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	seedConfigKeyPrefix = "seed."
)

// NewSeedCommand returns the seed subcommand (Cobra + Viper).
func NewSeedCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Seed benchmark accounts for load testing",
		Long: `Seed benchmark accounts for load testing. In bank mode, funds accounts with the base token.
In perps mode, also deposits USDC margin to each account's subaccount 0 for perps load testing.`,
		RunE: runSeed,
	}

	cmd.Flags().IntP("workers", "w", 10, "Number of workers to seed")
	cmd.Flags().StringP("seed-key", "k", "", "Key name or mnemonic to use for seeding")
	cmd.Flags().String("seed-private-key", "", "Hex-encoded private key (overrides seed-key)")
	cmd.Flags().String("rpc", "http://localhost:36657", "RPC endpoint")
	cmd.Flags().String("rest-url", "", "REST base URL for balance/account queries (default: use rpc)")
	cmd.Flags().String("chain-id", defaultChainID, "Chain ID")
	cmd.Flags().String("denom", defaultDenom, "Token denomination")
	cmd.Flags().String("fund-amount", defaultFundAmount, "Amount to fund each account")
	cmd.Flags().Int("batch-size", defaultBatchSize, "Number of accounts to fund per transaction")
	cmd.Flags().String("mode", "bank", "Seeding mode: bank or perps")
	cmd.Flags().String("perps-deposit", defaultPerpsDeposit, "USDC quantums to deposit to each subaccount (perps mode)")
	cmd.Flags().String("config", "", "Path to YAML config file (optional)")

	bindSeedEnv(cmd)
	return cmd
}

func bindSeedEnv(cmd *cobra.Command) {
	_ = viper.BindEnv("workers", "LOADTEST_WORKERS")
	_ = viper.BindEnv("seed-key", "LOADTEST_SEED_KEY")
	_ = viper.BindEnv("seed-private-key", "LOADTEST_SEED_PRIVATE_KEY")
	_ = viper.BindEnv("rpc", "LOADTEST_RPC")
	_ = viper.BindEnv("rest-url", "LOADTEST_REST_URL")
	_ = viper.BindEnv("grpc", "LOADTEST_GRPC_URL")
	_ = viper.BindEnv("chain-id", "LOADTEST_CHAIN_ID")
	_ = viper.BindEnv("denom", "LOADTEST_DENOM")
	_ = viper.BindEnv("fund-amount", "LOADTEST_FUND_AMOUNT")
	_ = viper.BindEnv("batch-size", "LOADTEST_BATCH_SIZE")
	_ = viper.BindEnv("mode", "LOADTEST_PERPS_SEED_MODE")
	_ = viper.BindEnv("perps-deposit", "LOADTEST_PERPS_DEPOSIT")
}

func runSeed(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	if configPath != "" {
		viper.SetConfigFile(configPath)
		if err := viper.MergeInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return fmt.Errorf("invalid config file: %w", err)
			}
		}
	}
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return err
	}

	// REST URL comes from config only (ConfigFromViper reads loadtest.restUrl or --rest-url / LOADTEST_REST_URL).
	cfg := ConfigFromViper()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid seed configuration: %w", err)
	}

	fmt.Printf("Seeding %d benchmark accounts...\n", cfg.Workers)
	if cfg.SeedPrivateKey != "" {
		fmt.Printf("  Seed private key: [REDACTED] (using private key)\n")
	} else {
		fmt.Printf("  Seed key: %s\n", cfg.SeedKey)
	}
	fmt.Printf("  RPC: %s\n", cfg.RPC)
	if cfg.RestURL != "" {
		fmt.Printf("  REST: %s\n", cfg.RestURL)
	}
	fmt.Printf("  Chain ID: %s\n", cfg.ChainID)
	fmt.Printf("  Fund amount per account: %s\n", cfg.FundAmount)
	fmt.Printf("  Batch size: %d\n", cfg.BatchSize)
	if cfg.Mode == SeedModePerps {
		fmt.Printf("  Mode: perps (will deposit margin to subaccounts)\n")
		fmt.Printf("  Perps deposit per account: %s USDC quantums\n", cfg.PerpsDeposit)
	} else {
		fmt.Printf("  Mode: bank (standard bank funding only)\n")
	}

	var summary *SeedSummary
	var err error
	if cfg.Mode == SeedModePerps {
		summary, err = PerpsSeeder{Config: cfg}.Seed()
	} else {
		summary, err = BankSeeder{Config: cfg}.Seed()
	}
	if err != nil {
		return fmt.Errorf("seeding accounts: %w", err)
	}

	fmt.Println("✓ Account seeding complete!")
	if summary != nil {
		if data, err := json.MarshalIndent(summary, "", "  "); err == nil {
			fmt.Printf("SEED_SUMMARY_JSON=%s\n", string(data))
		}
	}
	return nil
}

// ConfigFromViper builds Config from Viper state. Used by runSeed and by tests.
// Precedence: flag > env > config file (seed.*) > default.
func ConfigFromViper() Config {
	modeStr := getSeedString("mode", "bank")
	var mode SeedMode
	switch strings.ToLower(modeStr) {
	case "perps":
		mode = SeedModePerps
	default:
		mode = SeedModeBank
	}

	// REST URL: loadtest.restUrl or seed.restUrl (when using --config), or --rest-url / LOADTEST_REST_URL.
	// Try both key casings because Viper may store YAML keys in lowercase. Prefer config over bound-flag default.
	restURL := strings.TrimSpace(viper.GetString("loadtest.restUrl"))
	if restURL == "" {
		restURL = strings.TrimSpace(viper.GetString("loadtest.resturl"))
	}
	if restURL == "" {
		restURL = strings.TrimSpace(viper.GetString("seed.restUrl"))
	}
	if restURL == "" {
		restURL = strings.TrimSpace(viper.GetString("seed.resturl"))
	}
	if restURL == "" {
		restURL = getSeedString("rest-url", "")
	}

	return Config{
		Workers:           getSeedInt("workers", 10),
		SeedKey:           getSeedString("seed-key", ""),
		SeedPrivateKey:    getSeedString("seed-private-key", ""),
		RPC:               getSeedString("rpc", "http://localhost:36657"),
		RestURL:           restURL,
		GRPC:              getSeedString("grpc", ""),
		ChainID:           getSeedString("chain-id", defaultChainID),
		Denom:             getSeedString("denom", defaultDenom),
		FundAmount:        getSeedString("fund-amount", defaultFundAmount),
		BatchSize:         getSeedInt("batch-size", defaultBatchSize),
		Mode:              mode,
		PerpsDeposit:      getSeedString("perps-deposit", defaultPerpsDeposit),
		PerpsDepositDenom: getSeedString("perps-deposit-denom", "usdc"),
	}
}

func getSeedInt(flagKey string, defaultVal int) int {
	if viper.IsSet(flagKey) {
		return viper.GetInt(flagKey)
	}
	configKey := seedConfigKeyPrefix + toCamelCase(flagKey)
	if viper.IsSet(configKey) {
		return viper.GetInt(configKey)
	}
	return defaultVal
}

func getSeedString(flagKey string, defaultVal string) string {
	if viper.IsSet(flagKey) {
		return viper.GetString(flagKey)
	}
	configKey := seedConfigKeyPrefix + toCamelCase(flagKey)
	if viper.IsSet(configKey) {
		return viper.GetString(configKey)
	}
	return defaultVal
}

func toCamelCase(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
