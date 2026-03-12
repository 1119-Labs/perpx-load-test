package client

import (
	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// trackedPosition represents a position for a CLOB pair.
// This matches strategies.TrackedPosition for compatibility.
type trackedPosition struct {
	ClobPairID uint32
	Side       clobtypes.Order_Side // BUY = long, SELL = short
	Size       uint64               // Position size in quantums
	EntryPrice uint64               // Average entry price in subticks
}

// toStrategiesTrackedPosition converts a client trackedPosition to strategies.TrackedPosition.
func (p *trackedPosition) toStrategiesTrackedPosition() *strategies.TrackedPosition {
	if p == nil {
		return nil
	}
	return &strategies.TrackedPosition{
		ClobPairID: p.ClobPairID,
		Side:       p.Side,
		Size:       p.Size,
		EntryPrice: p.EntryPrice,
	}
}

// updatePosition updates the tracked position for a CLOB pair based on an order execution.
// This is approximate client-side tracking and does not reconcile with chain state.
func (c *PerpxPerpsClient) updatePosition(clobPairID uint32, side clobtypes.Order_Side, size uint64, price uint64) {
	c.posMtx.Lock()
	defer c.posMtx.Unlock()

	pos, exists := c.positions[clobPairID]
	if !exists {
		// New position
		c.positions[clobPairID] = &trackedPosition{
			ClobPairID: clobPairID,
			Side:       side,
			Size:       size,
			EntryPrice: price,
		}
		return
	}

	// Update existing position
	if pos.Side == side {
		// Same side: increase position size, update average entry price
		totalValue := pos.Size*pos.EntryPrice + size*price
		pos.Size += size
		if pos.Size > 0 {
			pos.EntryPrice = totalValue / pos.Size
		}
	} else {
		// Opposite side: reduce or flip position
		if size >= pos.Size {
			// Position is closed or flipped
			remainingSize := size - pos.Size
			if remainingSize > 0 {
				// Position flipped
				pos.Side = side
				pos.Size = remainingSize
				pos.EntryPrice = price
			} else {
				// Position closed
				delete(c.positions, clobPairID)
			}
		} else {
			// Partial close
			pos.Size -= size
		}
	}
}

// getPosition returns the current tracked position for a CLOB pair.
func (c *PerpxPerpsClient) getPosition(clobPairID uint32) *trackedPosition {
	c.posMtx.RLock()
	defer c.posMtx.RUnlock()
	return c.positions[clobPairID]
}

// hasPosition returns true if there is a tracked position for the CLOB pair.
func (c *PerpxPerpsClient) hasPosition(clobPairID uint32) bool {
	c.posMtx.RLock()
	defer c.posMtx.RUnlock()
	_, exists := c.positions[clobPairID]
	return exists
}
