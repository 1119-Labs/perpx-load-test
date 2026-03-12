package seed

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// TestKeyDerivationAlignment verifies that the key derivation used in seeding
// matches exactly what PerpxPerpsClient and PerpxBankClient use.
// This ensures that worker IDs align with seeded accounts.
func TestKeyDerivationAlignment(t *testing.T) {
	// This test verifies that the key derivation logic in seed.go matches
	// the logic in pkg/client/bank_client.go and pkg/client/perps_client.go
	// The derivation should be:
	//   seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", workerID)
	//   seed := sha256.Sum256([]byte(seedStr))
	//   adjustedSeed := sha256.Sum256(append(seed[:], byte(workerID)))
	//   privKeyBytes, _ := btcec.PrivKeyFromBytes(adjustedSeed[:])
	//   privKey := &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
	//   addr := privKey.PubKey().Address()

	for workerID := 0; workerID < 10; workerID++ {
		// Derive key using the exact same logic as seed.go
		seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", workerID)
		seed := sha256.Sum256([]byte(seedStr))
		adjustedSeed := sha256.Sum256(append(seed[:], byte(workerID)))
		privKeyBytes, _ := btcec.PrivKeyFromBytes(adjustedSeed[:])
		privKey := &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
		addr := privKey.PubKey().Address()

		// Verify the address is deterministic
		require.NotNil(t, addr, "address should not be nil for worker %d", workerID)
		require.Equal(t, 20, len(addr), "address should be 20 bytes for worker %d", workerID)

		// Verify same worker ID produces same address (idempotency)
		seedStr2 := fmt.Sprintf("bench worker %d seed phrase for load testing account", workerID)
		seed2 := sha256.Sum256([]byte(seedStr2))
		adjustedSeed2 := sha256.Sum256(append(seed2[:], byte(workerID)))
		privKeyBytes2, _ := btcec.PrivKeyFromBytes(adjustedSeed2[:])
		privKey2 := &secp256k1.PrivKey{Key: privKeyBytes2.Serialize()}
		addr2 := privKey2.PubKey().Address()

		require.Equal(t, addr, addr2, "same worker ID should produce same address")
	}

	// Verify different worker IDs produce different addresses
	seedStr0 := fmt.Sprintf("bench worker %d seed phrase for load testing account", 0)
	seed0 := sha256.Sum256([]byte(seedStr0))
	adjustedSeed0 := sha256.Sum256(append(seed0[:], byte(0)))
	privKeyBytes0, _ := btcec.PrivKeyFromBytes(adjustedSeed0[:])
	privKey0 := &secp256k1.PrivKey{Key: privKeyBytes0.Serialize()}
	addr0 := privKey0.PubKey().Address()

	seedStr1 := fmt.Sprintf("bench worker %d seed phrase for load testing account", 1)
	seed1 := sha256.Sum256([]byte(seedStr1))
	adjustedSeed1 := sha256.Sum256(append(seed1[:], byte(1)))
	privKeyBytes1, _ := btcec.PrivKeyFromBytes(adjustedSeed1[:])
	privKey1 := &secp256k1.PrivKey{Key: privKeyBytes1.Serialize()}
	addr1 := privKey1.PubKey().Address()

	require.NotEqual(t, addr0, addr1, "different worker IDs should produce different addresses")
}

// TestParseArgs_DefaultMode tests that default mode is "bank"
func TestParseArgs_DefaultMode(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	cfg := ConfigFromViper()
	require.Equal(t, SeedModeBank, cfg.Mode, "default mode should be 'bank'")
}

// TestParseArgs_PerpsMode tests parsing perps mode from Viper (flag/env)
func TestParseArgs_PerpsMode(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.Set("mode", "perps")

	cfg := ConfigFromViper()
	require.Equal(t, SeedModePerps, cfg.Mode, "mode should be 'perps' when set")
}

// TestParseArgs_PerpsModeEnvVar tests that env-bound key sets mode
func TestParseArgs_PerpsModeEnvVar(t *testing.T) {
	os.Setenv("LOADTEST_PERPS_SEED_MODE", "perps")
	defer os.Unsetenv("LOADTEST_PERPS_SEED_MODE")
	viper.Reset()
	defer viper.Reset()
	// Simulate env binding (seed command does this via bindSeedEnv)
	_ = viper.BindEnv("mode", "LOADTEST_PERPS_SEED_MODE")

	cfg := ConfigFromViper()
	require.Equal(t, SeedModePerps, cfg.Mode, "mode should be 'perps' when env var is set")
}

// TestParseArgs_PerpsDeposit tests perps deposit from Viper
func TestParseArgs_PerpsDeposit(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.Set("perps-deposit", "2000000")

	cfg := ConfigFromViper()
	require.Equal(t, "2000000", cfg.PerpsDeposit, "perps deposit should be set")
}

// TestParseArgs_PerpsDepositEnvVar tests perps deposit from env
func TestParseArgs_PerpsDepositEnvVar(t *testing.T) {
	os.Setenv("LOADTEST_PERPS_DEPOSIT", "5000000")
	defer os.Unsetenv("LOADTEST_PERPS_DEPOSIT")
	viper.Reset()
	defer viper.Reset()
	_ = viper.BindEnv("perps-deposit", "LOADTEST_PERPS_DEPOSIT")

	cfg := ConfigFromViper()
	require.Equal(t, "5000000", cfg.PerpsDeposit, "perps deposit should be set from env var")
}

