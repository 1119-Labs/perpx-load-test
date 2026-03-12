package client

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/1119-Labs/perpx-chain/protocol/app"
	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// initialPerpsClientID returns a random starting client ID for this worker.
// Deterministic/fixed start is configured via cfg.Perps (Viper) and handled in NewPerpxPerpsClient.
func initialPerpsClientID(workerID int, rng *rand.Rand) uint32 {
	// Randomize the starting client ID per run to avoid collisions with
	// still-live long-term/stateful orders when rerunning against the same chain.
	//
	// We also mix in workerID so that two clients created at the same instant
	// still get distinct ranges.
	var r uint32
	if rng != nil {
		r = uint32(rng.Uint32())
	} else {
		r = uint32(time.Now().UnixNano())
	}
	r ^= uint32(workerID) * 0x9e3779b9 // Knuth multiplicative hash

	// Keep IDs in a "high" range to minimize overlap with manual testing that
	// often starts at 1..N.
	start := (r & 0x7fffffff) | (1 << 30)
	if start == 0 {
		start = 1
	}
	return start
}

// TxMonitoringConfig controls transaction status monitoring.
type TxMonitoringConfig struct {
	Enabled    bool
	CheckDelay time.Duration // How long to wait before checking
	MaxChecks  int           // Maximum number of status checks
	// SampleRate controls automatic tx-status sampling for generated transactions.
	// If > 0 and monitoring is enabled, roughly 1 out of every SampleRate
	// generated transactions per client will have its status checked via
	// /cosmos/tx/v1beta1/txs/{hash}. If <= 0, auto-sampling is disabled.
	SampleRate int
}

// trackedOrder is a minimal representation of an order we might later cancel.
// This matches strategies.TrackedOrder for compatibility.
type trackedOrder struct {
	OrderID    clobtypes.OrderId
	ClobPairID uint32
	ClientID   uint32
	OrderFlags uint32 // Store order flags to determine cancel type
}

// toStrategiesTrackedOrder converts a client trackedOrder to strategies.TrackedOrder.
func (o trackedOrder) toStrategiesTrackedOrder() strategies.TrackedOrder {
	return strategies.TrackedOrder{
		OrderID:    o.OrderID,
		ClobPairID: o.ClobPairID,
		ClientID:   o.ClientID,
		OrderFlags: o.OrderFlags,
	}
}

// fromStrategiesTrackedOrder converts a strategies.TrackedOrder to client trackedOrder.
func fromStrategiesTrackedOrder(o strategies.TrackedOrder) trackedOrder {
	return trackedOrder{
		OrderID:    o.OrderID,
		ClobPairID: o.ClobPairID,
		ClientID:   o.ClientID,
		OrderFlags: o.OrderFlags,
	}
}

// PerpxPerpsClient implements loadtest.Client for PerpX perps order scenarios.
// It also implements strategies.OrderTracker and strategies.PositionTracker.
type PerpxPerpsClient struct {
	config   loadtest.Config
	strategy *strategies.PerpsOrderStrategy

	// Account information
	privKey    cryptotypes.PrivKey
	addr       sdk.AccAddress
	accountNum uint64
	sequence   uint64 // Local sequence counter (atomic) when not using timestamp nonces

	// We always use subaccount number 0 for this worker.
	subaccountNumber uint32

	// Encoding config
	encCfg app.EncodingConfig

	// Chain interaction boundary used for tx encoding and, in the future,
	// other chain-facing concerns. This allows us to adapt to SDK/proto
	// changes without modifying higher-level client logic.
	chainAPI PerpsChainAPI

	// Lazy initialization: query account info on first use
	accountQueried  bool
	accountQueryMtx sync.Mutex
	restURL         string // Cached REST API URL
	// When true, use timestamp nonces (Unix ms) for sequences instead of
	// incrementing the on-chain sequence. This exercises the timestamp‑nonce
	// replay protection path in the ante handler and avoids strict sequence
	// matching in CheckTx for high-throughput short-term order flows.
	useTimestampNonce bool

	// Scenario and order-tracking state (worker local).
	rand      *rand.Rand
	orders    []trackedOrder
	nextCID   uint32
	positions map[uint32]*trackedPosition // map of clobPairID -> position
	posMtx    sync.RWMutex                // protects positions map

	// Error handling and robustness
	retryConfig      RetryConfig
	marginConfig     MarginCheckConfig
	txMonitorConfig  TxMonitoringConfig
	errorMetrics     *ErrorMetrics
	workerID         int        // Worker ID for logging context
	orderTrackingMtx sync.Mutex // Protects order tracking operations

	// Testability hooks (default to real implementations).
	sleepFn           func(time.Duration)
	httpClient        *http.Client
	generateTxOnceFn  func() ([]byte, error)
	recoverSequenceFn func() error

	// txMonitorSampleCounter tracks how many transactions this client has
	// generated, for sampling tx-status checks when monitoring is enabled.
	txMonitorSampleCounter uint64
}

