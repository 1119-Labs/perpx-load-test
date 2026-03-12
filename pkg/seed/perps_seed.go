package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cosmossdk.io/math"
	"github.com/1119-Labs/perpx-chain/protocol/app"
	assettypes "github.com/1119-Labs/perpx-chain/protocol/x/assets/types"
	sendingtypes "github.com/1119-Labs/perpx-chain/protocol/x/sending/types"
	satypes "github.com/1119-Labs/perpx-chain/protocol/x/subaccounts/types"
	"github.com/cosmos/cosmos-sdk/client/tx"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
)

// seedPerpsAccounts deposits USDC margin to each account's subaccount 0.
// This uses the same deterministic key derivation as PerpxPerpsClient, ensuring
// that worker IDs align with seeded accounts.
func seedPerpsAccounts(
	cfg Config,
	encCfg app.EncodingConfig,
	seedPrivKey cryptotypes.PrivKey,
	seedAddr sdk.AccAddress,
	accountNum uint64,
	startSeq uint64,
	benchKeys []struct {
		privKey cryptotypes.PrivKey
		addr    sdk.AccAddress
	},
	restURL string,
) (*PerpsSeedSummary, error) {
	// Parse deposit amount (USDC quantums)
	depositQuantums, err := strconv.ParseUint(cfg.PerpsDeposit, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid perps deposit amount %q: %w", cfg.PerpsDeposit, err)
	}
	if depositQuantums == 0 {
		return nil, fmt.Errorf("perps deposit amount must be > 0")
	}

	fmt.Printf("\nDepositing margin to %d subaccounts...\n", len(benchKeys))
	fmt.Printf("  Deposit per subaccount: %s USDC quantums\n", cfg.PerpsDeposit)

	// Check seed balance for USDC
	restClient := &http.Client{Timeout: 10 * time.Second}
	balanceURL := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", restURL, seedAddr.String())
	balanceResp, err := restClient.Get(balanceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to query seed balance for perps deposits: %w", err)
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

	// Find USDC balance.
	// Prefer cfg.PerpsDepositDenom, but also fall back to the dev IBC USDC denom used by localnet-dev
	// so this works out of the box against the tracing stack.
	candidateUsdcDenoms := []string{cfg.PerpsDepositDenom}
	const localnetUsdcDenom = "ibc/8E27BA2D5493AF5636760E354E46004562C46AB7EC0CC4C1CA14E9E20E2545B5"
	if !strings.EqualFold(cfg.PerpsDepositDenom, localnetUsdcDenom) {
		candidateUsdcDenoms = append(candidateUsdcDenoms, localnetUsdcDenom)
	}

	seedUSDCBalance := math.ZeroInt()
	for _, bal := range balanceData.Balances {
		for _, denom := range candidateUsdcDenoms {
			if strings.EqualFold(bal.Denom, denom) {
				amount, ok := math.NewIntFromString(bal.Amount)
				if ok {
					seedUSDCBalance = amount
					break
				}
			}
		}
		if !seedUSDCBalance.IsZero() {
			break
		}
	}

	totalNeeded := math.NewInt(int64(depositQuantums)).Mul(math.NewInt(int64(len(benchKeys))))
	if seedUSDCBalance.LT(totalNeeded) {
		// If seed doesn't have USDC, we'll try to deposit anyway and let the chain handle it
		// (the seed account might need to be funded with USDC first, or the chain might auto-convert)
		fmt.Printf("  Warning: seed has %s USDC, needs %s for deposits\n", seedUSDCBalance, totalNeeded)
		fmt.Printf("  Attempting deposits anyway (chain may handle conversion or require USDC funding)\n")
	}

	// Deposit margin to each account's subaccount 0 in batches
	currentSeq := startSeq

	// gRPC client reused across batches.
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
		return nil, fmt.Errorf("failed to connect to gRPC: %w", err)
	}
	defer grpcConn.Close()

	txClient := txtypes.NewServiceClient(grpcConn)

	type pendingBatch struct {
		batchIdx int
		batches  int
		size     int
		txHash   string
	}
	pending := make([]pendingBatch, 0, (len(benchKeys)+cfg.BatchSize-1)/cfg.BatchSize)

	for i := 0; i < len(benchKeys); i += cfg.BatchSize {
		end := i + cfg.BatchSize
		if end > len(benchKeys) {
			end = len(benchKeys)
		}
		batch := benchKeys[i:end]

		// Build deposit messages
		msgs := make([]sdk.Msg, 0, len(batch))
		for _, bk := range batch {
			subaccountID := satypes.SubaccountId{
				Owner:  bk.addr.String(),
				Number: defaultSubaccountNum, // Perps clients use subaccount 0
			}

			// Use the account's own address as sender (they need USDC balance)
			// If they don't have USDC, this will fail, but that's expected if seed doesn't have USDC
			depositMsg := sendingtypes.NewMsgDepositToSubaccount(
				bk.addr.String(), // sender (the account itself)
				subaccountID,
				assettypes.AssetUsdc.Id,
				depositQuantums,
			)
			msgs = append(msgs, depositMsg)
		}

		// For each account, we need to sign with that account's key, not the seed key
		// So we'll send individual transactions or use a different approach
		// Actually, we can use the seed account to send USDC to each account first,
		// then have each account deposit to its own subaccount. But that's complex.
		// Let's try a simpler approach: use the seed account to directly deposit to each subaccount
		// by sending USDC from seed to each account, then having them deposit.

		// Actually, the simplest approach: send USDC from seed to each account's bank balance,
		// then have each account deposit to its own subaccount in separate transactions.
		// But that requires many transactions. Let's batch it differently:

		// We'll send USDC from seed to each account first, then batch the deposits
		// But for now, let's use a simpler approach: send USDC from seed directly to subaccounts
		// via the sending module's deposit message, using seed as sender.

		// Rebuild messages with seed as sender (seed deposits to each subaccount)
		msgs = make([]sdk.Msg, 0, len(batch))
		for _, bk := range batch {
			subaccountID := satypes.SubaccountId{
				Owner:  bk.addr.String(),
				Number: defaultSubaccountNum,
			}

			depositMsg := sendingtypes.NewMsgDepositToSubaccount(
				seedAddr.String(), // seed account deposits to each subaccount
				subaccountID,
				assettypes.AssetUsdc.Id,
				depositQuantums,
			)
			msgs = append(msgs, depositMsg)
		}

		// Create and sign transaction with seed key
		txBuilder := encCfg.TxConfig.NewTxBuilder()
		if err := txBuilder.SetMsgs(msgs...); err != nil {
			return nil, fmt.Errorf("failed to set deposit messages: %w", err)
		}

		// Set fees (use same gas/fee config as bank funding, including denom‑aware
		// minimum gas price for dev USDC IBC). Perps margin deposits are more
		// expensive than simple bank sends, so give them generous headroom.
		gasLimit := 300000 * uint64(len(batch))
		minGasPrice := math.NewInt(25000000000)
		if cfg.Denom == "ibc/8E27BA2D5493AF5636760E354E46004562C46AB7EC0CC4C1CA14E9E20E2545B5" {
			minGasPrice = math.NewInt(25000)
		}
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

		// Second round: sign
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
			return nil, fmt.Errorf("failed to sign deposit transaction: %w", err)
		}

		if err := txBuilder.SetSignatures(sigV2); err != nil {
			return nil, fmt.Errorf("failed to set signature: %w", err)
		}

		// Encode and broadcast
		txBytes, err := encCfg.TxConfig.TxEncoder()(txBuilder.GetTx())
		if err != nil {
			return nil, fmt.Errorf("failed to encode transaction: %w", err)
		}

		// Broadcast via gRPC (sync). We intentionally do NOT wait for inclusion per-batch
		// here; we pipeline broadcasts (sequence-safe) and then wait in parallel.
		broadcastResp, err := txClient.BroadcastTx(context.Background(), &txtypes.BroadcastTxRequest{
			Mode:    txtypes.BroadcastMode_BROADCAST_MODE_SYNC,
			TxBytes: txBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to broadcast deposit transaction: %w", err)
		}

		if broadcastResp.TxResponse.Code != 0 {
			txr := broadcastResp.TxResponse
			// sdk code=19 is "tx already in mempool" (ErrTxInMempoolCache). This can
			// happen when a previous attempt broadcast the same tx and it's still cached.
			// Treat it as non-fatal and proceed to wait for inclusion when txhash is present.
			if txr.Codespace == "sdk" && txr.Code == 19 && txr.TxHash != "" {
				fmt.Printf("  Warning: deposit tx already in mempool (tx hash: %s); waiting for inclusion\n", txr.TxHash)
			} else {
				return nil, fmt.Errorf(
					"deposit transaction failed (check_tx): code=%d codespace=%q txhash=%s raw_log=%q info=%q",
					txr.Code, txr.Codespace, txr.TxHash, txr.RawLog, txr.Info,
				)
			}
		}

		txHash := broadcastResp.TxResponse.TxHash
		totalBatches := (len(benchKeys) + cfg.BatchSize - 1) / cfg.BatchSize
		batchIdx := (i / cfg.BatchSize) + 1
		fmt.Printf("  Batch %d/%d: deposited margin to %d subaccounts (tx hash: %s)\n",
			batchIdx, totalBatches, len(batch), txHash)
		pending = append(pending, pendingBatch{
			batchIdx: batchIdx,
			batches:  totalBatches,
			size:     len(batch),
			txHash:   txHash,
		})

		currentSeq++
	}

	// Wait for inclusion in parallel. This is the slow part (block production),
	// and parallel waits drastically reduce overall funding time.
	//
	// Note: broadcasts above remain sequential to keep seed account sequences valid.
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
				return fmt.Errorf("deposit batch %d/%d failed in block %d: code %d, log: %s",
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

	// Build perps summary.
	totalBatches := (len(benchKeys) + cfg.BatchSize - 1) / cfg.BatchSize
	return &PerpsSeedSummary{
		SubaccountsSeeded: len(benchKeys),
		Batches:           totalBatches,
	}, nil
}
