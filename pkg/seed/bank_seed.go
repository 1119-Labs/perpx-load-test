package seed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cosmossdk.io/math"
	"github.com/1119-Labs/perpx-chain/protocol/app"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
)

func seedAccounts(cfg Config) (*SeedSummary, error) {
	// Parse fund amount
	fundCoin, err := sdk.ParseCoinNormalized(cfg.FundAmount)
	if err != nil {
		return nil, fmt.Errorf("invalid fund amount: %w", err)
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
			return nil, fmt.Errorf("failed to decode private key (must be hex-encoded): %w", err)
		}
		if len(keyBytes) != 32 {
			return nil, fmt.Errorf("invalid private key length: expected 32 bytes, got %d", len(keyBytes))
		}
		// Create secp256k1 private key from bytes
		privKeyBytes, _ := btcec.PrivKeyFromBytes(keyBytes)
		seedPrivKey = &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
		seedAddr = sdk.AccAddress(seedPrivKey.PubKey().Address())
	} else {
		// Treat SeedKey as either a full mnemonic (contains spaces) or fail fast.
		// In the future this can be extended to look up named keys from a keyring.
		if strings.Contains(cfg.SeedKey, " ") {
			// It's a mnemonic
			hdPath := hd.CreateHDPath(118, 0, 0).String()
			derivedPriv, err := hd.Secp256k1.Derive()(cfg.SeedKey, "", hdPath)
			if err != nil {
				return nil, fmt.Errorf("failed to derive key from mnemonic: %w", err)
			}
			seedPrivKey = hd.Secp256k1.Generate()(derivedPriv)
			seedAddr = sdk.AccAddress(seedPrivKey.PubKey().Address())
		} else {
			return nil, fmt.Errorf("seed-key %q is not a mnemonic; please provide a mnemonic, or use --seed-private-key", cfg.SeedKey)
		}
	}

	fmt.Printf("Seed address: %s\n", seedAddr.String())

	// Use REST API for balance/account queries. REST URL comes from config (Viper: loadtest.restUrl or seed.restUrl); fallback to RPC.
	restURL := strings.TrimSpace(cfg.RestURL)
	if restURL == "" {
		restURL = cfg.RPC
	}

	restClient := &http.Client{Timeout: 10 * time.Second}

	// Check seed balance via REST API
	balanceURL := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", restURL, seedAddr.String())
	balanceResp, err := restClient.Get(balanceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to query seed balance: %w", err)
	}
	defer balanceResp.Body.Close()

	if balanceResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(balanceResp.Body)
		return nil, fmt.Errorf("failed to query seed balance: HTTP %d: %s", balanceResp.StatusCode, string(body))
	}

	var balanceData struct {
		Balances []struct {
			Denom  string `json:"denom"`
			Amount string `json:"amount"`
		} `json:"balances"`
	}
	if err := json.NewDecoder(balanceResp.Body).Decode(&balanceData); err != nil {
		return nil, fmt.Errorf("failed to decode balance response: %w", err)
	}

	seedBalance := sdk.NewCoins()
	for _, bal := range balanceData.Balances {
		amount, ok := math.NewIntFromString(bal.Amount)
		if !ok {
			return nil, fmt.Errorf("invalid amount: %s", bal.Amount)
		}
		seedBalance = seedBalance.Add(sdk.NewCoin(bal.Denom, amount))
	}
	fmt.Printf("Seed balance: %s\n", seedBalance)

	// Check if seed has enough funds
	if seedBalance.AmountOf(cfg.Denom).LT(totalRequired.AmountOf(cfg.Denom)) {
		return nil, fmt.Errorf("insufficient funds: seed has %s, needs %s",
			seedBalance.AmountOf(cfg.Denom), totalRequired.AmountOf(cfg.Denom))
	}

	// Get seed account info (sequence, account number) via REST API
	accountURL := fmt.Sprintf("%s/cosmos/auth/v1beta1/accounts/%s", restURL, seedAddr.String())
	accountResp, err := restClient.Get(accountURL)
	if err != nil {
		return nil, fmt.Errorf("failed to query seed account: %w", err)
	}
	defer accountResp.Body.Close()

	if accountResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(accountResp.Body)
		return nil, fmt.Errorf("failed to query seed account: HTTP %d: %s", accountResp.StatusCode, string(body))
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
		return nil, fmt.Errorf("failed to decode account response: %w", err)
	}

	// Parse account number and sequence
	accountNum, err := strconv.ParseUint(accountData.Account.AccountNumber, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse account number: %w", err)
	}
	sequence, err := strconv.ParseUint(accountData.Account.Sequence, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sequence: %w", err)
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

	// Check which accounts need funding (use REST API to avoid gRPC frame limits).
	// Parallelize with bounded concurrency since each account query is independent.
	needsFunding := make([]sdk.AccAddress, 0, cfg.Workers)
	needsFundingMu := &sync.Mutex{}

	{
		const maxConcurrency = 16
		eg, egctx := errgroup.WithContext(context.Background())
		eg.SetLimit(maxConcurrency)

		for i := range benchKeys {
			i := i
			eg.Go(func() error {
				select {
				case <-egctx.Done():
					return egctx.Err()
				default:
				}

				addr := benchKeys[i].addr
				balanceURL := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", restURL, addr.String())
				balanceResp, err := restClient.Get(balanceURL)
				if err != nil || balanceResp.StatusCode != http.StatusOK {
					// Account might not exist, assume it needs funding
					if balanceResp != nil {
						balanceResp.Body.Close()
					}
					needsFundingMu.Lock()
					needsFunding = append(needsFunding, addr)
					needsFundingMu.Unlock()
					return nil
				}

				var balanceData struct {
					Balances []struct {
						Denom  string `json:"denom"`
						Amount string `json:"amount"`
					} `json:"balances"`
				}
				if err := json.NewDecoder(balanceResp.Body).Decode(&balanceData); err != nil {
					balanceResp.Body.Close()
					needsFundingMu.Lock()
					needsFunding = append(needsFunding, addr)
					needsFundingMu.Unlock()
					return nil
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
					needsFundingMu.Lock()
					needsFunding = append(needsFunding, addr)
					needsFundingMu.Unlock()
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, fmt.Errorf("failed while scanning accounts for funding: %w", err)
		}
	}

	if len(needsFunding) == 0 {
		fmt.Println("All accounts already funded!")
		if cfg.Mode != SeedModePerps {
			summary := &SeedSummary{
				ChainID: cfg.ChainID,
				RPC:     cfg.RPC,
				Bank: BankSeedSummary{
					Mode:                  cfg.Mode,
					WorkersRequested:      cfg.Workers,
					AccountsAlreadyFunded: cfg.Workers,
					AccountsFunded:        0,
					Batches:               0,
				},
			}
			return summary, nil
		}
		fmt.Println("Perps mode enabled: continuing to deposit margin to subaccounts...")
	}

	fmt.Printf("Funding %d accounts in batches of %d...\n", len(needsFunding), cfg.BatchSize)

	// Fund accounts in batches, keeping broadcasts sequence‑correct but
	// parallelizing the slow "wait for inclusion" phase across batches.
	currentSeq := sequence

	// Reuse a single gRPC client across all batches.
	grpcBase := cfg.GRPC
	if strings.TrimSpace(grpcBase) == "" {
		grpcBase = cfg.RPC
	}
	grpcAddr := deriveGRPCAddr(grpcBase)
	grpcConn, err := grpc.Dial(
		grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC for broadcasting: %w", err)
	}
	defer grpcConn.Close()
	txClient := txtypes.NewServiceClient(grpcConn)

	type pendingBatch struct {
		batchIdx int
		batches  int
		size     int
		txHash   string
	}
	pending := make([]pendingBatch, 0, (len(needsFunding)+cfg.BatchSize-1)/cfg.BatchSize)

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
			return nil, fmt.Errorf("failed to set messages: %w", err)
		}

		// Set fees
		gasLimit := 200000 * uint64(len(batch))
		minGasPrice := math.NewInt(25000000000)
		feeAmount := minGasPrice.Mul(math.NewInt(int64(gasLimit)))
		feeCoins := sdk.NewCoins(sdk.NewCoin(cfg.Denom, feeAmount))
		txBuilder.SetFeeAmount(feeCoins)
		txBuilder.SetGasLimit(gasLimit)

		// First round: set empty signatures
		sigV2Empty := signing.SignatureV2{
			PubKey: seedPrivKey.PubKey(),
			Data: &signing.SingleSignatureData{
				SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
				Signature: nil,
			},
			Sequence: currentSeq,
		}
		if err := txBuilder.SetSignatures(sigV2Empty); err != nil {
			return nil, fmt.Errorf("failed to set empty signature: %w", err)
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
			return nil, fmt.Errorf("failed to sign: %w", err)
		}

		if err := txBuilder.SetSignatures(sigV2); err != nil {
			return nil, fmt.Errorf("failed to set signature: %w", err)
		}

		// Encode transaction
		txBytes, err := encCfg.TxConfig.TxEncoder()(txBuilder.GetTx())
		if err != nil {
			return nil, fmt.Errorf("failed to encode transaction: %w", err)
		}

		// Broadcast transaction (using sync mode to ensure it's accepted).
		// Broadcasts remain sequential to keep the seed account sequence valid.
		broadcastResp, err := txClient.BroadcastTx(context.Background(), &txtypes.BroadcastTxRequest{
			Mode:    txtypes.BroadcastMode_BROADCAST_MODE_SYNC,
			TxBytes: txBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to broadcast transaction: %w", err)
		}

		if broadcastResp.TxResponse.Code != 0 {
			txr := broadcastResp.TxResponse
			// sdk code=19 is "tx already in mempool" (ErrTxInMempoolCache). This commonly
			// happens when a previous run broadcast the same tx and the node still has it
			// cached. In that case, keep going and wait for inclusion using the returned
			// tx hash (if present).
			if txr.Codespace == "sdk" && txr.Code == 19 && txr.TxHash != "" {
				fmt.Printf("  Warning: tx already in mempool (tx hash: %s); waiting for inclusion\n", txr.TxHash)
			} else {
				return nil, fmt.Errorf(
					"transaction failed (check_tx): code=%d codespace=%q txhash=%s raw_log=%q info=%q",
					txr.Code, txr.Codespace, txr.TxHash, txr.RawLog, txr.Info,
				)
			}
		}

		txHash := broadcastResp.TxResponse.TxHash
		totalBatches := (len(needsFunding) + cfg.BatchSize - 1) / cfg.BatchSize
		batchIdx := (i / cfg.BatchSize) + 1
		fmt.Printf("  Batch %d/%d: broadcasting %d accounts (tx hash: %s)\n",
			batchIdx, totalBatches, len(batch), txHash)

		pending = append(pending, pendingBatch{
			batchIdx: batchIdx,
			batches:  totalBatches,
			size:     len(batch),
			txHash:   txHash,
		})

		currentSeq++
	}

	// Wait for all batch txs to be included, in parallel, similar to perps
	// margin deposits.
	if len(pending) > 0 {
		const maxWait = 2 * time.Minute
		waitConcurrency := 8
		if len(pending) < waitConcurrency {
			waitConcurrency = len(pending)
		}
		eg, egctx := errgroup.WithContext(context.Background())
		eg.SetLimit(waitConcurrency)
		for _, p := range pending {
			p := p
			eg.Go(func() error {
				txStatusResp, err := waitForTxInclusionGRPC(egctx, txClient, p.txHash, maxWait)
				if err != nil {
					return err
				}
				if txStatusResp.TxResponse != nil && txStatusResp.TxResponse.Code != 0 {
					return fmt.Errorf("funding batch %d/%d failed in block %d: code %d, log: %s",
						p.batchIdx, p.batches, txStatusResp.TxResponse.Height, txStatusResp.TxResponse.Code, txStatusResp.TxResponse.RawLog)
				}
				fmt.Printf("  Batch %d/%d: transaction included in block %d\n",
					p.batchIdx, p.batches, txStatusResp.TxResponse.Height)
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
	}

	// Verify all accounts are funded (use REST API). This is also IO-bound and
	// can be parallelized safely with bounded concurrency.
	fmt.Println("Verifying account balances...")
	allFunded := true
	{
		const maxConcurrency = 16
		eg, egctx := errgroup.WithContext(context.Background())
		eg.SetLimit(maxConcurrency)

		allFundedMu := &sync.Mutex{}

		for i := range needsFunding {
			i := i
			eg.Go(func() error {
				select {
				case <-egctx.Done():
					return egctx.Err()
				default:
				}

				addr := needsFunding[i]
				balanceURL := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", restURL, addr.String())
				balanceResp, err := restClient.Get(balanceURL)
				if err != nil || balanceResp.StatusCode != http.StatusOK {
					if balanceResp != nil {
						balanceResp.Body.Close()
					}
					fmt.Printf("  Warning: failed to query balance for %s: %v\n", addr.String(), err)
					allFundedMu.Lock()
					allFunded = false
					allFundedMu.Unlock()
					return nil
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
					allFundedMu.Lock()
					allFunded = false
					allFundedMu.Unlock()
					return nil
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
					allFundedMu.Lock()
					allFunded = false
					allFundedMu.Unlock()
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, fmt.Errorf("failed while verifying funded accounts: %w", err)
		}
	}

	if !allFunded {
		return nil, fmt.Errorf("some accounts were not properly funded")
	}

	// If perps mode is enabled, deposit margin to subaccounts
	if cfg.Mode == SeedModePerps {
		// Re-query the actual seed account sequence from the chain before starting
		// perps deposits. Local sequence tracking can drift due to broadcast
		// timing, so we use the on-chain value as the source of truth.
		accountURL := fmt.Sprintf("%s/cosmos/auth/v1beta1/accounts/%s", restURL, seedAddr.String())
		accountResp2, err := restClient.Get(accountURL)
		if err != nil {
			return nil, fmt.Errorf("failed to query seed account before perps deposits: %w", err)
		}
		defer accountResp2.Body.Close()

		if accountResp2.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(accountResp2.Body)
			return nil, fmt.Errorf("failed to query seed account before perps deposits: HTTP %d: %s", accountResp2.StatusCode, string(body))
		}

		var accountData2 struct {
			Account struct {
				Type          string `json:"@type"`
				Address       string `json:"address"`
				AccountNumber string `json:"account_number"`
				Sequence      string `json:"sequence"`
			} `json:"account"`
		}
		if err := json.NewDecoder(accountResp2.Body).Decode(&accountData2); err != nil {
			return nil, fmt.Errorf("failed to decode seed account before perps deposits: %w", err)
		}

		actualSeq, err := strconv.ParseUint(accountData2.Account.Sequence, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse actual seed sequence before perps deposits: %w", err)
		}

		if actualSeq != currentSeq {
			fmt.Printf("Note: seed account sequence on-chain (%d) differs from local tracking (%d), using on-chain value\n",
				actualSeq, currentSeq)
			currentSeq = actualSeq
		}

		perpsSummary, err := seedPerpsAccounts(cfg, encCfg, seedPrivKey, seedAddr, accountNum, currentSeq, benchKeys, restURL)
		if err != nil {
			return nil, fmt.Errorf("failed to seed perps accounts: %w", err)
		}

		// Build combined summary for bank + perps modes.
		totalBatches := (len(needsFunding) + cfg.BatchSize - 1) / cfg.BatchSize
		summary := &SeedSummary{
			ChainID: cfg.ChainID,
			RPC:     cfg.RPC,
			Bank: BankSeedSummary{
				Mode:                  cfg.Mode,
				WorkersRequested:      cfg.Workers,
				AccountsAlreadyFunded: cfg.Workers - len(needsFunding),
				AccountsFunded:        len(needsFunding),
				Batches:               totalBatches,
			},
			Perps: perpsSummary,
		}
		return summary, nil
	}

	// Bank-only mode summary.
	totalBatches := (len(needsFunding) + cfg.BatchSize - 1) / cfg.BatchSize
	summary := &SeedSummary{
		ChainID: cfg.ChainID,
		RPC:     cfg.RPC,
		Bank: BankSeedSummary{
			Mode:                  cfg.Mode,
			WorkersRequested:      cfg.Workers,
			AccountsAlreadyFunded: cfg.Workers - len(needsFunding),
			AccountsFunded:        len(needsFunding),
			Batches:               totalBatches,
		},
	}

	return summary, nil
}

// deriveGRPCAddr normalizes a gRPC base URL or host:port string into the
// host:port form expected by grpc.Dial. It strips http/https schemes and any
// path suffixes.
func deriveGRPCAddr(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return "localhost"
	}
	addr := strings.TrimPrefix(base, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	if idx := strings.Index(addr, "/"); idx != -1 {
		addr = addr[:idx]
	}
	return addr
}