// PerpsClientConfig allows YAML/CLI-driven configuration of perps client knobs
// that historically were controlled only via environment variables.
type PerpsClientConfig struct {
	UseTimestampNonce bool

	DeterministicClientIDs bool
	ClientIDStart          uint32

	// Tx monitoring
	MonitorTxStatus     bool
	TxCheckDelayMs      int
	TxMaxChecks         int
	TxMonitorSampleRate int

	// Retry
	MaxRetries   int
	RetryDelayMs int

	// Margin check
	EnableMarginCheck    bool
	MinMarginRatio       string
	OnInsufficientMargin string
}

// Ensure PerpxPerpsClient implements Client, OrderTracker, and PositionTracker.
var _ loadtest.Client = (*PerpxPerpsClient)(nil)
var _ strategies.OrderTracker = (*PerpxPerpsClient)(nil)
var _ strategies.PositionTracker = (*PerpxPerpsClient)(nil)

// OrderTracker interface implementation

// GetRandomOrder returns a random tracked order, or false if none exist.
func (c *PerpxPerpsClient) GetRandomOrder() (strategies.TrackedOrder, bool) {
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()

	if len(c.orders) == 0 {
		return strategies.TrackedOrder{}, false
	}

	idx := c.rand.Intn(len(c.orders))
	return c.orders[idx].toStrategiesTrackedOrder(), true
}

// GetRandomOrdersForMarket returns up to maxCount random orders for the given market.
func (c *PerpxPerpsClient) GetRandomOrdersForMarket(clobPairID uint32, maxCount int) []strategies.TrackedOrder {
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()

	result := make([]strategies.TrackedOrder, 0, maxCount)
	for _, o := range c.orders {
		if o.ClobPairID == clobPairID {
			result = append(result, o.toStrategiesTrackedOrder())
			if len(result) >= maxCount {
				break
			}
		}
	}
	return result
}

// TrackOrder adds an order to tracking.
func (c *PerpxPerpsClient) TrackOrder(order strategies.TrackedOrder) {
	c.trackOrder(fromStrategiesTrackedOrder(order))
}

// RemoveOrder removes an order from tracking.
func (c *PerpxPerpsClient) RemoveOrder(order strategies.TrackedOrder) {
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()

	clientOrder := fromStrategiesTrackedOrder(order)
	for i, o := range c.orders {
		if o.ClientID == clientOrder.ClientID && o.ClobPairID == clientOrder.ClobPairID {
			// Swap with last and remove
			last := len(c.orders) - 1
			c.orders[i] = c.orders[last]
			c.orders = c.orders[:last]
			return
		}
	}
}

// RemoveOrders removes multiple orders from tracking.
func (c *PerpxPerpsClient) RemoveOrders(orders []strategies.TrackedOrder) {
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()

	// Create a map for fast lookup
	toRemove := make(map[uint32]map[uint32]bool) // clobPairID -> clientID -> true
	for _, o := range orders {
		if toRemove[o.ClobPairID] == nil {
			toRemove[o.ClobPairID] = make(map[uint32]bool)
		}
		toRemove[o.ClobPairID][o.ClientID] = true
	}

	// Filter out orders to remove
	remaining := make([]trackedOrder, 0, len(c.orders))
	for _, o := range c.orders {
		if toRemove[o.ClobPairID] == nil || !toRemove[o.ClobPairID][o.ClientID] {
			remaining = append(remaining, o)
		}
	}
	c.orders = remaining
}

