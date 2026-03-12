package client

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// TxBuilder is a small internal interface that captures the ability to turn a
// signed SDK transaction into raw bytes for broadcasting.
type TxBuilder interface {
	BuildTxBytes(msg sdk.Msg, seq uint64) ([]byte, error)
}

// MarginManager is an internal interface that abstracts margin checks and the
// fallback behavior when margin is deemed insufficient.
type MarginManager interface {
	CheckMarginSufficient(clobPairID uint32, quantums, subticks uint64) bool
	HandleInsufficientMargin() (sdk.Msg, error)
}

// PositionTrackerInternal describes the minimal surface the client needs from
// its position-tracking component. It is intentionally narrower than the public
// strategies.PositionTracker.
type PositionTrackerInternal interface {
	getPosition(clobPairID uint32) *trackedPosition
	hasPosition(clobPairID uint32) bool
}

// RetryPolicy encapsulates retry-related configuration and logic for transient
// errors and sequence mismatches.
type RetryPolicy interface {
	MaxRetries() int
	InitialDelay() time.Duration
	IsRetryableError(err error) bool
	RecoverSequence() error
}

// MetricsSink represents the minimal error-metrics surface that callers care
// about for observability and stats export.
type MetricsSink interface {
	GetSnapshot() map[string]interface{}
}

// Ensure PerpxPerpsClient satisfies the internal interfaces we expect. This
// keeps the compiler enforcing the intended structure even before we fully
// decouple the concrete implementation.
var (
	_ MarginManager          = (*PerpxPerpsClient)(nil)
	_ PositionTrackerInternal = (*PerpxPerpsClient)(nil)
	_ RetryPolicy            = (*PerpxPerpsClient)(nil)
	_ MetricsSink            = (*ErrorMetrics)(nil)
)

