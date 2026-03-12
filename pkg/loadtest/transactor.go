package loadtest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/1119-Labs/perpx-load-test/internal/logging"
	"github.com/gorilla/websocket"
)

const (
	// Increased timeout to handle heavy load scenarios where write buffer may fill up
	connSendTimeout = 30 * time.Second
	connPingPeriod  = (30 * 9 / 10) * time.Second

	jsonRPCID = -1

	defaultProgressCallbackInterval = 5 * time.Second

	// periodicSequenceRecoveryInterval is how many txs to send between periodic
	// sequence recoveries when using broadcast_tx_async (which does not return
	// CheckTx results, so we re-sync with the chain periodically).
	periodicSequenceRecoveryInterval = 25
)

// validateWebSocketURL parses and validates a user-provided WebSocket URL.
// It ensures that only ws:// or wss:// URLs with a non-empty host and without
// control characters are used for outbound connections.
func validateWebSocketURL(raw string) (*url.URL, error) {
	if strings.ContainsAny(raw, "\r\n") {
		return nil, fmt.Errorf("invalid WebSocket URL %q: contains control characters", raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid WebSocket URL %q: %w", raw, err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return nil, fmt.Errorf("unsupported protocol in WebSocket URL %q: %s (only ws:// and wss:// are supported)", raw, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid WebSocket URL %q: missing host", raw)
	}
	return u, nil
}

// Transactor represents a single wire-level connection to a CometBFT RPC
// endpoint, and this is responsible for sending transactions to that endpoint.
type Transactor struct {
	remoteAddr string  // The full URL of the remote WebSockets endpoint.
	config     *Config // The configuration for the load test.

	client            Client
	logger            logging.Logger
	conn              *websocket.Conn
	broadcastTxMethod string
	wg                sync.WaitGroup

	// Rudimentary statistics
	statsMtx  sync.RWMutex
	startTime time.Time // When did the transaction sending start?
	txCount   int       // How many transactions have been sent.
	txBytes   int64     // How many transaction bytes have been sent, cumulatively.
	txRate    float64   // The number of transactions sent, per second.

	progressCallbackMtx      sync.RWMutex
	progressCallbackID       int                                      // A unique identifier for this transactor when calling the progress callback.
	progressCallbackInterval time.Duration                            // How frequently to call the progress update callback.
	progressCallback         func(id int, txCount int, txBytes int64) // Called with the total number of transactions executed so far.

	stopMtx sync.RWMutex
	stop    bool
	stopErr error // Did an error occur that triggered the stop?

	// sequenceRecoveryRequested is set when we observe CheckTx sequence mismatch
	// errors in the websocket responses. Recovery is performed in the send loop
	// (not the receive loop) to avoid concurrent mutation of client state.
	sequenceRecoveryRequested atomic.Bool
	// sequenceRecoveryTarget is the next sequence we should use (when we can
	// extract it from the CheckTx log). This corresponds to mempool state.
	sequenceRecoveryTarget atomic.Uint64

	// lastRecoveryAtTxCount is the tx count when we last ran sequence recovery.
	// Used for periodic recovery when using broadcast_tx_async (which does not
	// return CheckTx results, so we never see sequence mismatch responses).
	lastRecoveryAtTxCount atomic.Int32

	// checkTxResults is used to coordinate sequence advancement with CheckTx
	// outcomes in sync/commit mode. When enabled, the send loop will wait for a
	// CheckTx response after each tx before generating the next one, allowing the
	// client to only advance its local sequence on success.
	checkTxResults chan checkTxResult
}

type checkTxResult struct {
	hash      string
	code      int
	codespace string
	log       string
}

func normalizeTxHash(h string) string {
	return strings.ToUpper(strings.TrimSpace(h))
}

func txHashFromBytes(tx []byte) string {
	sum := sha256.Sum256(tx)
	return fmt.Sprintf("%X", sum[:])
}

func shouldRecoverSequence(code int, log string) bool {
	// Canonical Cosmos SDK sequence mismatch code.
	if code == 32 {
		return true
	}

	logLower := strings.ToLower(log)
	// Some chains/modules can surface auth-related mismatches via module-specific
	// codes or wrapped logs. Recover only when the log indicates a sequence/auth
	// problem, not for business-level validation failures (e.g. clob 3006/3007).
	return strings.Contains(logLower, "account sequence mismatch") ||
		strings.Contains(logLower, "incorrect account sequence") ||
		strings.Contains(logLower, "signature verification failed") ||
		strings.Contains(logLower, "account number") ||
		strings.Contains(logLower, "unauthorized")
}

// sequencedTxClient is an optional interface that allows the transactor to
// generate transactions without advancing sequence, and to advance sequence
// only after a successful CheckTx.
type sequencedTxClient interface {
	GenerateTxWithSequence() (tx []byte, seq uint64, err error)
	CommitSequence(next uint64)
}

type sequencedTxClientWithEffects interface {
	GenerateTxWithSequenceAndEffects() (tx []byte, seq uint64, onCheckTxSuccess func(), err error)
	CommitSequence(next uint64)
}

type sequencedTxClientWithEffectsAndRejectHook interface {
	GenerateTxWithSequenceAndEffectsAndRejectHook() (tx []byte, seq uint64, onCheckTxSuccess func(), onCheckTxReject func(code int, codespace, log string), err error)
	CommitSequence(next uint64)
}

// NewTransactor initiates a WebSockets connection to the given host address.
// Must be a valid WebSockets URL, e.g. "ws://host:port/websocket"
func NewTransactor(remoteAddr string, config *Config) (*Transactor, error) {
	u, err := validateWebSocketURL(remoteAddr)
	if err != nil {
		return nil, err
	}
	clientFactory, exists := clientFactories[config.ClientFactory]
	if !exists {
		return nil, fmt.Errorf("unrecognized client factory: %s", config.ClientFactory)
	}
	client, err := clientFactory.NewClient(*config)
	if err != nil {
		return nil, err
	}
	// Set a timeout for WebSocket dial to prevent hanging
	// Create a new dialer instead of modifying the default one
	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, resp, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial WebSocket endpoint %s: %w", remoteAddr, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("failed to connect to remote WebSockets endpoint %s: %s (status code %d)", remoteAddr, resp.Status, resp.StatusCode)
	}
	logger := logging.NewLogrusLogger(fmt.Sprintf("transactor[%s]", u.String()))
	logger.Debug("Connected to remote CometBFT WebSockets RPC")
	return &Transactor{
		remoteAddr:               u.String(),
		config:                   config,
		client:                   client,
		logger:                   logger,
		conn:                     conn,
		broadcastTxMethod:        "broadcast_tx_" + config.BroadcastTxMethod,
		progressCallbackInterval: defaultProgressCallbackInterval,
		checkTxResults:           make(chan checkTxResult, 2048),
	}, nil
}

func (t *Transactor) SetProgressCallback(id int, interval time.Duration, callback func(int, int, int64)) {
	t.progressCallbackMtx.Lock()
	t.progressCallbackID = id
	t.progressCallbackInterval = interval
	t.progressCallback = callback
	t.progressCallbackMtx.Unlock()
}

// Start kicks off the transactor's operations in separate goroutines (one for
// reading from the WebSockets endpoint, and one for writing to it).
func (t *Transactor) Start() {
	t.logger.Debug("Starting transactor")
	t.wg.Add(2)
	go t.receiveLoop()
	go t.sendLoop()
}

// Cancel will indicate to the transactor that it must stop, but does not wait
// until it has completely stopped. To wait, call the Transactor.Wait() method.
func (t *Transactor) Cancel() {
	t.setStop(fmt.Errorf("transactor operations cancelled"))
}

// Wait will block until the transactor terminates.
func (t *Transactor) Wait() error {
	t.wg.Wait()
	t.stopMtx.RLock()
	defer t.stopMtx.RUnlock()
	return t.stopErr
}

// GetTxCount returns the total number of transactions sent thus far by this
// transactor.
func (t *Transactor) GetTxCount() int {
	t.statsMtx.RLock()
	defer t.statsMtx.RUnlock()
	return t.txCount
}

// GetTxBytes returns the cumulative total number of bytes (as transactions)
// sent thus far by this transactor.
func (t *Transactor) GetTxBytes() int64 {
	t.statsMtx.RLock()
	defer t.statsMtx.RUnlock()
	return t.txBytes
}

// GetTxRate returns the average number of transactions per second sent by
// this transactor over the duration of its operation.
func (t *Transactor) GetTxRate() float64 {
	t.statsMtx.RLock()
	defer t.statsMtx.RUnlock()
	return t.txRate
}

func (t *Transactor) receiveLoop() {
	defer t.wg.Done()
	for {
		_, msg, err := t.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				t.logger.Error("Failed to read response on connection", "err", err)
				return
			}
		}

		// If we get an RPC response, surface errors and failed CheckTx responses.
		// This is especially important when using broadcast_tx_sync/commit, where
		// CheckTx failures do not manifest as websocket-level errors.
		if len(msg) > 0 {
			var res RPCResponse
			if err := json.Unmarshal(msg, &res); err == nil {
				if res.Error != nil && res.Error.Code != 0 {
					t.logger.Error(
						"RPC error response",
						"code", res.Error.Code,
						"message", res.Error.Message,
						"data", res.Error.Data,
					)
				} else if len(res.Result) > 0 {
					// broadcast_tx_sync style result (flat code/log/hash).
					var br struct {
						Code      int    `json:"code"`
						Codespace string `json:"codespace"`
						Log       string `json:"log"`
						Hash      string `json:"hash"`
					}
					if err := json.Unmarshal(res.Result, &br); err == nil && br.Hash != "" {
						// Surface the result to the send loop for sync sequencing (best-effort).
						select {
						case t.checkTxResults <- checkTxResult{hash: br.Hash, code: br.Code, codespace: br.Codespace, log: br.Log}:
						default:
						}

						if br.Code != 0 {
							logStr := br.Log
							if len(logStr) > 800 {
								logStr = logStr[:800] + "…"
							}
							if shouldRecoverSequence(br.Code, br.Log) {
								t.sequenceRecoveryTarget.Store(0)
								if br.Code == 32 {
									if exp, ok := parseSequenceMismatchExpected(br.Log); ok {
										t.sequenceRecoveryTarget.Store(exp)
									}
								}
								t.sequenceRecoveryRequested.Store(true)
							}
							t.logger.Error(
								"CheckTx rejected transaction",
								"hash", br.Hash,
								"code", br.Code,
								"codespace", br.Codespace,
								"log", logStr,
							)
						}
					} else {
						// broadcast_tx_commit style result (check_tx/deliver_tx).
						var bc struct {
							Hash    string `json:"hash"`
							CheckTx *struct {
								Code      int    `json:"code"`
								Codespace string `json:"codespace"`
								Log       string `json:"log"`
							} `json:"check_tx"`
							DeliverTx *struct {
								Code      int    `json:"code"`
								Codespace string `json:"codespace"`
								Log       string `json:"log"`
							} `json:"deliver_tx"`
						}
						if err := json.Unmarshal(res.Result, &bc); err == nil && bc.Hash != "" {
							if bc.CheckTx != nil {
								// Surface the CheckTx result to the send loop for sync sequencing (best-effort).
								select {
								case t.checkTxResults <- checkTxResult{hash: bc.Hash, code: bc.CheckTx.Code, codespace: bc.CheckTx.Codespace, log: bc.CheckTx.Log}:
								default:
								}
							}

							if bc.CheckTx != nil && bc.CheckTx.Code != 0 {
								logStr := bc.CheckTx.Log
								if len(logStr) > 800 {
									logStr = logStr[:800] + "…"
								}
								if shouldRecoverSequence(bc.CheckTx.Code, bc.CheckTx.Log) {
									t.sequenceRecoveryTarget.Store(0)
									if bc.CheckTx.Code == 32 {
										if exp, ok := parseSequenceMismatchExpected(bc.CheckTx.Log); ok {
											t.sequenceRecoveryTarget.Store(exp)
										}
									}
									t.sequenceRecoveryRequested.Store(true)
								}
								t.logger.Error(
									"CheckTx rejected transaction",
									"hash", bc.Hash,
									"code", bc.CheckTx.Code,
									"codespace", bc.CheckTx.Codespace,
									"log", logStr,
								)
							} else if bc.DeliverTx != nil && bc.DeliverTx.Code != 0 {
								logStr := bc.DeliverTx.Log
								if len(logStr) > 800 {
									logStr = logStr[:800] + "…"
								}
								t.logger.Error(
									"DeliverTx failed transaction",
									"hash", bc.Hash,
									"code", bc.DeliverTx.Code,
									"codespace", bc.DeliverTx.Codespace,
									"log", logStr,
								)
							}
						}
					}
				}
			}
		}

		if t.mustStop() {
			return
		}
	}
}