// HasOrders returns true if there are any tracked orders.
func (c *PerpxPerpsClient) HasOrders() bool {
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()
	return len(c.orders) > 0
}

// PositionTracker interface implementation

// GetPosition returns the current position for a CLOB pair, or nil if none exists.
func (c *PerpxPerpsClient) GetPosition(clobPairID uint32) *strategies.TrackedPosition {
	return c.getPosition(clobPairID).toStrategiesTrackedPosition()
}

// NewPerpxPerpsClient creates a new perps client.
// The id is a per-worker identifier used to derive a unique account key.
func NewPerpxPerpsClient(cfg loadtest.Config, strategy *strategies.PerpsOrderStrategy, id int) (*PerpxPerpsClient, error) {
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

	c := &PerpxPerpsClient{
		config:            cfg,
		strategy:          strategy,
		privKey:           privKey,
		addr:              addr,
		accountNum:        0,
		sequence:          0,
		subaccountNumber:  0,
		encCfg:            encCfg,
		chainAPI:          defaultPerpsChainAPI{encCfgEncodingTx: encCfg.TxConfig.TxEncoder()},
		accountQueried:    false,
		restURL:           restURL,
		useTimestampNonce: cfg.Perps.UseTimestampNonce,
		rand:              strategies.NewRandForWorker(workerID, cfg.Perps.RNGSeed),
		orders:            make([]trackedOrder, 0, 1024), // Default capacity, will be bounded by scenario
		nextCID:           0,                             // initialized below
		positions:         make(map[uint32]*trackedPosition),
		workerID:          workerID,
		errorMetrics:      NewErrorMetrics(),
		retryConfig:       loadRetryConfigFromConfig(cfg),
		marginConfig:      loadMarginConfigFromConfig(cfg),
		txMonitorConfig:   loadTxMonitorConfigFromConfig(cfg),
		sleepFn:           time.Sleep,
		httpClient:        &http.Client{Timeout: 10 * time.Second},
	}

	// Client ID start: YAML overrides env, otherwise preserve existing behavior.
	if cfg.Perps.DeterministicClientIDs {
		c.nextCID = 1
	} else if cfg.Perps.ClientIDStart > 0 {
		c.nextCID = cfg.Perps.ClientIDStart
	} else {
		c.nextCID = initialPerpsClientID(workerID, c.rand)
	}

	return c, nil
}

func loadRetryConfigFromConfig(cfg loadtest.Config) RetryConfig {
	maxRetries := 3
	if cfg.Perps.MaxRetries > 0 {
		maxRetries = cfg.Perps.MaxRetries
	}
	retryDelayMs := 100
	if cfg.Perps.RetryDelayMs > 0 {
		retryDelayMs = cfg.Perps.RetryDelayMs
	}
	return RetryConfig{
		MaxRetries: maxRetries,
		RetryDelay: time.Duration(retryDelayMs) * time.Millisecond,
		RetryableErrors: []string{
			"sequence", "mismatch", "insufficient", "funds", "rate limit", "timeout", "network", "temporary",
		},
	}
}

func loadMarginConfigFromConfig(cfg loadtest.Config) MarginCheckConfig {
	enabled := cfg.Perps.EnableMarginCheck
	minMarginRatio := 1.0
	if v := strings.TrimSpace(cfg.Perps.MinMarginRatio); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			minMarginRatio = parsed
		}
	}
	onInsufficient := "skip"
	if v := strings.TrimSpace(cfg.Perps.OnInsufficientMargin); v != "" {
		v = strings.ToLower(v)
		if v == "skip" || v == "deposit" || v == "close" {
			onInsufficient = v
		}
	}
	return MarginCheckConfig{
		Enabled:        enabled,
		MinMarginRatio: minMarginRatio,
		OnInsufficient: onInsufficient,
	}
}

