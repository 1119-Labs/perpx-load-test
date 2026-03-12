package seed

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultBatchSize  = 50
	defaultFundAmount = "1000000000000000000aperpx"
	defaultDenom      = "aperpx"
	defaultChainID    = "localperpxprotocol"
	// Default perps margin deposit per worker subaccount, in USDC quantums.
	// 1 USDC = 1e6 quantums, so 10,000 USDC = 10,000 * 1e6 = 1e10 quantums.
	// We choose a conservative default so that stateful long-term orders used
	// by the perps loadtest are well-collateralized on localnet.
	defaultPerpsDeposit  = "10000000000" // 10,000 USDC in quantums
	defaultSubaccountNum = 0             // Perps clients use subaccount 0
)

// SeedMode represents the seeding mode
type SeedMode string

const (
	SeedModeBank  SeedMode = "bank"  // Default: only bank funding
	SeedModePerps SeedMode = "perps" // Perps-aware: bank funding + margin deposits
)

// Config holds seeding configuration
type Config struct {
	Workers        int
	SeedKey        string
	SeedPrivateKey string // Optional: hex-encoded private key (takes precedence over SeedKey)
	RPC            string
	// RestURL is the REST base URL for balance/account queries
	RestURL string
	// GRPC is an optional gRPC endpoint override. If set, it will be preferred
	// when deriving the gRPC address for broadcasting (with envconfig still
	// able to override via LOADTEST_GRPC_URL).
	GRPC       string
	ChainID    string
	Denom      string
	FundAmount string
	BatchSize  int

	// Perps-specific configuration
	Mode              SeedMode // Seeding mode: "bank" (default) or "perps"
	PerpsDeposit      string   // USDC quantums to deposit to subaccounts (default: "1000000")
	PerpsDepositDenom string   // Denom for perps deposits (default: "usdc", but uses quantums)
}

// BankSeedSummary captures high-level results for bank-mode seeding.
type BankSeedSummary struct {
	Mode                  SeedMode `json:"mode"`
	WorkersRequested      int      `json:"workers_requested"`
	AccountsAlreadyFunded int      `json:"accounts_already_funded"`
	AccountsFunded        int      `json:"accounts_funded"`
	Batches               int      `json:"batches"`
}

// PerpsSeedSummary captures high-level results for perps-mode seeding.
type PerpsSeedSummary struct {
	SubaccountsSeeded int `json:"subaccounts_seeded"`
	Batches           int `json:"batches"`
}

// SeedSummary is a machine-readable summary artifact for a seeding run.
type SeedSummary struct {
	ChainID string            `json:"chain_id"`
	RPC     string            `json:"rpc"`
	Bank    BankSeedSummary   `json:"bank"`
	Perps   *PerpsSeedSummary `json:"perps,omitempty"`
}

// BankSeeder runs bank-mode seeding.
type BankSeeder struct {
	Config Config
}

// PerpsSeeder runs perps-mode (bank + margin) seeding.
type PerpsSeeder struct {
	Config Config
}

// seedAccountsFunc is a hookable entrypoint for tests to inject fakes.
var seedAccountsFunc = seedAccounts

// Seed runs bank-mode seeding and returns a summary.
func (s BankSeeder) Seed() (*SeedSummary, error) {
	cfg := s.Config
	cfg.Mode = SeedModeBank
	return seedAccountsFunc(cfg)
}

// Seed runs perps-mode seeding (bank + margin) and returns a summary.
func (s PerpsSeeder) Seed() (*SeedSummary, error) {
	cfg := s.Config
	cfg.Mode = SeedModePerps
	return seedAccountsFunc(cfg)
}

// Validate performs basic sanity checks on the configuration before running a
// seeding operation. It is intentionally conservative and focuses on catching
// obvious misconfigurations early.
func (c Config) Validate() error {
	if c.Workers <= 0 {
		return fmt.Errorf("workers must be > 0")
	}
	if strings.TrimSpace(c.RPC) == "" {
		return fmt.Errorf("RPC endpoint cannot be empty")
	}
	if strings.TrimSpace(c.ChainID) == "" {
		return fmt.Errorf("chain ID cannot be empty")
	}
	if strings.TrimSpace(c.Denom) == "" {
		return fmt.Errorf("denom cannot be empty")
	}
	if c.BatchSize <= 0 {
		return fmt.Errorf("batch size must be > 0")
	}
	if c.Mode != SeedModeBank && c.Mode != SeedModePerps {
		return fmt.Errorf("invalid seed mode %q (expected %q or %q)", c.Mode, SeedModeBank, SeedModePerps)
	}
	if c.Mode == SeedModePerps {
		if strings.TrimSpace(c.PerpsDeposit) == "" {
			return fmt.Errorf("perps deposit amount cannot be empty in perps mode")
		}
		if _, err := strconv.ParseUint(c.PerpsDeposit, 10, 64); err != nil {
			return fmt.Errorf("invalid perps deposit amount %q: %w", c.PerpsDeposit, err)
		}
	}
	return nil
}
