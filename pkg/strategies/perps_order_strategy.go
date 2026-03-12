package strategies

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	assettypes "github.com/1119-Labs/perpx-chain/protocol/x/assets/types"
	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	sendingtypes "github.com/1119-Labs/perpx-chain/protocol/x/sending/types"
	satypes "github.com/1119-Labs/perpx-chain/protocol/x/subaccounts/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// OrderTracker is an interface for tracking orders that can be canceled.
// This abstraction allows the strategy to work with different order tracking
// implementations while keeping the strategy stateless.
type OrderTracker interface {
	// GetRandomOrder returns a random tracked order, or false if none exist.
	GetRandomOrder() (TrackedOrder, bool)

	// GetRandomOrdersForMarket returns up to maxCount random orders for the given market.
	GetRandomOrdersForMarket(clobPairID uint32, maxCount int) []TrackedOrder

	// TrackOrder adds an order to tracking.
	TrackOrder(order TrackedOrder)

	// RemoveOrder removes an order from tracking.
	RemoveOrder(order TrackedOrder)

	// RemoveOrders removes multiple orders from tracking.
	RemoveOrders(orders []TrackedOrder)

	// HasOrders returns true if there are any tracked orders.
	HasOrders() bool
}

// TrackedOrder represents a minimal order that can be tracked for cancellation.
type TrackedOrder struct {
	OrderID    clobtypes.OrderId
	ClobPairID uint32
	ClientID   uint32
	OrderFlags uint32 // Store order flags to determine cancel type
}

// PositionTracker is an interface for tracking positions.
// This allows the strategy to generate position-aware orders (e.g., close orders).
type PositionTracker interface {
	// GetPosition returns the current position for a CLOB pair, or nil if none exists.
	GetPosition(clobPairID uint32) *TrackedPosition
}

// TrackedPosition represents a position for a CLOB pair.
type TrackedPosition struct {
	ClobPairID uint32
	Side       clobtypes.Order_Side // BUY = long, SELL = short
	Size       uint64               // Position size in quantums
	EntryPrice uint64               // Average entry price in subticks
}

// PerpsOrderStrategyConfig is a minimal configuration surface for constructing
// a PerpsOrderStrategy. It is intended to be populated by client factories.
type PerpsOrderStrategyConfig struct {
	ChainID string
	Denom   string

	// Markets describes the CLOB markets this worker may trade.
	Markets []PerpsMarketConfig

	// ScenarioName selects a built-in scenario preset (e.g. "simple_perps").
	ScenarioName string

	// Optional override for leverage range; if zero-valued, the preset's
	// leverage range is used.
	MinLeverageOverride float64
	MaxLeverageOverride float64

	// Optional override for action weights; if all fields are zero, the
	// preset's default weights are used.
	ActionWeightsOverride PerpsActionWeights
}

// PerpsOrderStrategy encapsulates immutable configuration for generating
// perps-related CLOB messages. It holds no worker-shared mutable state; any
// worker-local order tracking should live in the client.
type PerpsOrderStrategy struct {
	chainID string
	denom   string

	scenario PerpsScenarioConfig

	// rng is used for randomized parameter selection. Each strategy instance
	// owns its own RNG to avoid global contention.
	rng *rand.Rand
}

// OrderTrackingEffects captures the order-tracker mutations implied by a message.
// In "strict" mode, callers should apply these effects only after the tx is
// accepted (e.g. CheckTx code == 0) to avoid canceling/tracking orders that
// never actually existed on-chain.
type OrderTrackingEffects struct {
	Track      *TrackedOrder
	Remove     *TrackedOrder
	RemoveMany []TrackedOrder
}

