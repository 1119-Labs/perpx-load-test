package client

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
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
	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// sharedHTTPClient is reused for all REST account queries to avoid per-call
// allocations and enable connection pooling across requests.
var sharedHTTPClient = &http.Client{Timeout: 10 * time.Second}

// PerpxBankClient implements loadtest.Client for PerpX bank send transactions
type PerpxBankClient struct {
	config   loadtest.Config
	strategy *strategies.BankSendStrategy

	// Account information for this client's primary worker-bound account.
	privKey    cryptotypes.PrivKey
	addr       sdk.AccAddress
	accountNum uint64
	sequence   uint64 // Local sequence counter (atomic)

	// Worker/connection mapping metadata.
	// workerIndex is the global worker index (0-based) for this client. It
	// determines the bench key/address for the primary account.
	workerIndex int
	// connectionIndex is the 0-based index of the connection this client
	// belongs to within its endpoint's connection set.
	connectionIndex int
	// workersPerConnection is the number of logical workers assigned to each
	// connection across the entire worker set.
	workersPerConnection int
	// perConnectionRate is the configured tx/s rate for this connection.
	perConnectionRate int
	// txCounter is a monotonically increasing counter used to derive the
	// (second, j) indices for the worker/connection mapping.
	txCounter uint64

	// lastSenderIdx/lastSender track which sender account produced the most
	// recently generated sequenced tx. This allows CommitSequence / sequence
	// recovery to apply to the correct underlying sender.
	lastSenderMtx sync.Mutex
	lastSenderIdx int
	lastBench     *benchSender // non-nil when lastSenderIdx != workerIndex

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
// connIndex and workersPerConn define the worker/connection mapping.
func NewPerpxBankClient(cfg loadtest.Config, strategy *strategies.BankSendStrategy, id int, connIndex int, workersPerConn int) (*PerpxBankClient, error) {
	encCfg := app.GetEncodingConfig()

	// Use the provided worker id so each worker gets a distinct account.
	workerID := id

	// Generate deterministic key and address for this worker
	privKey, addr := GenerateDeterministicKeyAndAddress(workerID)

	// REST base URL comes from config only (Viper: YAML, env, CLI). Fallback: derive from first WebSocket endpoint.
	restURL := strings.TrimSpace(cfg.RESTURL)
	if restURL == "" && len(cfg.Endpoints) > 0 && cfg.Endpoints[0] != "" {
		restURL = convertWebSocketToHTTP(cfg.Endpoints[0])
	}

	// Initialize client without querying account (lazy initialization)
	// This avoids blocking during initialization, which happens before WebSocket connection
	client := &PerpxBankClient{
		config:               cfg,
		strategy:             strategy,
		privKey:              privKey,
		addr:                 addr,
		accountNum:           0, // Will be queried lazily
		sequence:             0, // Will be queried lazily
		encCfg:               encCfg,
		accountQueried:       false,
		restURL:              restURL,
		workerIndex:          workerID,
		connectionIndex:      connIndex,
		workersPerConnection: workersPerConn,
		perConnectionRate:    cfg.Rate,
		txCounter:            0,
	}

	return client, nil
}

// ensureAccountQueried queries account info if not already queried (lazy initialization).
func (c *PerpxBankClient) ensureAccountQueried() error {
	c.accountQueryMtx.Lock()
	defer c.accountQueryMtx.Unlock()

	if c.accountQueried {
		return nil
	}

	info, err := QueryAccountInfo(c.restURL, c.addr.String(), sharedHTTPClient)
	if err != nil {
		return err
	}

	c.accountNum = info.AccountNumber
	atomic.StoreUint64(&c.sequence, info.Sequence)
	c.accountQueried = true

	return nil
}

// benchSender holds cached metadata for a bench-derived sender account used
// by the worker/connection mapping so we don't have to re-derive keys or
// re-query account info for every transaction.
type benchSender struct {
	// seq is the local sequence counter for this sender. It is initialized from
	// the on-chain sequence and then incremented atomically per generated tx.
	seq uint64

	priv        cryptotypes.PrivKey
	addr        sdk.AccAddress
	accountNum  uint64
	initialized bool
}

var (
	benchSendersMu sync.RWMutex
	benchSenders   = make(map[int]*benchSender)
)

// benchAddrs caches derived bench addresses for receiver indices so we don't
// have to redo the relatively expensive hashing/EC key derivation on every
// transaction when selecting a receiver.
var (
	benchAddrsMu sync.RWMutex
	benchAddrs   = make(map[int]string)
)

// connectionRange returns the [start, end) global worker index range owned by
// this client's connection.
func (c *PerpxBankClient) connectionRange() (start, end int) {
	if c.workersPerConnection <= 0 {
		return 0, 0
	}
	start = c.connectionIndex * c.workersPerConnection
	end = start + c.workersPerConnection
	return start, end
}

// senderReceiverIndices computes the global sender and receiver worker indices
// for logical second t and per-second tx index j
func (c *PerpxBankClient) senderReceiverIndices(t uint64, j int) (senderIdx, receiverIdx int) {
	_, end := c.connectionRange()
	if c.workersPerConnection <= 0 || end == 0 || c.perConnectionRate <= 0 {
		// Fallback to this client's own worker index for both sender and receiver
		// if mapping parameters are not initialized.
		return c.workerIndex, c.workerIndex
	}

	wConn := c.workersPerConnection
	connStart := c.connectionIndex * wConn

	base := int((2 * uint64(c.perConnectionRate) * t) % uint64(wConn))
	sLocal := (base + j) % wConn
	rLocal := (base + c.perConnectionRate + j) % wConn

	return connStart + sLocal, connStart + rLocal
}

// deriveBenchKey derives a deterministic bench private key and address for the
// given integer index. This matches the scheme used by seed.go and
// DeriveBenchAddress so that indices line up with pre-funded accounts.
func deriveBenchKey(index int) (cryptotypes.PrivKey, sdk.AccAddress) {
	seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", index)
	seed := sha256.Sum256([]byte(seedStr))
	adjustedSeed := sha256.Sum256(append(seed[:], byte(index)))
	privKeyBytes, _ := btcec.PrivKeyFromBytes(adjustedSeed[:])
	privKey := &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
	addr := sdk.AccAddress(privKey.PubKey().Address())
	return privKey, addr
}

// getBenchAddress returns a cached bench address for the given index,
// deriving it once via DeriveBenchAddress on first use.
func getBenchAddress(index int) string {
	benchAddrsMu.RLock()
	addr, ok := benchAddrs[index]
	benchAddrsMu.RUnlock()
	if ok {
		return addr
	}

	benchAddrsMu.Lock()
	defer benchAddrsMu.Unlock()

	// Another goroutine may have populated the cache while we were waiting.
	if addr, ok = benchAddrs[index]; ok {
		return addr
	}

	addr = DeriveBenchAddress(index)
	benchAddrs[index] = addr
	return addr
}

// getOrInitBenchSender returns a cached benchSender for the given index,
// initializing it (including on-chain account lookup) on first use.
func getOrInitBenchSender(index int, restURL string) (*benchSender, error) {
	benchSendersMu.RLock()
	s, ok := benchSenders[index]
	initialized := false
	if ok {
		initialized = s.initialized
	}
	benchSendersMu.RUnlock()

	if !ok {
		benchSendersMu.Lock()
		s, ok = benchSenders[index]
		if !ok {
			priv, addr := deriveBenchKey(index)
			s = &benchSender{
				priv: priv,
				addr: addr,
			}
			benchSenders[index] = s
		}
		initialized = s.initialized
		benchSendersMu.Unlock()
	}

	if initialized {
		return s, nil
	}

	// First-time initialization: query on-chain account metadata.
	info, err := QueryAccountInfo(restURL, s.addr.String(), sharedHTTPClient)
	if err != nil {
		return nil, err
	}

	benchSendersMu.Lock()
	// Another goroutine may have initialized this sender while we were querying.
	if !s.initialized {
		s.accountNum = info.AccountNumber
		s.seq = info.Sequence
		s.initialized = true
	}
	benchSendersMu.Unlock()

	return s, nil
}

// GenerateTx generates a bank send transaction using the worker/connection
func (c *PerpxBankClient) GenerateTx() ([]byte, error) {
	// For sync/commit broadcast modes, the transactor will prefer the sequenced
	// GenerateTxWithSequence path below when available. Keep GenerateTx for
	// async mode and backwards compatibility.
	// Ensure account info is queried (lazy initialization) for this client's
	// primary worker-bound account.
	if err := c.ensureAccountQueried(); err != nil {
		return nil, err
	}

	// Compute logical (second, j) indices for this connection using a
	// monotonically increasing per-connection counter.
	counter := atomic.AddUint64(&c.txCounter, 1) - 1
	rate := c.perConnectionRate
	if rate <= 0 {
		rate = c.config.Rate
	}
	t := counter / uint64(rate)
	j := int(counter % uint64(rate))

	senderIdx, receiverIdx := c.senderReceiverIndices(t, j)

	if c.config.Debug.LogWorkerIDs {
		fmt.Printf("bank tx sender=%d receiver=%d (conn=%d)\n", senderIdx, receiverIdx, c.connectionIndex)
	}

	var (
		senderPriv cryptotypes.PrivKey
		senderAddr sdk.AccAddress
		seq        uint64
		accountNum uint64
	)

	// If the sender index matches this client's worker index, use the local
	// account metadata; otherwise, use a bench-derived sender.
	if senderIdx == c.workerIndex {
		senderPriv = c.privKey
		senderAddr = c.addr
		accountNum = c.accountNum
		seq = atomic.AddUint64(&c.sequence, 1) - 1
	} else {
		sender, err := getOrInitBenchSender(senderIdx, c.restURL)
		if err != nil {
			return nil, err
		}
		senderPriv = sender.priv
		senderAddr = sender.addr
		accountNum = sender.accountNum
		seq = atomic.AddUint64(&sender.seq, 1) - 1
	}

	toAddr := getBenchAddress(receiverIdx)

	// Build transaction using strategy, explicitly specifying sender and
	// receiver addresses.
	txBuilder := c.encCfg.TxConfig.NewTxBuilder()

	msg, err := c.strategy.CreateMsgTo(senderAddr.String(), toAddr)
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
		PubKey: senderPriv.PubKey(),
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
		Address:       senderAddr.String(),
		ChainID:       c.strategy.ChainID(),
		AccountNumber: accountNum,
		Sequence:      seq,
		PubKey:        senderPriv.PubKey(),
	}

	sigV2, err := tx.SignWithPrivKey(
		context.Background(),
		signing.SignMode_SIGN_MODE_DIRECT,
		signerData,
		txBuilder,
		senderPriv,
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

// GenerateTxWithSequence generates a bank send transaction using the current
// sender/receiver schedule, but WITHOUT advancing the sender's sequence. This
// allows the transactor (broadcast_tx_sync) to advance sequence only after
// successful CheckTx.
func (c *PerpxBankClient) GenerateTxWithSequence() ([]byte, uint64, error) {
	// Ensure primary account is initialized (needed when senderIdx == workerIndex).
	if err := c.ensureAccountQueried(); err != nil {
		return nil, 0, err
	}

	// Derive (t, j) from a monotonically increasing counter, same as GenerateTx.
	counter := atomic.AddUint64(&c.txCounter, 1) - 1
	rate := c.perConnectionRate
	if rate <= 0 {
		rate = c.config.Rate
	}
	t := counter / uint64(rate)
	j := int(counter % uint64(rate))

	senderIdx, receiverIdx := c.senderReceiverIndices(t, j)

	if c.config.Debug.LogWorkerIDs {
		fmt.Printf("bank sequenced tx sender=%d receiver=%d (conn=%d)\n", senderIdx, receiverIdx, c.connectionIndex)
	}

	var (
		senderPriv cryptotypes.PrivKey
		senderAddr sdk.AccAddress
		seq        uint64
		accountNum uint64
		bench      *benchSender
	)

	if senderIdx == c.workerIndex {
		senderPriv = c.privKey
		senderAddr = c.addr
		accountNum = c.accountNum
		seq = atomic.LoadUint64(&c.sequence)
	} else {
		s, err := getOrInitBenchSender(senderIdx, c.restURL)
		if err != nil {
			return nil, 0, err
		}
		bench = s
		senderPriv = s.priv
		senderAddr = s.addr
		accountNum = s.accountNum
		seq = atomic.LoadUint64(&s.seq)
	}

	// Record last sender for CommitSequence / recovery.
	c.lastSenderMtx.Lock()
	c.lastSenderIdx = senderIdx
	c.lastBench = bench
	c.lastSenderMtx.Unlock()

	toAddr := getBenchAddress(receiverIdx)
	txBuilder := c.encCfg.TxConfig.NewTxBuilder()
	msg, err := c.strategy.CreateMsgTo(senderAddr.String(), toAddr)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create message: %w", err)
	}
	if err := txBuilder.SetMsgs(msg); err != nil {
		return nil, 0, fmt.Errorf("failed to set message: %w", err)
	}

	// Fees (same as GenerateTx)
	gasLimit := uint64(200000)
	minGasPrice := math.NewInt(25000000000)
	feeAmount := minGasPrice.Mul(math.NewInt(int64(gasLimit)))
	feeCoins := sdk.NewCoins(sdk.NewCoin(c.strategy.Denom(), feeAmount))
	txBuilder.SetFeeAmount(feeCoins)
	txBuilder.SetGasLimit(gasLimit)

	sigV2Empty := signing.SignatureV2{
		PubKey: senderPriv.PubKey(),
		Data: &signing.SingleSignatureData{
			SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
			Signature: nil,
		},
		Sequence: seq,
	}
	if err := txBuilder.SetSignatures(sigV2Empty); err != nil {
		return nil, 0, fmt.Errorf("failed to set empty signature: %w", err)
	}

	signerData := authsigning.SignerData{
		Address:       senderAddr.String(),
		ChainID:       c.strategy.ChainID(),
		AccountNumber: accountNum,
		Sequence:      seq,
		PubKey:        senderPriv.PubKey(),
	}
	sigV2, err := tx.SignWithPrivKey(
		context.Background(),
		signing.SignMode_SIGN_MODE_DIRECT,
		signerData,
		txBuilder,
		senderPriv,
		c.encCfg.TxConfig,
		seq,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to sign: %w", err)
	}
	if err := txBuilder.SetSignatures(sigV2); err != nil {
		return nil, 0, fmt.Errorf("failed to set signature: %w", err)
	}

	txBytes, err := c.encCfg.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, 0, fmt.Errorf("failed to encode transaction: %w", err)
	}
	return txBytes, seq, nil
}