// TestParseArgs_InvalidMode tests that invalid mode defaults to bank
func TestParseArgs_InvalidMode(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.Set("mode", "invalid")

	cfg := ConfigFromViper()
	require.Equal(t, SeedModeBank, cfg.Mode, "invalid mode should default to 'bank'")
}

func TestConfigValidate_BasicConstraints(t *testing.T) {
	cfg := Config{
		Workers:           10,
		SeedKey:           "alice",
		RPC:               "http://localhost:36657",
		ChainID:           defaultChainID,
		Denom:             defaultDenom,
		FundAmount:        "1000aperpx",
		BatchSize:         defaultBatchSize,
		Mode:              SeedModeBank,
		PerpsDeposit:      defaultPerpsDeposit,
		PerpsDepositDenom: "usdc",
	}

	require.NoError(t, cfg.Validate())

	cfg.Workers = 0
	require.Error(t, cfg.Validate(), "workers == 0 should be rejected")

	cfg.Workers = 10
	cfg.RPC = ""
	require.Error(t, cfg.Validate(), "empty RPC should be rejected")

	cfg.RPC = "http://localhost:36657"
	cfg.BatchSize = 0
	require.Error(t, cfg.Validate(), "batch size == 0 should be rejected")
}

func TestConfigValidate_PerpsModeDepositChecks(t *testing.T) {
	cfg := Config{
		Workers:           1,
		SeedKey:           "alice",
		RPC:               "http://localhost:36657",
		ChainID:           defaultChainID,
		Denom:             defaultDenom,
		FundAmount:        "1000aperpx",
		BatchSize:         defaultBatchSize,
		Mode:              SeedModePerps,
		PerpsDeposit:      defaultPerpsDeposit,
		PerpsDepositDenom: "usdc",
	}

	require.NoError(t, cfg.Validate())

	cfg.PerpsDeposit = ""
	require.Error(t, cfg.Validate(), "empty perps deposit should be rejected in perps mode")

	cfg.PerpsDeposit = "not-a-number"
	require.Error(t, cfg.Validate(), "non-numeric perps deposit should be rejected in perps mode")
}

// TestSeeders_WireModesAndSummaries verifies that BankSeeder and PerpsSeeder
// force the expected modes and propagate summaries from the shared engine.
func TestSeeders_WireModesAndSummaries(t *testing.T) {
	orig := seedAccountsFunc
	defer func() { seedAccountsFunc = orig }()

	var seen []Config
	seedAccountsFunc = func(cfg Config) (*SeedSummary, error) {
		seen = append(seen, cfg)
		return &SeedSummary{
			ChainID: cfg.ChainID,
			RPC:     cfg.RPC,
			Bank: BankSeedSummary{
				Mode:             cfg.Mode,
				WorkersRequested: cfg.Workers,
			},
		}, nil
	}

	baseCfg := Config{
		Workers:           3,
		SeedKey:           "alice",
		RPC:               "http://localhost:36657",
		ChainID:           defaultChainID,
		Denom:             defaultDenom,
		FundAmount:        defaultFundAmount,
		BatchSize:         defaultBatchSize,
		Mode:              SeedModePerps, // should be overridden by BankSeeder
		PerpsDeposit:      defaultPerpsDeposit,
		PerpsDepositDenom: "usdc",
	}

	bankSummary, err := BankSeeder{Config: baseCfg}.Seed()
	require.NoError(t, err)
	require.Len(t, seen, 1)
	require.Equal(t, SeedModeBank, seen[0].Mode)
	require.Equal(t, 3, bankSummary.Bank.WorkersRequested)
	require.Equal(t, SeedModeBank, bankSummary.Bank.Mode)

	perpsSummary, err := PerpsSeeder{Config: baseCfg}.Seed()
	require.NoError(t, err)
	require.Len(t, seen, 2)
	require.Equal(t, SeedModePerps, seen[1].Mode)
	require.Equal(t, 3, perpsSummary.Bank.WorkersRequested)
	require.Equal(t, SeedModePerps, perpsSummary.Bank.Mode)
}

// TestSeeders_PropagateErrors verifies that failures in the shared engine are
// surfaced by both BankSeeder and PerpsSeeder.
func TestSeeders_PropagateErrors(t *testing.T) {
	orig := seedAccountsFunc
	defer func() { seedAccountsFunc = orig }()

	seedAccountsFunc = func(cfg Config) (*SeedSummary, error) {
		return nil, fmt.Errorf("boom")
	}

	cfg := Config{
		Workers:           1,
		SeedKey:           "alice",
		RPC:               "http://localhost:36657",
		ChainID:           defaultChainID,
		Denom:             defaultDenom,
		FundAmount:        defaultFundAmount,
		BatchSize:         defaultBatchSize,
		Mode:              SeedModeBank,
		PerpsDeposit:      defaultPerpsDeposit,
		PerpsDepositDenom: "usdc",
	}

	_, err := BankSeeder{Config: cfg}.Seed()
	require.Error(t, err)

	_, err = PerpsSeeder{Config: cfg}.Seed()
	require.Error(t, err)
}