func loadTxMonitorConfigFromConfig(cfg loadtest.Config) TxMonitoringConfig {
	checkDelayMs := 1000
	if cfg.Perps.TxCheckDelayMs > 0 {
		checkDelayMs = cfg.Perps.TxCheckDelayMs
	}
	maxChecks := 10
	if cfg.Perps.TxMaxChecks > 0 {
		maxChecks = cfg.Perps.TxMaxChecks
	}
	sampleRate := 0
	if cfg.Perps.TxMonitorSampleRate > 0 {
		sampleRate = cfg.Perps.TxMonitorSampleRate
	}
	return TxMonitoringConfig{
		Enabled:    cfg.Perps.MonitorTxStatus,
		CheckDelay: time.Duration(checkDelayMs) * time.Millisecond,
		MaxChecks:  maxChecks,
		SampleRate: sampleRate,
	}
}

// validateOrderParams validates order parameters before creating a message.
// isCloseOrder indicates if this is a close order (position-based), which may have different size constraints.
func (c *PerpxPerpsClient) validateOrderParams(clobPairID uint32, quantums, subticks uint64, clientID uint32, goodTilBlock uint32, isCloseOrder bool) error {
	// Validate quantums
	if quantums == 0 {
		c.errorMetrics.IncrementInvalidOrderParams()
		return fmt.Errorf("quantums must be > 0")
	}

	// Validate subticks
	if subticks == 0 {
		c.errorMetrics.IncrementInvalidOrderParams()
		return fmt.Errorf("subticks must be > 0")
	}

	// Validate subticks are within market bounds
	market := c.findMarket(clobPairID)
	if market != nil {
		if subticks < market.MinSubticks || subticks > market.MaxSubticks {
			c.errorMetrics.IncrementInvalidOrderParams()
			return fmt.Errorf("subticks %d out of bounds [%d, %d] for clobPairID %d", subticks, market.MinSubticks, market.MaxSubticks, clobPairID)
		}
		// For close orders, quantums may exceed market bounds (they're based on position size)
		// Only validate quantums bounds for non-close orders
		if !isCloseOrder {
			if quantums < market.MinQuantityQuantums || quantums > market.MaxQuantityQuantums {
				c.errorMetrics.IncrementInvalidOrderParams()
				return fmt.Errorf("quantums %d out of bounds [%d, %d] for clobPairID %d", quantums, market.MinQuantityQuantums, market.MaxQuantityQuantums, clobPairID)
			}
		}
	}

	// Validate goodTilBlock (should be in reasonable range)
	if goodTilBlock == 0 {
		c.errorMetrics.IncrementInvalidOrderParams()
		return fmt.Errorf("goodTilBlock must be > 0")
	}

	// Check if client ID is unique (check against tracked orders)
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()
	for _, order := range c.orders {
		if order.ClientID == clientID && order.ClobPairID == clobPairID {
			c.errorMetrics.IncrementInvalidOrderParams()
			return fmt.Errorf("client ID %d already in use for clobPairID %d", clientID, clobPairID)
		}
	}

	return nil
}

// findMarket finds the market configuration for a given CLOB pair ID.
func (c *PerpxPerpsClient) findMarket(clobPairID uint32) *strategies.PerpsMarketConfig {
	if c.strategy == nil {
		return nil
	}
	scenario := c.strategy.Scenario()
	if scenario == nil {
		return nil
	}
	for i := range scenario.Markets {
		if scenario.Markets[i].ClobPairID == clobPairID {
			return &scenario.Markets[i]
		}
	}
	return nil
}