// NewPerpsOrderStrategy constructs a new PerpsOrderStrategy from the provided
// configuration and scenario preset.
func NewPerpsOrderStrategy(cfg PerpsOrderStrategyConfig) (*PerpsOrderStrategy, error) {
	if cfg.ChainID == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}
	if cfg.Denom == "" {
		return nil, fmt.Errorf("denom cannot be empty")
	}
	if len(cfg.Markets) == 0 {
		return nil, fmt.Errorf("at least one perps market must be specified")
	}

	scenarioName := cfg.ScenarioName
	if scenarioName == "" {
		scenarioName = ScenarioSimplePerps
	}

	scenario, err := NewPerpsScenarioFromPreset(scenarioName, cfg.Markets)
	if err != nil {
		return nil, fmt.Errorf("failed to build perps scenario: %w", err)
	}

	// Apply leverage overrides if provided.
	if cfg.MinLeverageOverride != 0 || cfg.MaxLeverageOverride != 0 {
		scenario.MinLeverage = cfg.MinLeverageOverride
		scenario.MaxLeverage = cfg.MaxLeverageOverride
	}

	// Apply action weight overrides if any field is non-zero.
	override := cfg.ActionWeightsOverride
	if override.Place != 0 || override.Cancel != 0 || override.Amend != 0 || override.Close != 0 || override.Noop != 0 {
		scenario.Actions = override
	}

	if err := scenario.Validate(); err != nil {
		return nil, err
	}

	return &PerpsOrderStrategy{
		chainID:  cfg.ChainID,
		denom:    cfg.Denom,
		scenario: *scenario,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

// ChainID returns the chain ID used for signing.
func (s *PerpsOrderStrategy) ChainID() string {
	return s.chainID
}

// Denom returns the fee denomination.
func (s *PerpsOrderStrategy) Denom() string {
	return s.denom
}

// Scenario returns the scenario configuration.
func (s *PerpsOrderStrategy) Scenario() *PerpsScenarioConfig {
	return &s.scenario
}

// CreateMsgForAction creates a perps-related CLOB message for a specific action.
// This is a thin wrapper around createMsgForAction that allows callers to
// explicitly select the action while keeping all message-building logic
// centralized in the strategy.
func (s *PerpsOrderStrategy) CreateMsgForAction(
	action PerpsAction,
	fromAddr string,
	subaccountNumber uint32,
	orderTracker OrderTracker,
	positionTracker PositionTracker,
	nextClientID *uint32,
) (sdk.Msg, error) {
	return s.createMsgForAction(action, fromAddr, subaccountNumber, orderTracker, positionTracker, nextClientID)
}

// CreateMsgForActionWithEffects creates a message and returns the order-tracking
// effects that should be applied on tx acceptance.
func (s *PerpsOrderStrategy) CreateMsgForActionWithEffects(
	action PerpsAction,
	fromAddr string,
	subaccountNumber uint32,
	orderTracker OrderTracker,
	positionTracker PositionTracker,
	nextClientID *uint32,
) (sdk.Msg, *OrderTrackingEffects, error) {
	return s.createMsgForActionWithEffects(action, fromAddr, subaccountNumber, orderTracker, positionTracker, nextClientID)
}

// CreateMsg creates a perps-related CLOB message from the given address.
// It samples an action from the configured scenario and builds the
// corresponding message type. The orderTracker is used for cancel/amend actions
// and will be updated for place actions. The positionTracker is used for
// position-aware orders (e.g., close orders).
func (s *PerpsOrderStrategy) CreateMsg(fromAddr string, subaccountNumber uint32, orderTracker OrderTracker, positionTracker PositionTracker, nextClientID *uint32) (sdk.Msg, error) {
	// Validate the bech32 address early for clearer errors.
	if _, err := sdk.AccAddressFromBech32(fromAddr); err != nil {
		return nil, fmt.Errorf("invalid from address: %w", err)
	}

	action := s.scenario.SampleAction(s.rng)
	return s.createMsgForAction(action, fromAddr, subaccountNumber, orderTracker, positionTracker, nextClientID)
}

// CreateMsgWithEffects creates a message and returns the order-tracking effects
// that should be applied on tx acceptance.
func (s *PerpsOrderStrategy) CreateMsgWithEffects(fromAddr string, subaccountNumber uint32, orderTracker OrderTracker, positionTracker PositionTracker, nextClientID *uint32) (sdk.Msg, *OrderTrackingEffects, error) {
	// Validate the bech32 address early for clearer errors.
	if _, err := sdk.AccAddressFromBech32(fromAddr); err != nil {
		return nil, nil, fmt.Errorf("invalid from address: %w", err)
	}

	action := s.scenario.SampleAction(s.rng)
	return s.createMsgForActionWithEffects(action, fromAddr, subaccountNumber, orderTracker, positionTracker, nextClientID)
}

// createMsgForAction builds a specific message type for the given action.
// It is kept package-private to make unit testing easier while keeping the
// public surface minimal.
func (s *PerpsOrderStrategy) createMsgForAction(
	action PerpsAction,
	fromAddr string,
	subaccountNumber uint32,
	orderTracker OrderTracker,
	positionTracker PositionTracker,
	nextClientID *uint32,
) (sdk.Msg, error) {
	msg, eff, err := s.createMsgForActionWithEffects(action, fromAddr, subaccountNumber, orderTracker, positionTracker, nextClientID)
	if err != nil {
		return nil, err
	}
	// Preserve existing (non-strict) behavior for callers using CreateMsg():
	// apply effects immediately.
	if orderTracker != nil && eff != nil {
		if eff.Track != nil {
			orderTracker.TrackOrder(*eff.Track)
		}
		if eff.Remove != nil {
			orderTracker.RemoveOrder(*eff.Remove)
		}
		if len(eff.RemoveMany) > 0 {
			orderTracker.RemoveOrders(eff.RemoveMany)
		}
	}
	return msg, nil
}

func (s *PerpsOrderStrategy) createMsgForActionWithEffects(
	action PerpsAction,
	fromAddr string,
	subaccountNumber uint32,
	orderTracker OrderTracker,
	positionTracker PositionTracker,
	nextClientID *uint32,
) (sdk.Msg, *OrderTrackingEffects, error) {
	switch action {
	case PerpsActionPlace:
		msg, trackedOrder, err := s.buildPlaceOrderMsg(fromAddr, subaccountNumber, false, false, 0, nextClientID)
		if err != nil {
			return nil, nil, err
		}
		eff := &OrderTrackingEffects{}
		if trackedOrder != nil {
			eff.Track = trackedOrder
		}
		return msg, eff, nil
	case PerpsActionCancel:
		// Try batch cancel first if configured and possible
		if s.scenario.BatchCancelProbability > 0 && s.rng.Float64() < s.scenario.BatchCancelProbability {
			if orderTracker != nil && orderTracker.HasOrders() {
				msg, canceledOrders, ok := s.buildBatchCancelMsg(fromAddr, subaccountNumber, orderTracker)
				if ok {
					eff := &OrderTrackingEffects{}
					if len(canceledOrders) > 0 {
						eff.RemoveMany = canceledOrders
					}
					return msg, eff, nil
				}
				// Fall through to single cancel if batch cancel failed
			}
		}

		// Single cancel
		if orderTracker != nil {
			order, ok := orderTracker.GetRandomOrder()
			if ok {
				msg, err := s.buildCancelOrderMsg(fromAddr, subaccountNumber, order)
				if err != nil {
					return nil, nil, err
				}
				eff := &OrderTrackingEffects{Remove: &order}
				return msg, eff, nil
			}
		}
		// No tracked orders - fall back to placing a new order
		msg, trackedOrder, err := s.buildPlaceOrderMsg(fromAddr, subaccountNumber, true, false, 0, nextClientID)
		if err != nil {
			return nil, nil, err
		}
		eff := &OrderTrackingEffects{}
		if trackedOrder != nil {
			eff.Track = trackedOrder
		}
		return msg, eff, nil
	case PerpsActionAmend:
		// Model "amend" as a new maker-style placement.
		msg, trackedOrder, err := s.buildPlaceOrderMsg(fromAddr, subaccountNumber, false, false, 0, nextClientID)
		if err != nil {
			return nil, nil, err
		}
		eff := &OrderTrackingEffects{}
		if trackedOrder != nil {
			eff.Track = trackedOrder
		}
		return msg, eff, nil
	case PerpsActionClose:
		// Close order: check if position exists and generate reduce-only order
		clobPairID := s.scenario.SampleClobPairID(s.rng)
		var pos *TrackedPosition
		if positionTracker != nil {
			pos = positionTracker.GetPosition(clobPairID)
		}

		var closeSize uint64
		if pos != nil {
			// Position exists: generate close order with partial close logic
			// Randomly choose to close 25%, 50%, 75%, or 100% of position
			closePercentages := []float64{0.25, 0.50, 0.75, 1.0}
			closePct := closePercentages[s.rng.Intn(len(closePercentages))]
			closeSize = uint64(float64(pos.Size) * closePct)
			if closeSize == 0 {
				closeSize = 1 // Ensure at least 1 quantum
			}
		}

		msg, trackedOrder, err := s.buildPlaceOrderMsg(fromAddr, subaccountNumber, true, true, closeSize, nextClientID)
		if err != nil {
			return nil, nil, err
		}
		eff := &OrderTrackingEffects{}
		if trackedOrder != nil {
			eff.Track = trackedOrder
		}
		return msg, eff, nil
	case PerpsActionNoop:
		msg, err := s.buildNoopMsg(fromAddr)
		if err != nil {
			return nil, nil, err
		}
		return msg, &OrderTrackingEffects{}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported perps action: %v", action)
	}
}

// buildPlaceOrderMsg builds a MsgPlaceOrder with all advanced features supported.
// Returns the message, a tracked order (if tracking is needed), and any error.
func (s *PerpsOrderStrategy) buildPlaceOrderMsg(
	fromAddr string,
	subaccountNumber uint32,
	isTaker bool,
	isClose bool,
	closeSize uint64,
	nextClientID *uint32,
) (sdk.Msg, *TrackedOrder, error) {
	// Sample market and parameters
	clobPairID := s.scenario.SampleClobPairID(s.rng)
	market := s.findMarket(clobPairID)
	if market == nil {
		return nil, nil, fmt.Errorf("market not found for clobPairID %d", clobPairID)
	}

	quantums := s.scenario.RandomQuantityQuantums(s.rng, *market)
	ctx := context.Background()
	subticks := s.scenario.RandomSubticks(ctx, s.rng, *market)

	// For stateful orders we avoid IOC/FOK time-in-force, since long-term orders
	// cannot require immediate execution per clob module validation rules.
	timeInForce := clobtypes.Order_TIME_IN_FORCE_UNSPECIFIED

	// Determine side and reduce-only flag
	var side clobtypes.Order_Side
	reduceOnly := false

	// For close orders, we previously set reduce-only, but long-term/stateful
	// orders with reduce-only and non-immediate time-in-force are rejected by
	// the chain. To keep validation happy without block-height-aware short-term
	// orders, we avoid setting reduce-only here.
	if isClose {
		reduceOnly = false
		if closeSize > 0 {
			quantums = closeSize
		}
		// Side will be determined by position if available, otherwise random
	}

	// Random side (will be overridden for close orders if position exists)
	side = clobtypes.Order_SIDE_BUY
	if s.rng.Intn(2) == 1 {
		side = clobtypes.Order_SIDE_SELL
	}

	// Generate client ID
	clientID := *nextClientID
	*nextClientID++

	// Calculate GoodTilBlockTime for stateful orders (1 hour from now).
	// We avoid using GoodTilBlock (block height) because the strategy does not
	// have access to the current block height, and incorrect heights cause ABCI
	// validation failures against ShortBlockWindow.
	goodTilBlockTime := uint32(time.Now().Unix() + 3600)

	// Determine order type based on scenario configuration
	var msg sdk.Msg
	var orderFlags uint32

	// Check for TWAP order first (mutually exclusive with other types)
	if s.scenario.TWAPOrderProbability > 0 && s.rng.Float64() < s.scenario.TWAPOrderProbability {
		orderID := clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{
				Owner:  fromAddr,
				Number: subaccountNumber,
			},
			ClientId:   clientID,
			OrderFlags: clobtypes.OrderIdFlags_Twap,
			ClobPairId: clobPairID,
		}

		// TWAP orders are stateful, so they must use GoodTilBlockTime, and they must
		// include TwapParameters to pass ValidateBasic().
		interval := s.scenario.TWAPIntervalSeconds
		if interval == 0 {
			interval = 60
		}
		numIntervals := s.scenario.TWAPNumIntervals
		if numIntervals == 0 {
			numIntervals = 5
		}
		duration := interval * numIntervals

		order := clobtypes.Order{
			OrderId:  orderID,
			Side:     side,
			Quantums: quantums,
			Subticks: subticks,
			GoodTilOneof: &clobtypes.Order_GoodTilBlockTime{
				GoodTilBlockTime: goodTilBlockTime,
			},
			TimeInForce:    timeInForce,
			ReduceOnly:     reduceOnly,
			ClientMetadata: 0,
			ConditionType:  clobtypes.Order_CONDITION_TYPE_UNSPECIFIED,
			TwapParameters: &clobtypes.TwapParameters{
				Duration:       duration,
				Interval:       interval,
				PriceTolerance: 0,
			},
		}

		msg = clobtypes.NewMsgPlaceOrder(order)
		orderFlags = clobtypes.OrderIdFlags_Twap
	} else {
		// Check for conditional order
		isConditional := false
		var conditionType clobtypes.Order_ConditionType
		var triggerPrice uint64

		if market.ConditionalOrderProbability > 0 && s.rng.Float64() < market.ConditionalOrderProbability {
			isConditional = true
			conditionTypes := market.ConditionalOrderTypes
			if len(conditionTypes) == 0 {
				conditionTypes = []clobtypes.Order_ConditionType{
					clobtypes.Order_CONDITION_TYPE_STOP_LOSS,
					clobtypes.Order_CONDITION_TYPE_TAKE_PROFIT,
				}
			}
			conditionType = conditionTypes[s.rng.Intn(len(conditionTypes))]

			// Calculate trigger price
			priceOffset := int64(subticks) * 5 / 100 // 5% offset
			if conditionType == clobtypes.Order_CONDITION_TYPE_STOP_LOSS {
				if side == clobtypes.Order_SIDE_SELL {
					triggerPrice = subticks - uint64(priceOffset)
				} else {
					triggerPrice = subticks + uint64(priceOffset)
				}
			} else { // TAKE_PROFIT
				if side == clobtypes.Order_SIDE_SELL {
					triggerPrice = subticks + uint64(priceOffset)
				} else {
					triggerPrice = subticks - uint64(priceOffset)
				}
			}
			// Ensure trigger price is within bounds
			if triggerPrice < market.MinSubticks {
				triggerPrice = market.MinSubticks
			}
			if triggerPrice > market.MaxSubticks {
				triggerPrice = market.MaxSubticks
			}
		}

		// Order flags are mutually exclusive. To avoid incorrect GoodTilBlock (height)
		// usage, we treat all non-conditional orders as long-term (stateful) so they
		// use GoodTilBlockTime instead of GoodTilBlock.
		if isConditional {
			orderFlags = clobtypes.OrderIdFlags_Conditional
		} else {
			orderFlags = clobtypes.OrderIdFlags_LongTerm
		}

		orderID := clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{
				Owner:  fromAddr,
				Number: subaccountNumber,
			},
			ClientId:   clientID,
			OrderFlags: orderFlags,
			ClobPairId: clobPairID,
		}

		order := clobtypes.Order{
			OrderId:        orderID,
			Side:           side,
			Quantums:       quantums,
			Subticks:       subticks,
			GoodTilOneof:   &clobtypes.Order_GoodTilBlockTime{GoodTilBlockTime: goodTilBlockTime},
			TimeInForce:    timeInForce,
			ReduceOnly:     reduceOnly,
			ClientMetadata: 0,
		}

		if isConditional {
			order.ConditionType = conditionType
			order.ConditionalOrderTriggerSubticks = triggerPrice
		} else {
			order.ConditionType = clobtypes.Order_CONDITION_TYPE_UNSPECIFIED
			order.ConditionalOrderTriggerSubticks = 0
		}

		msg = clobtypes.NewMsgPlaceOrder(order)
	}

	// Validate the message
	if placeMsg, ok := msg.(*clobtypes.MsgPlaceOrder); ok {
		if err := placeMsg.ValidateBasic(); err != nil {
			return nil, nil, fmt.Errorf("invalid MsgPlaceOrder: %w", err)
		}
	}

	// Create tracked order
	trackedOrder := &TrackedOrder{
		OrderID: clobtypes.OrderId{
			SubaccountId: satypes.SubaccountId{
				Owner:  fromAddr,
				Number: subaccountNumber,
			},
			ClientId:   clientID,
			OrderFlags: orderFlags,
			ClobPairId: clobPairID,
		},
		ClobPairID: clobPairID,
		ClientID:   clientID,
		OrderFlags: orderFlags,
	}

	return msg, trackedOrder, nil
}

