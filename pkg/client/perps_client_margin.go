package client

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	assettypes "github.com/1119-Labs/perpx-chain/protocol/x/assets/types"
	sendingtypes "github.com/1119-Labs/perpx-chain/protocol/x/sending/types"
	satypes "github.com/1119-Labs/perpx-chain/protocol/x/subaccounts/types"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// MarginCheckConfig controls margin checking behavior.
type MarginCheckConfig struct {
	Enabled        bool
	MinMarginRatio float64 // Minimum margin ratio required
	OnInsufficient string  // "skip", "deposit", "close"
}

// checkMarginSufficient checks if there is sufficient margin for an order.
// This is a simplified check - in production, you'd query the actual margin from the chain.
func (c *PerpxPerpsClient) CheckMarginSufficient(clobPairID uint32, quantums, subticks uint64) bool {
	if !c.marginConfig.Enabled {
		return true // Margin check disabled
	}

	// Simplified margin check: assume we have sufficient margin if position tracking is not enabled
	// In a real implementation, this would query the subaccount's margin from the chain
	// For now, we'll use a heuristic based on tracked positions
	pos := c.getPosition(clobPairID)
	if pos == nil {
		// No position, assume sufficient margin for new position
		return true
	}

	// If we have a position, check if we're trying to increase it significantly
	// This is a simplified check - real margin calculation would be more complex
	orderValue := quantums * subticks
	positionValue := pos.Size * pos.EntryPrice

	// If order value is much larger than position value, might be insufficient margin
	// This is a heuristic - real implementation would query actual margin ratio
	if orderValue > positionValue*10 {
		return false
	}

	return true
}

// handleInsufficientMargin handles insufficient margin according to configuration.
func (c *PerpxPerpsClient) HandleInsufficientMargin() (sdk.Msg, error) {
	switch c.marginConfig.OnInsufficient {
	case "deposit":
		// Attempt a margin deposit. This is intentionally separate from the
		// scenario "noop" action (which is a true noop bank self-send) so that
		// callers can choose between "deposit" and "skip" without requiring USDC
		// balances for skip ticks.
		return c.buildMarginDepositMsg(), nil
	case "close":
		// Try to generate a close-order message via the strategy so that all
		// perps order construction remains centralized in PerpsOrderStrategy.
		if c.strategy != nil {
			msg, err := c.strategy.CreateMsgForAction(
				strategies.PerpsActionClose,
				c.addr.String(),
				c.subaccountNumber,
				c, // OrderTracker
				c, // PositionTracker
				&c.nextCID,
			)
			if err == nil && msg != nil {
				return msg, nil
			}
		}
		// Fall through to skip if no close message could be generated
		fallthrough
	case "skip":
		fallthrough
	default:
		// Skip this tick - return a true noop message via the strategy.
		if c.strategy == nil {
			return nil, fmt.Errorf("strategy is nil; cannot build noop message")
		}
		msg, err := c.strategy.CreateMsgForAction(
			strategies.PerpsActionNoop,
			c.addr.String(),
			c.subaccountNumber,
			c, // OrderTracker (unused for noop)
			c, // PositionTracker (unused for noop)
			&c.nextCID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build noop message via strategy: %w", err)
		}
		return msg, nil
	}
}

func (c *PerpxPerpsClient) buildMarginDepositMsg() sdk.Msg {
	// Small USDC margin deposit used for margin-recovery paths.
	// This is intentionally not used for scenario noops.
	const quantums = uint64(1000)
	subaccountID := satypes.SubaccountId{
		Owner:  c.addr.String(),
		Number: c.subaccountNumber,
	}
	return sendingtypes.NewMsgDepositToSubaccount(
		c.addr.String(),
		subaccountID,
		assettypes.AssetUsdc.Id,
		quantums,
	)
}
