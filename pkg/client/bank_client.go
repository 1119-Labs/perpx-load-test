package client

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"cosmossdk.io/math"
	"github.com/1119-Labs/perpx-chain/protocol/app"
	"github.com/1119-Labs/perpx-chain/protocol/loadtest/pkg/loadtest"
	"github.com/1119-Labs/perpx-chain/protocol/loadtest/pkg/strategies"
)

// PerpxBankClient implements loadtest.Client for PerpX bank send transactions
type PerpxBankClient struct {
	config   loadtest.Config
	strategy *strategies.BankSendStrategy

	// Account information
	privKey    cryptotypes.PrivKey
	addr       sdk.AccAddress
	accountNum uint64
	sequence   uint64 // Local sequence counter (atomic)

	// Encoding config
	encCfg app.EncodingConfig

	// Lazy initialization: query account info on first use
	accountQueried  bool
	accountQueryMtx sync.Mutex
	restURL         string // Cached REST API URL
}

// Ensure PerpxBankClient implements Client
var _ loadtest.Client = (*PerpxBankClient)(nil)

// NewPerpxBankClient creates a new PerpX bank client.
// The id is a per-worker identifier used to derive a unique account key.
func NewPerpxBankClient(cfg loadtest.Config, strategy *strategies.BankSendStrategy, seedKey string, id int) (*PerpxBankClient, error) {
	encCfg := app.GetEncodingConfig()

	// Use the provided worker id so each worker gets a distinct account.
	workerID := id

	// Generate deterministic key for this worker (similar to regen_genesis_addresses.go)
	seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", workerID)
	seed := sha256.Sum256([]byte(seedStr))
	// Use worker ID as path for additional determinism
	adjustedSeed := sha256.Sum256(append(seed[:], byte(workerID)))
	privKeyBytes, _ := btcec.PrivKeyFromBytes(adjustedSeed[:])
	privKey := &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
	addr := sdk.AccAddress(privKey.PubKey().Address())

	// Connect to gRPC endpoint (use first endpoint, convert ws:// to http://)
	rpcEndpoint := cfg.Endpoints[0]
	if len(rpcEndpoint) > 0 {
		// Convert ws://localhost:36657/websocket to http://localhost:36657
		rpcEndpoint = convertWebSocketToHTTP(rpcEndpoint)
		// Ensure we remove any trailing /websocket path that might remain
		rpcEndpoint = strings.TrimSuffix(rpcEndpoint, "/websocket")
		// Replace 127.0.0.1 with localhost to match seed.go behavior
		rpcEndpoint = strings.Replace(rpcEndpoint, "127.0.0.1", "localhost", -1)
	} else {
		rpcEndpoint = "http://localhost:36657"
	}

	// Convert RPC port to gRPC port (36657 -> 39090, 26657 -> 9090)
	grpcAddr := rpcEndpoint
	if len(grpcAddr) > 7 && grpcAddr[:7] == "http://" {
		grpcAddr = grpcAddr[7:]
	}
	// Replace RPC port with gRPC port
	if strings.Contains(grpcAddr, ":36657") {
		grpcAddr = strings.Replace(grpcAddr, ":36657", ":39090", 1)
	} else if strings.Contains(grpcAddr, ":26657") {
		grpcAddr = strings.Replace(grpcAddr, ":26657", ":9090", 1)
	} else if !strings.Contains(grpcAddr, ":") {
		// Default to gRPC port if no port specified
		grpcAddr = "localhost:39090"
	}

	// Use REST API for account queries (more reliable than gRPC, avoids frame size issues)
	// Convert RPC URL to REST API URL (same logic as seed.go)
	restURL := strings.Replace(rpcEndpoint, ":36657", ":31317", 1)
	if !strings.Contains(restURL, ":31317") {
		// If port wasn't 36657, try to infer REST port or use default
		restURL = strings.Replace(rpcEndpoint, ":26657", ":1317", 1)
		if !strings.Contains(restURL, ":1317") {
			// Default to localhost:31317 if we can't determine
			restURL = "http://localhost:31317"
		}
	}

	// Initialize client without querying account (lazy initialization)
	// This avoids blocking during initialization, which happens before WebSocket connection
	client := &PerpxBankClient{
		config:         cfg,
		strategy:       strategy,
		privKey:        privKey,
		addr:           addr,
		accountNum:     0, // Will be queried lazily
		sequence:       0, // Will be queried lazily
		encCfg:         encCfg,
		accountQueried: false,
		restURL:        restURL,
	}

	return client, nil
}