// CommitSequence advances the sequence for the sender account that produced the
// most recently generated sequenced tx.
func (c *PerpxBankClient) CommitSequence(next uint64) {
	if next == 0 {
		return
	}
	c.lastSenderMtx.Lock()
	senderIdx := c.lastSenderIdx
	bench := c.lastBench
	c.lastSenderMtx.Unlock()

	if senderIdx == c.workerIndex || bench == nil {
		// Primary sender (client-bound account).
		for {
			cur := atomic.LoadUint64(&c.sequence)
			if next <= cur {
				return
			}
			if atomic.CompareAndSwapUint64(&c.sequence, cur, next) {
				return
			}
		}
	}

	// Bench-derived sender.
	for {
		cur := atomic.LoadUint64(&bench.seq)
		if next <= cur {
			return
		}
		if atomic.CompareAndSwapUint64(&bench.seq, cur, next) {
			return
		}
	}
}

// RecoverSequence re-queries the on-chain sequence for the last sender and
// resets the local sequence accordingly.
func (c *PerpxBankClient) RecoverSequence() error {
	c.lastSenderMtx.Lock()
	senderIdx := c.lastSenderIdx
	bench := c.lastBench
	c.lastSenderMtx.Unlock()

	if senderIdx == c.workerIndex || bench == nil {
		info, err := QueryAccountInfo(c.restURL, c.addr.String(), sharedHTTPClient)
		if err != nil {
			return err
		}
		atomic.StoreUint64(&c.sequence, info.Sequence)
		return nil
	}

	info, err := QueryAccountInfo(c.restURL, bench.addr.String(), sharedHTTPClient)
	if err != nil {
		return err
	}
	atomic.StoreUint64(&bench.seq, info.Sequence)
	return nil
}

// RecoverSequenceTo resets the local sequence for the last sender to a specific
// target value (typically the "expected" value from a CheckTx mismatch).
func (c *PerpxBankClient) RecoverSequenceTo(next uint64) error {
	if next == 0 {
		return nil
	}
	c.lastSenderMtx.Lock()
	senderIdx := c.lastSenderIdx
	bench := c.lastBench
	c.lastSenderMtx.Unlock()

	if senderIdx == c.workerIndex || bench == nil {
		atomic.StoreUint64(&c.sequence, next)
		return nil
	}
	atomic.StoreUint64(&bench.seq, next)
	return nil
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