func (t *Transactor) sendLoop() {
	defer t.wg.Done()
	t.conn.SetPingHandler(func(message string) error {
		err := t.conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(connSendTimeout))
		if err == websocket.ErrCloseSent {
			return nil
		}
		return err
	})

	pingTicker := time.NewTicker(connPingPeriod)
	timeLimitTicker := time.NewTicker(time.Duration(t.config.Time) * time.Second)
	sendTicker := time.NewTicker(time.Duration(t.config.SendPeriod) * time.Second)
	progressTicker := time.NewTicker(t.getProgressCallbackInterval())
	defer func() {
		pingTicker.Stop()
		timeLimitTicker.Stop()
		sendTicker.Stop()
		progressTicker.Stop()
	}()

	for {
		if t.config.Count > 0 {
			currentCount := t.GetTxCount()
			if currentCount >= t.config.Count {
				t.logger.Info("Maximum transaction limit reached", "count", currentCount)
				t.setStop(nil)
			}
		}
		select {
		case <-sendTicker.C:
			if err := t.sendTransactions(); err != nil {
				t.logger.Error("Failed to send transactions", "err", err)
				t.setStop(err)
			}

		case <-progressTicker.C:
			t.reportProgress()

		case <-pingTicker.C:
			if err := t.sendPing(); err != nil {
				t.logger.Error("Failed to write ping message", "err", err)
				t.setStop(err)
			}

		case <-timeLimitTicker.C:
			// Each transactor hits the time limit around the same time; logging this
			// at INFO would produce one line per connection. Keep at DEBUG to avoid
			// spamming while still retaining visibility when debugging.
			t.logger.Debug("Time limit reached for load testing")
			t.setStop(nil)
		}
		if t.mustStop() {
			t.close()
			return
		}
	}
}

