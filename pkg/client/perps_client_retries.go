package client

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// RetryConfig controls retry behavior for failed transactions.
type RetryConfig struct {
	MaxRetries      int
	RetryDelay      time.Duration
	RetryableErrors []string // Error codes/messages that should be retried
}

// isRetryableError checks if an error is retryable based on the retry configuration.
func (c *PerpxPerpsClient) isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	for _, retryable := range c.retryConfig.RetryableErrors {
		if strings.Contains(errStr, strings.ToLower(retryable)) {
			return true
		}
	}
	return false
}

// recoverSequence re-queries the account sequence from the chain and resets the local counter.
// This function must be called without holding accountQueryMtx to avoid deadlock.
func (c *PerpxPerpsClient) recoverSequence() error {
	// Force re-query by clearing the queried flag
	// We need to do this carefully to avoid deadlock
	c.accountQueryMtx.Lock()
	wasQueried := c.accountQueried
	c.accountQueried = false
	c.accountQueryMtx.Unlock()

	// If it wasn't queried, no need to recover
	if !wasQueried {
		return nil
	}

	// Now re-query (this will acquire the lock internally)
	if err := c.ensureAccountQueried(); err != nil {
		c.errorMetrics.IncrementAccountQueryFailures()
		c.errorMetrics.IncrementErrorCount("sequence_recovery_failed")
		return fmt.Errorf("failed to recover sequence: %w", err)
	}

	c.errorMetrics.IncrementSequenceMismatches()
	c.errorMetrics.IncrementErrorCount("sequence_recovered")
	return nil
}

// RecoverSequence implements loadtest.SequenceRecoverer.
func (c *PerpxPerpsClient) RecoverSequence() error {
	return c.recoverSequence()
}

// RecoverSequenceTo sets the local sequence counter to the provided value.
// This is used when CheckTx returns the mempool "expected" sequence, which can
// be ahead of the committed sequence exposed via REST when there are pending
// transactions.
func (c *PerpxPerpsClient) RecoverSequenceTo(next uint64) error {
	if next == 0 {
		return fmt.Errorf("invalid target sequence: %d", next)
	}
	atomic.StoreUint64(&c.sequence, next)
	return nil
}

// GenerateTxWithSequence generates a transaction using the client's current
// sequence WITHOUT incrementing it. This allows callers (e.g. the transactor
// using broadcast_tx_sync) to only advance the sequence after a successful
// CheckTx.
//
// If timestamp nonces are enabled, the sequence is not meaningful; callers
// should fall back to GenerateTx().
func (c *PerpxPerpsClient) GenerateTxWithSequence() ([]byte, uint64, error) {
	if c.useTimestampNonce {
		tx, err := c.GenerateTx()
		return tx, 0, err
	}

	// Ensure we have initialized accountNum/sequence from REST before taking a
	// snapshot of the sequence to sign with; otherwise we can incorrectly sign
	// with seq=0 on the first tx.
	if err := c.ensureAccountQueried(); err != nil {
		return nil, 0, err
	}

	seq := atomic.LoadUint64(&c.sequence)
	tx, err := c.generateTxOnceWithSequence(seq)
	return tx, seq, err
}

// GenerateTxWithSequenceAndEffects generates a tx using the current sequence
// without incrementing it, and returns a callback that should be invoked only
// after successful CheckTx (code == 0) to apply order-tracking mutations.
func (c *PerpxPerpsClient) GenerateTxWithSequenceAndEffects() ([]byte, uint64, func(), error) {
	if c.useTimestampNonce {
		tx, err := c.GenerateTx()
		return tx, 0, func() {}, err
	}
	if err := c.ensureAccountQueried(); err != nil {
		return nil, 0, nil, err
	}
	seq := atomic.LoadUint64(&c.sequence)
	tx, apply, err := c.generateTxOnceWithSequenceAndEffects(seq)
	return tx, seq, apply, err
}

// GenerateTxWithSequenceAndEffectsAndRejectHook is like GenerateTxWithSequenceAndEffects,
// but also returns an onReject hook that can be used by the transactor to apply
// safe, removal-only cleanup on specific CheckTx rejections (e.g. canceling an
// already-gone order).
func (c *PerpxPerpsClient) GenerateTxWithSequenceAndEffectsAndRejectHook() ([]byte, uint64, func(), func(code int, codespace, log string), error) {
	if c.useTimestampNonce {
		tx, err := c.GenerateTx()
		return tx, 0, func() {}, func(int, string, string) {}, err
	}
	if err := c.ensureAccountQueried(); err != nil {
		return nil, 0, nil, nil, err
	}
	seq := atomic.LoadUint64(&c.sequence)
	tx, onSuccess, cleanup, err := c.generateTxOnceWithSequenceAndEffectsAndCleanup(seq)
	if err != nil {
		return nil, 0, nil, nil, err
	}

	onReject := func(code int, codespace, log string) {
		// If a cancel is rejected because the order doesn't exist, we should
		// remove it from local tracking to avoid repeatedly canceling it.
		//
		// We only run removal-only cleanup (never tracking) to keep safety.
		if strings.EqualFold(strings.TrimSpace(codespace), "clob") && code == 3006 {
			ll := strings.ToLower(log)
			if strings.Contains(ll, "order id to cancel does not exist") ||
				strings.Contains(ll, "stateful order does not exist") ||
				strings.Contains(ll, "does not exist") {
				if cleanup != nil {
					cleanup()
				}
			}
		}
	}
	return tx, seq, onSuccess, onReject, nil
}

// CommitSequence advances the client's local sequence to next, but never moves
// it backwards.
func (c *PerpxPerpsClient) CommitSequence(next uint64) {
	if next == 0 {
		return
	}
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

// MaxRetries returns the maximum number of retries allowed by this client's
// retry policy.
func (c *PerpxPerpsClient) MaxRetries() int {
	return c.retryConfig.MaxRetries
}

// InitialDelay returns the initial retry delay used by this client's retry
// policy.
func (c *PerpxPerpsClient) InitialDelay() time.Duration {
	return c.retryConfig.RetryDelay
}

// IsRetryableError exposes the internal retryable-error check for components
// that depend on the RetryPolicy interface.
func (c *PerpxPerpsClient) IsRetryableError(err error) bool {
	return c.isRetryableError(err)
}