// findMarket finds the market configuration for a given CLOB pair ID.
func (s *PerpsOrderStrategy) findMarket(clobPairID uint32) *PerpsMarketConfig {
	for i := range s.scenario.Markets {
		if s.scenario.Markets[i].ClobPairID == clobPairID {
			return &s.scenario.Markets[i]
		}
	}
	return nil
}

// buildCancelOrderMsg builds a MsgCancelOrder for a tracked order.
func (s *PerpsOrderStrategy) buildCancelOrderMsg(fromAddr string, subaccountNumber uint32, order TrackedOrder) (sdk.Msg, error) {
	// Use GoodTilBlockTime for stateful cancels (1 hour from now). We avoid
	// GoodTilBlock (height) because the strategy does not know the current block
	// height; incorrect heights cause ABCI ShortBlockWindow validation failures.
	goodTilBlockTime := uint32(time.Now().Unix() + 3600)

	var msg *clobtypes.MsgCancelOrder
	if order.OrderFlags != clobtypes.OrderIdFlags_ShortTerm {
		// Any stateful order requires stateful cancel.
		msg = clobtypes.NewMsgCancelOrderStateful(order.OrderID, goodTilBlockTime)
	} else {
		// Short-term order (should not be generated by this strategy anymore).
		// As a safety net, fall back to a near-term GoodTilBlockTime via a stateful
		// cancel to avoid invalid GoodTilBlock heights.
		msg = clobtypes.NewMsgCancelOrderStateful(order.OrderID, goodTilBlockTime)
	}

	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("invalid MsgCancelOrder: %w", err)
	}
	return msg, nil
}