func (t *Transactor) writeTx(tx []byte) error {
	txBase64 := base64.StdEncoding.EncodeToString(tx)
	paramsJSON, err := json.Marshal(map[string]interface{}{"tx": txBase64})
	if err != nil {
		return err
	}
	_ = t.conn.SetWriteDeadline(time.Now().Add(connSendTimeout))
	return t.conn.WriteJSON(RPCRequest{
		JSONRPC: "2.0",
		ID:      jsonRPCID,
		Method:  t.broadcastTxMethod,
		Params:  json.RawMessage(paramsJSON),
	})
}

func (t *Transactor) mustStop() bool {
	t.stopMtx.RLock()
	defer t.stopMtx.RUnlock()
	return t.stop
}

func (t *Transactor) setStop(err error) {
	t.stopMtx.Lock()
	t.stop = true
	if err != nil {
		t.stopErr = err
	}
	t.stopMtx.Unlock()
}

func (t *Transactor) sendTransactions() error {
	// send as many transactions as we can, up to the send rate
	totalSent := t.GetTxCount()
	toSend := t.config.Rate
	if (t.config.Count > 0) && ((totalSent + toSend) > t.config.Count) {
		toSend = t.config.Count - totalSent
		t.logger.Debug("Nearing max transaction count", "totalSent", totalSent, "maxTxCount", t.config.Count, "toSend", toSend)
	}
	if totalSent == 0 {
		t.trackStartTime()
	}
	var sent int
	var sentBytes int64
	defer func() { t.trackSentTxs(sent, sentBytes) }()

	// Periodic sequence recovery: with broadcast_tx_async we never get CheckTx
	// results, so local sequence can drift. Re-sync every N txs when the client
	// supports it.
	if r, ok := t.client.(SequenceRecoverer); ok {
		last := int(t.lastRecoveryAtTxCount.Load())
		if totalSent > 0 && (totalSent-last) >= periodicSequenceRecoveryInterval {
			t.logger.Debug("Periodic sequence recovery (async mode)", "totalSent", totalSent)
			if err := r.RecoverSequence(); err != nil {
				t.logger.Error("Periodic sequence recovery failed", "err", err)
			} else {
				t.lastRecoveryAtTxCount.Store(int32(totalSent))
			}
		}
	}

	// This is very noisy at high TPS (printed every send period, per connection).
	// Keep it at DEBUG so default INFO output stays readable.
	t.logger.Debug("Sending batch of transactions", "toSend", toSend)

	// In sync/commit mode, if the client supports sequenced generation, send
	// sequentially and only advance local sequence on successful CheckTx.
	if t.config.BroadcastTxMethod == "sync" || t.config.BroadcastTxMethod == "commit" {
		var (
			sc  sequencedTxClient
			sce sequencedTxClientWithEffects
			scr sequencedTxClientWithEffectsAndRejectHook
			ok  bool
		)
		if scr, ok = t.client.(sequencedTxClientWithEffectsAndRejectHook); ok {
			// ok
		} else if sce, ok = t.client.(sequencedTxClientWithEffects); ok {
			// ok
		} else if sc, ok = t.client.(sequencedTxClient); ok {
			// ok
		} else {
			ok = false
		}

		if ok {
			// Buffer out-of-order results keyed by tx hash.
			pending := make(map[string]checkTxResult, 16)
			for ; sent < toSend; sent++ {
				if t.sequenceRecoveryRequested.Load() {
					if r, ok := t.client.(SequenceRecovererTo); ok {
						exp := t.sequenceRecoveryTarget.Load()
						if exp > 0 {
							t.logger.Info("Recovering account sequence after CheckTx mismatch", "next", exp)
							if err := r.RecoverSequenceTo(exp); err != nil {
								return fmt.Errorf("failed to recover account sequence to %d after CheckTx mismatch: %w", exp, err)
							}
						} else if r2, ok := t.client.(SequenceRecoverer); ok {
							t.logger.Info("Recovering account sequence after CheckTx mismatch")
							if err := r2.RecoverSequence(); err != nil {
								return fmt.Errorf("failed to recover account sequence after CheckTx mismatch: %w", err)
							}
						}
					} else if r, ok := t.client.(SequenceRecoverer); ok {
						t.logger.Info("Recovering account sequence after CheckTx mismatch")
						if err := r.RecoverSequence(); err != nil {
							return fmt.Errorf("failed to recover account sequence after CheckTx mismatch: %w", err)
						}
					}
					t.sequenceRecoveryRequested.Store(false)
				}

				var (
					tx       []byte
					seq      uint64
					onAccept func()
					onReject func(code int, codespace, log string)
					err      error
				)
				if scr != nil {
					tx, seq, onAccept, onReject, err = scr.GenerateTxWithSequenceAndEffectsAndRejectHook()
				} else if sce != nil {
					tx, seq, onAccept, err = sce.GenerateTxWithSequenceAndEffects()
					onReject = func(int, string, string) {}
				} else {
					tx, seq, err = sc.GenerateTxWithSequence()
					onAccept = func() {}
					onReject = func(int, string, string) {}
				}
				if err != nil {
					return err
				}
				if err := t.writeTx(tx); err != nil {
					return err
				}
				sentBytes += int64(len(tx))

				// Wait for the matching CheckTx result before proceeding. Results can
				// arrive out-of-order across goroutines/WS messages, so we match by hash.
				wantHash := normalizeTxHash(txHashFromBytes(tx))
				deadline := time.NewTimer(connSendTimeout)

				var r checkTxResult
				if pr, ok := pending[wantHash]; ok {
					r = pr
					delete(pending, wantHash)
				} else {
					for {
						select {
						case got := <-t.checkTxResults:
							got.hash = normalizeTxHash(got.hash)
							if got.hash == "" {
								// Ignore malformed.
								continue
							}
							if got.hash == wantHash {
								r = got
								goto matched
							}
							// Buffer for later.
							if len(pending) < 1024 {
								pending[got.hash] = got
							}
						case <-deadline.C:
							return fmt.Errorf("timed out waiting for CheckTx result for tx %s", wantHash)
						}
					}
				}
			matched:
				deadline.Stop()

				if r.code == 0 {
					// Apply deferred client-side effects only on successful CheckTx.
					if onAccept != nil {
						onAccept()
					}
					// Only advance after successful CheckTx.
					// Note: sequence 0 is a valid on-chain starting sequence.
					if scr != nil {
						scr.CommitSequence(seq + 1)
					} else if sce != nil {
						sce.CommitSequence(seq + 1)
					} else {
						sc.CommitSequence(seq + 1)
					}
				} else {
					// Allow the client to apply safe cleanup on specific rejections
					// (e.g. cancel order already gone).
					if onReject != nil {
						onReject(r.code, r.codespace, r.log)
					}
					if shouldRecoverSequence(r.code, r.log) {
						// For code 32, prefer the "expected" sequence returned by CheckTx
						// (mempool state) and apply it immediately to minimize signing with
						// a bad local value.
						if r.code == 32 {
							if exp, ok := parseSequenceMismatchExpected(r.log); ok {
								t.sequenceRecoveryTarget.Store(exp)
								if rto, ok := t.client.(SequenceRecovererTo); ok {
									_ = rto.RecoverSequenceTo(exp)
									t.sequenceRecoveryRequested.Store(false)
									continue
								}
							} else {
								t.sequenceRecoveryTarget.Store(0)
							}
						} else {
							t.sequenceRecoveryTarget.Store(0)
						}
						t.sequenceRecoveryRequested.Store(true)
					}
				}
			}
			return nil
		}
	}

	for ; sent < toSend; sent++ {
		// If we observed a sequence mismatch in CheckTx, refresh account metadata
		// before generating further transactions. Do this in the send loop so we
		// don't race with GenerateTx() state.
		if t.sequenceRecoveryRequested.Load() {
			// Prefer recovery to the exact mempool "expected" sequence when available.
			if r, ok := t.client.(SequenceRecovererTo); ok {
				exp := t.sequenceRecoveryTarget.Load()
				if exp > 0 {
					t.logger.Info("Recovering account sequence after CheckTx mismatch", "next", exp)
					if err := r.RecoverSequenceTo(exp); err != nil {
						return fmt.Errorf("failed to recover account sequence to %d after CheckTx mismatch: %w", exp, err)
					}
				} else if r2, ok := t.client.(SequenceRecoverer); ok {
					t.logger.Info("Recovering account sequence after CheckTx mismatch")
					if err := r2.RecoverSequence(); err != nil {
						return fmt.Errorf("failed to recover account sequence after CheckTx mismatch: %w", err)
					}
				}
			} else if r, ok := t.client.(SequenceRecoverer); ok {
				t.logger.Info("Recovering account sequence after CheckTx mismatch")
				if err := r.RecoverSequence(); err != nil {
					return fmt.Errorf("failed to recover account sequence after CheckTx mismatch: %w", err)
				}
			} else {
				t.logger.Info("Client does not support sequence recovery; continuing despite CheckTx mismatch")
			}
			t.sequenceRecoveryRequested.Store(false)
		}

		tx, err := t.client.GenerateTx()
		if err != nil {
			return err
		}
		if err := t.writeTx(tx); err != nil {
			return err
		}
		sentBytes += int64(len(tx))
	}
	return nil
}

