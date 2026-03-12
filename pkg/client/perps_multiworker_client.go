package client

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// MultiWorkerPerpsClient composes multiple PerpxPerpsClient instances for a
// single websocket connection and rotates signers per tick according to the
// worker-sharding rule.
//
// It implements the sequenced tx interface used by the transactor in sync mode
// by delegating to the selected underlying worker and only committing that
// worker's sequence after successful CheckTx.
type MultiWorkerPerpsClient struct {
	config loadtest.Config

	// workers holds the per-connection slice of underlying perps clients. Its
	// length is G = WorkersPerConnection.
	workers []*PerpxPerpsClient

	// baseWorkerID is the global worker index of workers[0]; workers[i] uses
	// baseWorkerID+i for deterministic account derivation.
	baseWorkerID int

	// ratePerConn is R = cfg.Rate (tx per sendPeriod per connection).
	ratePerConn int

	// txCounter is a monotonically increasing counter of generated txs for this
	// connection. It is used to derive the logical (tick, offsetWithinTick) and
	// thus the worker index for each tx:
	//   slot = txCounter
	//   block = slot / R
	//   offsetWithinBlock = slot % R
	//   k = (block * 2R) % G
	//   workerOffset = (k + offsetWithinBlock) % G
	txCounter uint64

	mu            sync.Mutex
	lastWorkerIdx int
}

// NewMultiWorkerPerpsClient constructs a MultiWorkerPerpsClient for a single
// websocket connection, given the base worker index and number of workers per
// connection.
func NewMultiWorkerPerpsClient(
	cfg loadtest.Config,
	strategy *strategies.PerpsOrderStrategy,
	baseWorkerID int,
	workersPerConnection int,
) (*MultiWorkerPerpsClient, error) {
	if workersPerConnection <= 0 {
		// Fallback: treat as single-worker connection.
		workersPerConnection = 1
	}

	workers := make([]*PerpxPerpsClient, workersPerConnection)
	for i := 0; i < workersPerConnection; i++ {
		wid := baseWorkerID + i
		c, err := NewPerpxPerpsClient(cfg, strategy, wid)
		if err != nil {
			return nil, err
		}
		workers[i] = c
	}

	m := &MultiWorkerPerpsClient{
		config:        cfg,
		workers:       workers,
		baseWorkerID:  baseWorkerID,
		ratePerConn:   cfg.Rate,
		txCounter:     0,
		lastWorkerIdx: 0,
	}
	if m.ratePerConn <= 0 {
		m.ratePerConn = 1
	}
	return m, nil
}

// selectWorkerIndex returns the index into m.workers for the next tx based on
// the k-advance scheduling rule:
//
//	G = len(workers), R = ratePerConn
//	For slot n (0-based):
//	  block = n / R
//	  offset = n % R
//	  k = (block * 2R) % G
//	  workerOffset = (k + offset) % G
func (m *MultiWorkerPerpsClient) selectWorkerIndex() int {
	G := len(m.workers)
	if G == 0 {
		return 0
	}
	R := m.ratePerConn
	if R <= 0 {
		R = 1
	}
	slot := atomic.AddUint64(&m.txCounter, 1) - 1
	block := int(slot / uint64(R))
	offset := int(slot % uint64(R))
	k := (block * 2 * R) % G
	workerOffset := (k + offset) % G
	return workerOffset
}

// GenerateTx satisfies loadtest.Client but is not used in sync mode when the
// sequenced interfaces are available. We delegate to the underlying worker
// selected by the scheduler.
func (m *MultiWorkerPerpsClient) GenerateTx() ([]byte, error) {
	idx := m.selectWorkerIndex()
	w := m.workers[idx]
	m.mu.Lock()
	m.lastWorkerIdx = idx
	m.mu.Unlock()
	tx, err := w.GenerateTx()
	if m.config.Debug.LogWorkerIDs {
		globalID := m.baseWorkerID + idx
		fmt.Printf("perps tx from worker %d (conn=%d, endpoint=%d)\n", globalID, m.config.TransactorIndex, m.config.EndpointOrdinal)
	}
	return tx, err
}

// GenerateTxWithSequenceAndEffectsAndRejectHook delegates to the selected
// underlying worker's sequenced interface so the transactor can gate sequence
// advancement on successful CheckTx.
func (m *MultiWorkerPerpsClient) GenerateTxWithSequenceAndEffectsAndRejectHook() ([]byte, uint64, func(), func(code int, codespace, log string), error) {
	idx := m.selectWorkerIndex()
	w := m.workers[idx]
	tx, seq, onAccept, onReject, err := w.GenerateTxWithSequenceAndEffectsAndRejectHook()
	if err != nil {
		return nil, 0, nil, nil, err
	}
	m.mu.Lock()
	m.lastWorkerIdx = idx
	m.mu.Unlock()
	if m.config.Debug.LogWorkerIDs {
		globalID := m.baseWorkerID + idx
		fmt.Printf("perps sequenced tx from worker %d (seq=%d, conn=%d, endpoint=%d)\n", globalID, seq, m.config.TransactorIndex, m.config.EndpointOrdinal)
	}
	return tx, seq, onAccept, onReject, nil
}

// CommitSequence advances the sequence for the worker that produced the last
// sequenced tx. The transactor calls this only after successful CheckTx.
func (m *MultiWorkerPerpsClient) CommitSequence(next uint64) {
	if next == 0 {
		return
	}
	m.mu.Lock()
	idx := m.lastWorkerIdx
	m.mu.Unlock()
	if idx < 0 || idx >= len(m.workers) {
		return
	}
	m.workers[idx].CommitSequence(next)
}

// RecoverSequence delegates sequence recovery to the worker that produced the
// last tx.
func (m *MultiWorkerPerpsClient) RecoverSequence() error {
	m.mu.Lock()
	idx := m.lastWorkerIdx
	m.mu.Unlock()
	if idx < 0 || idx >= len(m.workers) {
		return nil
	}
	if r, ok := interface{}(m.workers[idx]).(loadtest.SequenceRecoverer); ok {
		return r.RecoverSequence()
	}
	return nil
}

// RecoverSequenceTo delegates sequence recovery to a specific target sequence
// for the worker that produced the last tx.
func (m *MultiWorkerPerpsClient) RecoverSequenceTo(next uint64) error {
	if next == 0 {
		return nil
	}
	m.mu.Lock()
	idx := m.lastWorkerIdx
	m.mu.Unlock()
	if idx < 0 || idx >= len(m.workers) {
		return nil
	}
	if r, ok := interface{}(m.workers[idx]).(loadtest.SequenceRecovererTo); ok {
		return r.RecoverSequenceTo(next)
	}
	return nil
}