// buildBatchCancelMsg builds a MsgBatchCancel for multiple orders on the same market.
// Returns the message, the canceled orders, and true if successful.
func (s *PerpsOrderStrategy) buildBatchCancelMsg(fromAddr string, subaccountNumber uint32, orderTracker OrderTracker) (sdk.Msg, []TrackedOrder, bool) {
	// Batch cancel for short-term orders requires a valid GoodTilBlock (height),
	// which the strategy cannot compute without block height. To avoid generating
	// invalid GoodTilBlock values that fail ABCI validation, we currently disable
	// batch cancels and fall back to single cancels.
	return nil, nil, false
}

// buildDepositMsg builds a MsgDepositToSubaccount for noop actions.
func (s *PerpsOrderStrategy) buildDepositMsg(fromAddr string, subaccountNumber uint32) sdk.Msg {
	quantums := uint64(1000)

	subaccountID := satypes.SubaccountId{
		Owner:  fromAddr,
		Number: subaccountNumber,
	}

	return sendingtypes.NewMsgDepositToSubaccount(
		fromAddr,
		subaccountID,
		assettypes.AssetUsdc.Id,
		quantums,
	)
}

func (s *PerpsOrderStrategy) buildNoopMsg(fromAddr string) (sdk.Msg, error) {
	// Self-send 1 aperpx. This is effectively a no-op on balances but is a valid
	// message on any chain with the native fee denom seeded to workers.
	//
	// NOTE: denom is intentionally hard-coded to avoid depending on fee denom
	// (LOADTEST_DENOM), which may be set to USDC/IBC in some environments.
	fromAcc, err := sdk.AccAddressFromBech32(fromAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid from address %q: %w", fromAddr, err)
	}
	coins := sdk.NewCoins(sdk.NewInt64Coin("aperpx", 1))
	return banktypes.NewMsgSend(fromAcc, fromAcc, coins), nil
}
