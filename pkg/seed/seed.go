package seed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cosmossdk.io/math"
	"github.com/1119-Labs/perpx-chain/protocol/app"
)

const (
	defaultBatchSize  = 50
	defaultFundAmount = "1000000aperpx"
	defaultDenom      = "aperpx"
	defaultChainID    = "localperpxprotocol"
)

// Config holds seeding configuration
type Config struct {
	Workers        int
	SeedKey        string
	SeedPrivateKey string // Optional: hex-encoded private key (takes precedence over SeedKey)
	RPC            string
	ChainID        string
	Denom          string
	FundAmount     string
	BatchSize      int
}

// Run executes the seed command
func Run(args []string) {
	cfg := parseArgs(args)

	fmt.Printf("Seeding %d benchmark accounts...\n", cfg.Workers)
	if cfg.SeedPrivateKey != "" {
		fmt.Printf("  Seed private key: [REDACTED] (using private key)\n")
	} else {
		fmt.Printf("  Seed key: %s\n", cfg.SeedKey)
	}
	fmt.Printf("  RPC: %s\n", cfg.RPC)
	fmt.Printf("  Chain ID: %s\n", cfg.ChainID)
	fmt.Printf("  Fund amount per account: %s\n", cfg.FundAmount)
	fmt.Printf("  Batch size: %d\n", cfg.BatchSize)

	if err := seedAccounts(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error seeding accounts: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Account seeding complete!")
}

func parseArgs(args []string) Config {
	cfg := Config{
		Workers:        10,
		SeedKey:        getEnv("LOADTEST_SEED_KEY", "alice"),
		SeedPrivateKey: getEnv("LOADTEST_SEED_PRIVATE_KEY", ""),
		RPC:            getEnv("LOADTEST_RPC", "http://localhost:36657"),
		ChainID:        getEnv("LOADTEST_CHAIN_ID", defaultChainID),
		Denom:          getEnv("LOADTEST_DENOM", defaultDenom),
		FundAmount:     getEnv("LOADTEST_FUND_AMOUNT", defaultFundAmount),
		BatchSize:      defaultBatchSize,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workers", "-w":
			if i+1 < len(args) {
				cfg.Workers, _ = strconv.Atoi(args[i+1])
				i++
			}
		case "--seed-key", "-k":
			if i+1 < len(args) {
				cfg.SeedKey = args[i+1]
				i++
			}
		case "--seed-private-key", "--private-key", "-p":
			if i+1 < len(args) {
				cfg.SeedPrivateKey = args[i+1]
				i++
			}
		case "--rpc", "-r":
			if i+1 < len(args) {
				cfg.RPC = args[i+1]
				i++
			}
		case "--chain-id":
			if i+1 < len(args) {
				cfg.ChainID = args[i+1]
				i++
			}
		case "--denom":
			if i+1 < len(args) {
				cfg.Denom = args[i+1]
				i++
			}
		case "--fund-amount":
			if i+1 < len(args) {
				cfg.FundAmount = args[i+1]
				i++
			}
		case "--batch-size":
			if i+1 < len(args) {
				cfg.BatchSize, _ = strconv.Atoi(args[i+1])
				i++
			}
		case "--help", "-h":
			printHelp()
			os.Exit(0)
		}
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func printHelp() {
	fmt.Println(`Usage: perpx-load-test seed [OPTIONS]

Options:
  --workers, -w N          Number of workers to seed (default: 10)
  --seed-key, -k KEY        Key name or mnemonic to use for seeding (default: alice)
  --seed-private-key, -p KEY  Hex-encoded private key to use for seeding (takes precedence over --seed-key)
  --rpc, -r URL            RPC endpoint (default: http://localhost:36657)
  --chain-id ID            Chain ID (default: localperpxprotocol)
  --denom DENOM            Token denomination (default: aperpx)
  --fund-amount AMOUNT      Amount to fund each account (default: 1000000aperpx)
  --batch-size N           Number of accounts to fund per transaction (default: 50)
  --help, -h               Show this help message

Environment Variables:
  LOADTEST_SEED_KEY            Override seed key
  LOADTEST_SEED_PRIVATE_KEY    Override seed private key (hex-encoded)
  LOADTEST_RPC                 Override RPC endpoint
  LOADTEST_CHAIN_ID            Override chain ID
  LOADTEST_DENOM               Override denomination
  LOADTEST_FUND_AMOUNT         Override fund amount`)
}

func seedAccounts(cfg Config) error {
	// Parse fund amount
	fundCoin, err := sdk.ParseCoinNormalized(cfg.FundAmount)
	if err != nil {
		return fmt.Errorf("invalid fund amount: %w", err)
	}

	// Calculate total needed
	totalNeeded := fundCoin.Amount.Mul(math.NewInt(int64(cfg.Workers)))
	estimatedFees := sdk.NewCoins(sdk.NewCoin(cfg.Denom, math.NewInt(int64(cfg.Workers)*10000))) // ~10k per tx
	totalRequired := sdk.NewCoins(sdk.NewCoin(cfg.Denom, totalNeeded.Add(estimatedFees.AmountOf(cfg.Denom))))

	fmt.Printf("Total required: %s\n", totalRequired)

	// Setup encoding config
	encCfg := app.GetEncodingConfig()

	// Get or create seed key
	var seedPrivKey cryptotypes.PrivKey
	var seedAddr sdk.AccAddress

	// If private key is provided, use it directly (takes precedence)
	if cfg.SeedPrivateKey != "" {
		// Parse hex-encoded private key
		keyBytes, err := hex.DecodeString(strings.TrimPrefix(cfg.SeedPrivateKey, "0x"))
		if err != nil {
			return fmt.Errorf("failed to decode private key (must be hex-encoded): %w", err)
		}
		if len(keyBytes) != 32 {
			return fmt.Errorf("invalid private key length: expected 32 bytes, got %d", len(keyBytes))
		}
		// Create secp256k1 private key from bytes
		privKeyBytes, _ := btcec.PrivKeyFromBytes(keyBytes)
		seedPrivKey = &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
		seedAddr = sdk.AccAddress(seedPrivKey.PubKey().Address())
	} else {
		// Fall back to mnemonic-based key derivation
		// If the user passed the common dev key name "alice", transparently
		// substitute the actual alice validator mnemonic from localnet config.yml
		// so the command works out-of-the-box.
		if cfg.SeedKey == "alice" {
			// NOTE: This is the actual alice validator mnemonic from protocol/deployment/localnet/config.yml
			// This is a development-only mnemonic and MUST NOT be used in production.
			cfg.SeedKey = "merge panther lobster crazy road hollow amused security before critic about cliff exhibit cause coyote talent happy where lion river tobacco option coconut small"
		}

		// Treat SeedKey as either a full mnemonic (contains spaces) or fail fast.
		// In the future this can be extended to look up named keys from a keyring.
		if strings.Contains(cfg.SeedKey, " ") {
			// It's a mnemonic
			hdPath := hd.CreateHDPath(118, 0, 0).String()
			derivedPriv, err := hd.Secp256k1.Derive()(cfg.SeedKey, "", hdPath)
			if err != nil {
				return fmt.Errorf("failed to derive key from mnemonic: %w", err)
			}
			seedPrivKey = hd.Secp256k1.Generate()(derivedPriv)
			seedAddr = sdk.AccAddress(seedPrivKey.PubKey().Address())
		} else {
			return fmt.Errorf("seed-key %q is not a mnemonic; please provide a mnemonic, use \"alice\", or use --seed-private-key", cfg.SeedKey)
		}
	}

	fmt.Printf("Seed address: %s\n", seedAddr.String())

	// Use REST API for balance queries to avoid gRPC frame size limits
	// The "http2: frame too large" error occurs with gRPC when responses are large
	// Convert RPC URL (port 36657) to REST API URL (port 31317)
	restURL := strings.Replace(cfg.RPC, ":36657", ":31317", 1)
	if !strings.Contains(restURL, ":31317") {
		// If port wasn't 36657, try to infer REST port or use default
		restURL = strings.Replace(cfg.RPC, ":26657", ":1317", 1)
		if !strings.Contains(restURL, ":1317") {
			// Default to localhost:31317 if we can't determine
			restURL = "http://localhost:31317"
		}
	}

	restClient := &http.Client{Timeout: 10 * time.Second}

	// Check seed balance via REST API
	balanceURL := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", restURL, seedAddr.String())
	balanceResp, err := restClient.Get(balanceURL)
	if err != nil {
		return fmt.Errorf("failed to query seed balance: %w", err)
	}
	defer balanceResp.Body.Close()

	if balanceResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(balanceResp.Body)
		return fmt.Errorf("failed to query seed balance: HTTP %d: %s", balanceResp.StatusCode, string(body))
	}

	var balanceData struct {
		Balances []struct {
			Denom  string `json:"denom"`
			Amount string `json:"amount"`
		} `json:"balances"`
	}
	if err := json.NewDecoder(balanceResp.Body).Decode(&balanceData); err != nil {
		return fmt.Errorf("failed to decode balance response: %w", err)
	}

	seedBalance := sdk.NewCoins()
	for _, bal := range balanceData.Balances {
		amount, ok := math.NewIntFromString(bal.Amount)
		if !ok {
			return fmt.Errorf("invalid amount: %s", bal.Amount)
		}
		seedBalance = seedBalance.Add(sdk.NewCoin(bal.Denom, amount))
	}
	fmt.Printf("Seed balance: %s\n", seedBalance)

	// Check if seed has enough funds
	if seedBalance.AmountOf(cfg.Denom).LT(totalRequired.AmountOf(cfg.Denom)) {
		return fmt.Errorf("insufficient funds: seed has %s, needs %s",
			seedBalance.AmountOf(cfg.Denom), totalRequired.AmountOf(cfg.Denom))
	}

	// Get seed account info (sequence, account number) via REST API
	accountURL := fmt.Sprintf("%s/cosmos/auth/v1beta1/accounts/%s", restURL, seedAddr.String())
	accountResp, err := restClient.Get(accountURL)
	if err != nil {
		return fmt.Errorf("failed to query seed account: %w", err)
	}
	defer accountResp.Body.Close()

	if accountResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(accountResp.Body)
		return fmt.Errorf("failed to query seed account: HTTP %d: %s", accountResp.StatusCode, string(body))
	}

	var accountData struct {
		Account struct {
			Type          string `json:"@type"`
			Address       string `json:"address"`
			AccountNumber string `json:"account_number"`
			Sequence      string `json:"sequence"`
		} `json:"account"`
	}
	if err := json.NewDecoder(accountResp.Body).Decode(&accountData); err != nil {
		return fmt.Errorf("failed to decode account response: %w", err)
	}

	// Parse account number and sequence
	accountNum, err := strconv.ParseUint(accountData.Account.AccountNumber, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse account number: %w", err)
	}
	sequence, err := strconv.ParseUint(accountData.Account.Sequence, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse sequence: %w", err)
	}

	fmt.Printf("Seed account number: %d, sequence: %d\n", accountNum, sequence)

	// Generate bench keys deterministically
	benchKeys := make([]struct {
		privKey cryptotypes.PrivKey
		addr    sdk.AccAddress
	}, cfg.Workers)

	for i := 0; i < cfg.Workers; i++ {
		// Generate deterministic key from seed (similar to regen_genesis_addresses.go)
		seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", i)
		seed := sha256.Sum256([]byte(seedStr))
		// Use worker index as path for additional determinism
		adjustedSeed := sha256.Sum256(append(seed[:], byte(i)))
		privKeyBytes, _ := btcec.PrivKeyFromBytes(adjustedSeed[:])
		benchKeys[i].privKey = &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
		benchKeys[i].addr = sdk.AccAddress(benchKeys[i].privKey.PubKey().Address())
	}

	// Check which accounts need funding (use REST API to avoid gRPC frame limits)
	needsFunding := make([]sdk.AccAddress, 0, cfg.Workers)
	for _, bk := range benchKeys {
		balanceURL := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", restURL, bk.addr.String())
		balanceResp, err := restClient.Get(balanceURL)
		if err != nil || balanceResp.StatusCode != http.StatusOK {
			// Account might not exist, assume it needs funding
			if balanceResp != nil {
				balanceResp.Body.Close()
			}
			needsFunding = append(needsFunding, bk.addr)
			continue
		}

		var balanceData struct {
			Balances []struct {
				Denom  string `json:"denom"`
				Amount string `json:"amount"`
			} `json:"balances"`
		}
		if err := json.NewDecoder(balanceResp.Body).Decode(&balanceData); err != nil {
			balanceResp.Body.Close()
			needsFunding = append(needsFunding, bk.addr)
			continue
		}
		balanceResp.Body.Close()

		balance := sdk.NewCoins()
		for _, bal := range balanceData.Balances {
			amount, ok := math.NewIntFromString(bal.Amount)
			if ok {
				balance = balance.Add(sdk.NewCoin(bal.Denom, amount))
			}
		}
		if balance.AmountOf(cfg.Denom).LT(fundCoin.Amount) {
			needsFunding = append(needsFunding, bk.addr)
		}
	}

	if len(needsFunding) == 0 {
		fmt.Println("All accounts already funded!")
		return nil
	}

	fmt.Printf("Funding %d accounts in batches of %d...\n", len(needsFunding), cfg.BatchSize)

	// Fund accounts in batches
	currentSeq := sequence
	for i := 0; i < len(needsFunding); i += cfg.BatchSize {
		end := i + cfg.BatchSize
		if end > len(needsFunding) {
			end = len(needsFunding)
		}
		batch := needsFunding[i:end]

		// Build multi-msg transaction
		msgs := make([]sdk.Msg, 0, len(batch))
		for _, addr := range batch {
			msgs = append(msgs, &banktypes.MsgSend{
				FromAddress: seedAddr.String(),
				ToAddress:   addr.String(),
				Amount:      sdk.NewCoins(fundCoin),
			})
		}

		// Create and sign transaction
		txBuilder := encCfg.TxConfig.NewTxBuilder()
		if err := txBuilder.SetMsgs(msgs...); err != nil {
			return fmt.Errorf("failed to set messages: %w", err)
		}

		// Set fees based on gas limit and minimum gas price
		// Minimum gas price: 25000000000aperpx per unit of gas (from cmd/perpxd/cmd/config.go)
		// Gas limit: 100k per message
		gasLimit := 100000 * uint64(len(batch))
		minGasPrice := math.NewInt(25000000000) // 25 billion aperpx per unit of gas
		feeAmount := minGasPrice.Mul(math.NewInt(int64(gasLimit)))
		feeCoins := sdk.NewCoins(sdk.NewCoin(cfg.Denom, feeAmount))
		txBuilder.SetFeeAmount(feeCoins)
		txBuilder.SetGasLimit(gasLimit)

		// First round: set empty signatures to gather signer infos (required for SIGN_MODE_DIRECT)
		sigV2Empty := signing.SignatureV2{
			PubKey: seedPrivKey.PubKey(),
			Data: &signing.SingleSignatureData{
				SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
				Signature: nil,
			},
			Sequence: currentSeq,
		}
		if err := txBuilder.SetSignatures(sigV2Empty); err != nil {
			return fmt.Errorf("failed to set empty signature: %w", err)
		}

		// Second round: actually sign the transaction
		signerData := authsigning.SignerData{
			Address:       seedAddr.String(),
			ChainID:       cfg.ChainID,
			AccountNumber: accountNum,
			Sequence:      currentSeq,
			PubKey:        seedPrivKey.PubKey(),
		}

		sigV2, err := tx.SignWithPrivKey(
			context.Background(),
			signing.SignMode_SIGN_MODE_DIRECT,
			signerData,
			txBuilder,
			seedPrivKey,
			encCfg.TxConfig,
			currentSeq,
		)
		if err != nil {
			return fmt.Errorf("failed to sign: %w", err)
		}

		if err := txBuilder.SetSignatures(sigV2); err != nil {
			return fmt.Errorf("failed to set signature: %w", err)
		}

		// Encode transaction
		txBytes, err := encCfg.TxConfig.TxEncoder()(txBuilder.GetTx())
		if err != nil {
			return fmt.Errorf("failed to encode transaction: %w", err)
		}

		// Broadcast transaction (using sync mode to ensure it's included)
		// Use gRPC for broadcasting (convert RPC port to gRPC port: 36657 -> 39090)
		grpcURL := strings.Replace(cfg.RPC, ":36657", ":39090", 1)
		if !strings.Contains(grpcURL, ":39090") {
			grpcURL = strings.Replace(cfg.RPC, ":26657", ":9090", 1)
			if !strings.Contains(grpcURL, ":9090") {
				grpcURL = "http://localhost:39090"
			}
		}
		grpcAddr := strings.TrimPrefix(grpcURL, "http://")
		grpcConn, err := grpc.Dial(
			grpcAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return fmt.Errorf("failed to connect to gRPC for broadcasting: %w", err)
		}
		txClient := txtypes.NewServiceClient(grpcConn)
		// Use BROADCAST_MODE_SYNC (BROADCAST_MODE_BLOCK is deprecated and not supported in SDK v0.47+)
		broadcastResp, err := txClient.BroadcastTx(context.Background(), &txtypes.BroadcastTxRequest{
			Mode:    txtypes.BroadcastMode_BROADCAST_MODE_SYNC,
			TxBytes: txBytes,
		})
		if err != nil {
			grpcConn.Close()
			return fmt.Errorf("failed to broadcast transaction: %w", err)
		}

		if broadcastResp.TxResponse.Code != 0 {
			grpcConn.Close()
			return fmt.Errorf("transaction failed: %s", broadcastResp.TxResponse.RawLog)
		}

		txHash := broadcastResp.TxResponse.TxHash
		fmt.Printf("  Batch %d/%d: broadcasting %d accounts (tx hash: %s)\n",
			(i/cfg.BatchSize)+1, (len(needsFunding)+cfg.BatchSize-1)/cfg.BatchSize,
			len(batch), txHash)

		// Wait for transaction to be included in a block
		// Poll the transaction status until it's found or timeout
		maxWait := 30 * time.Second
		startTime := time.Now()
		txIncluded := false
		for time.Since(startTime) < maxWait {
			// Query transaction status via REST API
			txStatusURL := fmt.Sprintf("%s/cosmos/tx/v1beta1/txs/%s", restURL, txHash)
			txStatusResp, err := restClient.Get(txStatusURL)
			if err == nil && txStatusResp.StatusCode == http.StatusOK {
				var txStatusData struct {
					TxResponse struct {
						Height string `json:"height"`
						Code   int    `json:"code"`
						RawLog string `json:"raw_log"`
					} `json:"tx_response"`
				}
				if err := json.NewDecoder(txStatusResp.Body).Decode(&txStatusData); err == nil {
					txStatusResp.Body.Close()
					if txStatusData.TxResponse.Height != "" && txStatusData.TxResponse.Height != "0" {
						if txStatusData.TxResponse.Code != 0 {
							grpcConn.Close()
							return fmt.Errorf("transaction failed in block %s: code %d, log: %s",
								txStatusData.TxResponse.Height, txStatusData.TxResponse.Code, txStatusData.TxResponse.RawLog)
						}
						txIncluded = true
						totalBatches := (len(needsFunding) + cfg.BatchSize - 1) / cfg.BatchSize
						fmt.Printf("  Batch %d/%d: transaction included in block %s\n",
							(i/cfg.BatchSize)+1, totalBatches, txStatusData.TxResponse.Height)
						break
					}
				} else {
					txStatusResp.Body.Close()
				}
			} else if txStatusResp != nil && txStatusResp.StatusCode == http.StatusNotFound {
				// Transaction not found yet, continue polling
				txStatusResp.Body.Close()
			} else if txStatusResp != nil {
				// Some other error
				body, _ := io.ReadAll(txStatusResp.Body)
				txStatusResp.Body.Close()
				fmt.Printf("  Warning: error querying tx status: HTTP %d: %s\n", txStatusResp.StatusCode, string(body))
			}
			if txStatusResp != nil && txStatusResp.StatusCode != http.StatusNotFound {
				txStatusResp.Body.Close()
			}
			time.Sleep(500 * time.Millisecond)
		}
		grpcConn.Close()

		if !txIncluded {
			return fmt.Errorf("transaction %s was not included in a block within %v (transaction may have failed or been rejected)", txHash, maxWait)
		}

		currentSeq++
	}

	// Verify all accounts are funded (use REST API)
	fmt.Println("Verifying account balances...")
	allFunded := true
	for i, addr := range needsFunding {
		balanceURL := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", restURL, addr.String())
		balanceResp, err := restClient.Get(balanceURL)
		if err != nil || balanceResp.StatusCode != http.StatusOK {
			if balanceResp != nil {
				balanceResp.Body.Close()
			}
			fmt.Printf("  Warning: failed to query balance for %s: %v\n", addr.String(), err)
			allFunded = false
			continue
		}

		var balanceData struct {
			Balances []struct {
				Denom  string `json:"denom"`
				Amount string `json:"amount"`
			} `json:"balances"`
		}
		if err := json.NewDecoder(balanceResp.Body).Decode(&balanceData); err != nil {
			balanceResp.Body.Close()
			fmt.Printf("  Warning: failed to decode balance for %s: %v\n", addr.String(), err)
			allFunded = false
			continue
		}
		balanceResp.Body.Close()

		balance := sdk.NewCoins()
		for _, bal := range balanceData.Balances {
			amount, ok := math.NewIntFromString(bal.Amount)
			if ok {
				balance = balance.Add(sdk.NewCoin(bal.Denom, amount))
			}
		}
		if balance.AmountOf(cfg.Denom).LT(fundCoin.Amount) {
			fmt.Printf("  Warning: account %s (worker %d) has insufficient balance: %s\n",
				addr.String(), i, balance.AmountOf(cfg.Denom))
			allFunded = false
		}
	}

	if !allFunded {
		return fmt.Errorf("some accounts were not properly funded")
	}

	return nil
}