// ensureAccountQueried queries account info if not already queried (lazy initialization).
// Includes retry logic with exponential backoff for graceful degradation.
func (c *PerpxPerpsClient) ensureAccountQueried() error {
	c.accountQueryMtx.Lock()
	defer c.accountQueryMtx.Unlock()

	if c.accountQueried {
		return nil
	}

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

	// Retry logic with exponential backoff
	var lastErr error
	delay := c.retryConfig.RetryDelay
	sleep := c.sleepFn
	if sleep == nil {
		sleep = time.Sleep
	}
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			c.errorMetrics.IncrementRetryCount()
			c.errorMetrics.IncrementErrorCount("account_query_retry")
			sleep(delay)
			delay *= 2 // Exponential backoff
		}

		resp, err := httpClient.Get(accountURL)
		if err != nil {
			lastErr = fmt.Errorf("failed to query account via REST API at %s (account %s may not exist - run 'seed' command first): %w", accountURL, c.addr.String(), err)
			if attempt < c.retryConfig.MaxRetries && c.isRetryableError(err) {
				continue
			}
			c.errorMetrics.IncrementAccountQueryFailures()
			c.errorMetrics.IncrementErrorCount("account_query_failed")
			return lastErr
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			lastErr = fmt.Errorf("failed to query account: HTTP %d: %s (account %s may not exist - run 'seed' command first)", resp.StatusCode, string(body), c.addr.String())
			if attempt < c.retryConfig.MaxRetries {
				continue
			}
			c.errorMetrics.IncrementAccountQueryFailures()
			c.errorMetrics.IncrementErrorCount("account_query_http_error")
			return lastErr
		}

		if err := json.NewDecoder(resp.Body).Decode(&accountResp); err != nil {
			lastErr = fmt.Errorf("failed to decode account response: %w", err)
			if attempt < c.retryConfig.MaxRetries && c.isRetryableError(err) {
				continue
			}
			c.errorMetrics.IncrementAccountQueryFailures()
			c.errorMetrics.IncrementErrorCount("account_query_decode_error")
			return lastErr
		}

		accountNum, err := strconv.ParseUint(accountResp.Account.AccountNumber, 10, 64)
		if err != nil {
			lastErr = fmt.Errorf("failed to parse account number: %w", err)
			if attempt < c.retryConfig.MaxRetries {
				continue
			}
			c.errorMetrics.IncrementAccountQueryFailures()
			c.errorMetrics.IncrementErrorCount("account_query_parse_error")
			return lastErr
		}
		sequence, err := strconv.ParseUint(accountResp.Account.Sequence, 10, 64)
		if err != nil {
			lastErr = fmt.Errorf("failed to parse sequence: %w", err)
			if attempt < c.retryConfig.MaxRetries {
				continue
			}
			c.errorMetrics.IncrementAccountQueryFailures()
			c.errorMetrics.IncrementErrorCount("account_query_parse_error")
			return lastErr
		}

		c.accountNum = accountNum
		// Never move the local sequence backwards. REST reflects committed state,
		// which may lag mempool "expected" when there are pending txs. If we
		// already advanced our local counter (e.g., via CheckTx recovery),
		// clobbering it with a lower committed sequence will cause us to re-use
		// old sequences and trigger a cascade of "account sequence mismatch"
		// CheckTx failures.
		if cur := atomic.LoadUint64(&c.sequence); cur > sequence {
			sequence = cur
		}
		atomic.StoreUint64(&c.sequence, sequence)
		c.accountQueried = true
		return nil
	}

	c.errorMetrics.IncrementAccountQueryFailures()
	c.errorMetrics.IncrementErrorCount("account_query_max_retries_exceeded")
	return lastErr
}