// ensureAccountQueried queries account info if not already queried (lazy initialization)
func (c *PerpxBankClient) ensureAccountQueried() error {
	c.accountQueryMtx.Lock()
	defer c.accountQueryMtx.Unlock()

	if c.accountQueried {
		return nil
	}

	// Query account info via REST API (same approach as seed.go)
	accountURL := fmt.Sprintf("%s/cosmos/auth/v1beta1/accounts/%s", c.restURL, c.addr.String())

	var accountResp struct {
		Account struct {
			Type    string `json:"@type"`
			Address string `json:"address"`
			PubKey  *struct {
				Type string `json:"@type"`
				Key  string `json:"key"`
			} `json:"pub_key"`
			AccountNumber string `json:"account_number"`
			Sequence      string `json:"sequence"`
		} `json:"account"`
	}

	// Use a simple HTTP client with timeout (same approach as seed.go)
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(accountURL)
	if err != nil {
		return fmt.Errorf("failed to query account via REST API at %s (account %s may not exist - run 'seed' command first): %w", accountURL, c.addr.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to query account: HTTP %d: %s (account %s may not exist - run 'seed' command first)", resp.StatusCode, string(body), c.addr.String())
	}

	if err := json.NewDecoder(resp.Body).Decode(&accountResp); err != nil {
		return fmt.Errorf("failed to decode account response: %w", err)
	}

	// Parse account number and sequence
	accountNum, err := strconv.ParseUint(accountResp.Account.AccountNumber, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse account number: %w", err)
	}
	sequence, err := strconv.ParseUint(accountResp.Account.Sequence, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse sequence: %w", err)
	}

	c.accountNum = accountNum
	c.sequence = sequence
	c.accountQueried = true

	return nil
}

// GenerateTx generates a bank send transaction
func (c *PerpxBankClient) GenerateTx() ([]byte, error) {
	// Ensure account info is queried (lazy initialization)
	if err := c.ensureAccountQueried(); err != nil {
		return nil, err
	}

	// Get current sequence and increment atomically
	seq := atomic.AddUint64(&c.sequence, 1) - 1

	// Build transaction using strategy
	txBuilder := c.encCfg.TxConfig.NewTxBuilder()

	// Create bank send message
	msg, err := c.strategy.CreateMsg(c.addr.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	if err := txBuilder.SetMsgs(msg); err != nil {
		return nil, fmt.Errorf("failed to set message: %w", err)
	}

	// Set fees based on gas limit and minimum gas price
	// Minimum gas price: 25000000000aperpx per unit of gas (from cmd/perpxd/cmd/config.go)
	gasLimit := uint64(200000)
	minGasPrice := math.NewInt(25000000000) // 25 billion aperpx per unit of gas
	feeAmount := minGasPrice.Mul(math.NewInt(int64(gasLimit)))
	feeCoins := sdk.NewCoins(sdk.NewCoin(c.strategy.Denom(), feeAmount))
	txBuilder.SetFeeAmount(feeCoins)
	txBuilder.SetGasLimit(gasLimit)

	// First round: set empty signatures to gather signer infos (required for SIGN_MODE_DIRECT)
	sigV2Empty := signing.SignatureV2{
		PubKey: c.privKey.PubKey(),
		Data: &signing.SingleSignatureData{
			SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
			Signature: nil,
		},
		Sequence: seq,
	}
	if err := txBuilder.SetSignatures(sigV2Empty); err != nil {
		return nil, fmt.Errorf("failed to set empty signature: %w", err)
	}

	// Second round: actually sign the transaction
	signerData := authsigning.SignerData{
		Address:       c.addr.String(),
		ChainID:       c.strategy.ChainID(),
		AccountNumber: c.accountNum,
		Sequence:      seq,
		PubKey:        c.privKey.PubKey(),
	}

	sigV2, err := tx.SignWithPrivKey(
		context.Background(),
		signing.SignMode_SIGN_MODE_DIRECT,
		signerData,
		txBuilder,
		c.privKey,
		c.encCfg.TxConfig,
		seq,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	if err := txBuilder.SetSignatures(sigV2); err != nil {
		return nil, fmt.Errorf("failed to set signature: %w", err)
	}

	// Encode transaction
	txBytes, err := c.encCfg.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction: %w", err)
	}

	return txBytes, nil
}

// convertWebSocketToHTTP converts ws://host:port/path to http://host:port
func convertWebSocketToHTTP(wsURL string) string {
	if len(wsURL) > 5 && wsURL[:5] == "ws://" {
		// Remove /websocket suffix if present
		httpURL := "http://" + wsURL[5:]
		if len(httpURL) > 11 && httpURL[len(httpURL)-11:] == "/websocket" {
			httpURL = httpURL[:len(httpURL)-11]
		}
		return httpURL
	}
	if len(wsURL) > 6 && wsURL[:6] == "wss://" {
		httpURL := "https://" + wsURL[6:]
		if len(httpURL) > 11 && httpURL[len(httpURL)-11:] == "/websocket" {
			httpURL = httpURL[:len(httpURL)-11]
		}
		return httpURL
	}
	return wsURL
}