// parseSequenceMismatchExpected extracts the "expected" sequence value from the
// standard Cosmos SDK sequence mismatch log string, e.g.:
// "account sequence mismatch, expected 1373, got 1323: incorrect account sequence".
func parseSequenceMismatchExpected(log string) (uint64, bool) {
	const prefix = "account sequence mismatch, expected "
	i := strings.Index(log, prefix)
	if i < 0 {
		return 0, false
	}
	rest := log[i+len(prefix):]
	// rest should start with digits, then ", got".
	var exp uint64
	if _, err := fmt.Sscanf(rest, "%d, got", &exp); err != nil {
		return 0, false
	}
	if exp == 0 {
		return 0, false
	}
	return exp, true
}

func (t *Transactor) trackStartTime() {
	t.statsMtx.Lock()
	t.startTime = time.Now()
	t.txRate = 0.0
	t.statsMtx.Unlock()
}

func (t *Transactor) trackSentTxs(count int, byteCount int64) {
	t.statsMtx.Lock()
	defer t.statsMtx.Unlock()

	t.txCount += count
	t.txBytes += byteCount
	elapsed := time.Since(t.startTime).Seconds()
	if elapsed > 0 {
		t.txRate = float64(t.txCount) / elapsed
	} else {
		t.txRate = 0
	}
}

func (t *Transactor) sendPing() error {
	_ = t.conn.SetWriteDeadline(time.Now().Add(connSendTimeout))
	return t.conn.WriteMessage(websocket.PingMessage, []byte{})
}

func (t *Transactor) reportProgress() {
	txCount := t.GetTxCount()
	txRate := t.GetTxRate()
	txBytes := t.GetTxBytes()
	t.logger.Debug("Statistics", "txCount", txCount, "txRate", fmt.Sprintf("%.3f txs/sec", txRate))

	t.progressCallbackMtx.RLock()
	defer t.progressCallbackMtx.RUnlock()
	if t.progressCallback != nil {
		t.progressCallback(t.progressCallbackID, txCount, txBytes)
	}
}

func (t *Transactor) getProgressCallbackInterval() time.Duration {
	t.progressCallbackMtx.RLock()
	defer t.progressCallbackMtx.RUnlock()
	return t.progressCallbackInterval
}

func (t *Transactor) close() {
	// try to cleanly shut down the connection
	_ = t.conn.SetWriteDeadline(time.Now().Add(connSendTimeout))
	err := t.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil {
		t.logger.Error("Failed to write close message", "err", err)
	} else {
		t.logger.Debug("Wrote close message to remote endpoint")
	}
}