// GenerateTx generates a perps-related transaction based on the configured scenario.
// Includes retry logic with exponential backoff for failed transactions.
func (c *PerpxPerpsClient) GenerateTx() ([]byte, error) {
	var lastErr error
	delay := c.retryConfig.RetryDelay
	sleep := c.sleepFn
	if sleep == nil {
		sleep = time.Sleep
	}
	genOnce := c.generateTxOnce
	if c.generateTxOnceFn != nil {
		genOnce = c.generateTxOnceFn
	}
	recoverFn := c.recoverSequence
	if c.recoverSequenceFn != nil {
		recoverFn = c.recoverSequenceFn
	}

	// Retry loop for transaction generation
	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			c.errorMetrics.IncrementRetryCount()
			c.errorMetrics.IncrementErrorCount("tx_generation_retry")
			sleep(delay)
			delay *= 2 // Exponential backoff

			// If error was sequence- or auth-related, try to recover account
			// metadata (account number + sequence) from the chain before retrying.
			if lastErr != nil {
				errStr := strings.ToLower(lastErr.Error())
				if strings.Contains(errStr, "sequence") ||
					strings.Contains(errStr, "signature verification failed") ||
					strings.Contains(errStr, "account number") ||
					strings.Contains(errStr, "unauthorized") {
					if err := recoverFn(); err != nil {
						// Recovery failed, continue with retry
						c.errorMetrics.incrementErrorCountNoLock("sequence_recovery_failed")
					}
				}
			}
		}

		txBytes, err := genOnce()
		if err == nil {
			// Optionally kick off tx-status monitoring for a sampled subset
			// of generated transactions when monitoring is enabled.
			c.maybeMonitorTxStatus(txBytes)
			return txBytes, nil
		}

		lastErr = err

		// If error is not retryable, return immediately
		if !c.isRetryableError(err) {
			c.errorMetrics.IncrementErrorCount("non_retryable_error")
			return nil, err
		}

		// If we've exhausted retries, return the last error
		if attempt >= c.retryConfig.MaxRetries {
			c.errorMetrics.IncrementTransactionFailures()
			c.errorMetrics.IncrementErrorCount("tx_generation_max_retries_exceeded")
			return nil, fmt.Errorf("failed to generate transaction after %d retries: %w", c.retryConfig.MaxRetries, lastErr)
		}
	}

	c.errorMetrics.IncrementTransactionFailures()
	return nil, lastErr
}

// maybeMonitorTxStatus performs optional tx-status monitoring for a sampled
// subset of generated transactions. It computes the Cosmos SDK tx hash
// (sha256 of the raw tx bytes) and, if sampling conditions are met, spawns
// a goroutine that calls CheckTxStatus against the REST API.
func (c *PerpxPerpsClient) maybeMonitorTxStatus(txBytes []byte) {
	if !c.txMonitorConfig.Enabled {
		return
	}
	if c.txMonitorConfig.SampleRate <= 0 {
		return
	}

	n := atomic.AddUint64(&c.txMonitorSampleCounter, 1)
	if n%uint64(c.txMonitorConfig.SampleRate) != 0 {
		return
	}

	// Compute tx hash as SHA-256 of the raw transaction bytes, encoded as
	// upper-case hex, which matches Cosmos SDK / Tendermint conventions.
	sum := sha256.Sum256(txBytes)
	txHash := fmt.Sprintf("%X", sum[:])
	workerID := c.workerID

	go func() {
		included, succeeded, err := c.CheckTxStatus(txHash)
		prefix := fmt.Sprintf("[perps-tx-monitor worker=%d]", workerID)
		if err != nil {
			fmt.Printf("%s tx %s: included=%v succeeded=%v error=%v\n", prefix, txHash, included, succeeded, err)
			return
		}
		fmt.Printf("%s tx %s: included=%v succeeded=%v\n", prefix, txHash, included, succeeded)
	}()
}

func (c *PerpxPerpsClient) trackOrder(o trackedOrder) {
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()

	scenario := c.strategy.Scenario()
	if len(c.orders) >= scenario.MaxTrackedOrders {
		// Graceful degradation: use FIFO eviction (remove first order) instead of random
		// This is more predictable and helps with order tracking consistency
		c.orders = c.orders[1:] // Remove first element (FIFO)
		c.errorMetrics.IncrementErrorCount("order_tracking_full_evicted")
	}
	c.orders = append(c.orders, o)
}

// ClearOrderTracking clears all tracked orders. Useful for recovery when tracking becomes inconsistent.
func (c *PerpxPerpsClient) ClearOrderTracking() {
	c.orderTrackingMtx.Lock()
	defer c.orderTrackingMtx.Unlock()
	c.orders = c.orders[:0]
	c.errorMetrics.IncrementErrorCount("order_tracking_cleared")
}

// CheckTxStatus checks the status of a transaction by hash (optional monitoring).
// It queries the chain's REST API endpoint and polls until the transaction is found or max checks are reached.
// Returns (included, succeeded, error) where:
//   - included: true if transaction was included in a block
//   - succeeded: true if transaction succeeded (code == 0)
//   - error: non-nil if transaction not found after max checks or other errors occurred
func (c *PerpxPerpsClient) CheckTxStatus(txHash string) (included bool, succeeded bool, err error) {
	if !c.txMonitorConfig.Enabled {
		return false, false, fmt.Errorf("transaction monitoring is disabled")
	}

	// Wait for the configured delay before first check
	sleep := c.sleepFn
	if sleep == nil {
		sleep = time.Sleep
	}
	sleep(c.txMonitorConfig.CheckDelay)

	txStatusURL := fmt.Sprintf("%s/cosmos/tx/v1beta1/txs/%s", c.restURL, txHash)
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	var txStatusResp struct {
		TxResponse struct {
			Height string `json:"height"`
			Code   int    `json:"code"`
			RawLog string `json:"raw_log"`
		} `json:"tx_response"`
	}

	// Poll for transaction status
	for check := 0; check < c.txMonitorConfig.MaxChecks; check++ {
		resp, err := httpClient.Get(txStatusURL)
		if err != nil {
			// Network error - log and continue polling
			c.errorMetrics.IncrementErrorCount("tx_status_check_network_error")
			if check < c.txMonitorConfig.MaxChecks-1 {
				sleep(c.txMonitorConfig.CheckDelay)
				continue
			}
			// Last attempt failed
			return false, false, fmt.Errorf("failed to query transaction status after %d attempts: %w", c.txMonitorConfig.MaxChecks, err)
		}

		// Handle different HTTP status codes
		if resp.StatusCode == http.StatusNotFound {
			// Transaction not found yet - continue polling
			resp.Body.Close()
			if check < c.txMonitorConfig.MaxChecks-1 {
				sleep(c.txMonitorConfig.CheckDelay)
				continue
			}
			// Last check and still not found
			return false, false, fmt.Errorf("transaction %s not found after %d checks", txHash, c.txMonitorConfig.MaxChecks)
		}

		if resp.StatusCode != http.StatusOK {
			// HTTP error - log and continue polling if not last attempt
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			c.errorMetrics.IncrementErrorCount("tx_status_check_http_error")
			if check < c.txMonitorConfig.MaxChecks-1 {
				sleep(c.txMonitorConfig.CheckDelay)
				continue
			}
			// Last attempt failed
			return false, false, fmt.Errorf("failed to query transaction status: HTTP %d: %s", resp.StatusCode, string(body))
		}

		// Parse JSON response
		if err := json.NewDecoder(resp.Body).Decode(&txStatusResp); err != nil {
			resp.Body.Close()
			c.errorMetrics.IncrementErrorCount("tx_status_check_decode_error")
			if check < c.txMonitorConfig.MaxChecks-1 {
				sleep(c.txMonitorConfig.CheckDelay)
				continue
			}
			// Last attempt failed
			return false, false, fmt.Errorf("failed to decode transaction status response: %w", err)
		}
		resp.Body.Close()

		// Check if transaction was included in a block
		height := txStatusResp.TxResponse.Height
		if height != "" && height != "0" {
			// Transaction was included
			included = true
			code := txStatusResp.TxResponse.Code
			succeeded = (code == 0)

			if !succeeded {
				// Transaction failed - log the error
				c.errorMetrics.IncrementTransactionFailures()
				c.errorMetrics.IncrementErrorCount("tx_status_check_failed")
				return true, false, fmt.Errorf("transaction failed in block %s: code %d, log: %s", height, code, txStatusResp.TxResponse.RawLog)
			}

			// Transaction succeeded
			return true, true, nil
		}

		// Transaction not included yet - continue polling
		if check < c.txMonitorConfig.MaxChecks-1 {
			sleep(c.txMonitorConfig.CheckDelay)
		}
	}

	// Exhausted all checks without finding the transaction
	return false, false, fmt.Errorf("transaction %s not found after %d checks", txHash, c.txMonitorConfig.MaxChecks)
}
