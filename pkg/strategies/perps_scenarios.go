package strategies

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
)

const (
	// Default tick/step sizing when PerpsScenarioConfig.SubticksPerTick/StepBaseQuantums are 0.
	// Override via loadtest config (YAML or env → cfg.Perps → factory sets on scenario).
	defaultSubticksPerTick  = uint64(100000)
	defaultStepBaseQuantums = uint64(1000000)
)

// PerpsAction represents a high-level action that a perps scenario can take.
// The concrete message type(s) produced for each action are defined by
// PerpsOrderStrategy.CreateMsg() (e.g. place, cancel, amend, close).
type PerpsAction int

const (
	PerpsActionPlace PerpsAction = iota
	PerpsActionCancel
	PerpsActionAmend
	PerpsActionClose
	PerpsActionNoop
)

// String returns a string representation of the PerpsAction.
func (a PerpsAction) String() string {
	switch a {
	case PerpsActionPlace:
		return "place"
	case PerpsActionCancel:
		return "cancel"
	case PerpsActionAmend:
		return "amend"
	case PerpsActionClose:
		return "close"
	case PerpsActionNoop:
		return "noop"
	default:
		return "unknown"
	}
}

// PerpsActionWeights controls the relative probability of each action.
// All fields must be non-negative and at least one must be > 0 for sampling.
type PerpsActionWeights struct {
	Place  int
	Cancel int
	Amend  int
	Close  int
	Noop   int
}

// total returns the sum of all action weights.
func (w PerpsActionWeights) total() int {
	return w.Place + w.Cancel + w.Amend + w.Close + w.Noop
}

// validate ensures the weights are usable for sampling.
func (w PerpsActionWeights) validate() error {
	if w.Place < 0 || w.Cancel < 0 || w.Amend < 0 || w.Close < 0 || w.Noop < 0 {
		return fmt.Errorf("action weights must be non-negative")
	}
	if w.total() <= 0 {
		return fmt.Errorf("at least one action weight must be > 0")
	}
	return nil
}

// PerpsMarketConfig describes how to generate orders for a single CLOB market.
type PerpsMarketConfig struct {
	// ClobPairID is the numeric identifier for the CLOB pair on-chain.
	ClobPairID uint32
	// Symbol is an optional human-readable identifier (e.g. "ETH-USD").
	Symbol string

	// MinQuantityQuantums and MaxQuantityQuantums control the range of order
	// sizes in base quantums. Values must be > 0 and Min <= Max.
	MinQuantityQuantums uint64
	MaxQuantityQuantums uint64

	// MinSubticks and MaxSubticks control the range of order prices in
	// "subticks". Values must be > 0 and Min <= Max.
	// When UseMidPrice is true, these act as bounds for the calculated price.
	MinSubticks uint64
	MaxSubticks uint64

	// PriceOffsetBps is the price offset in basis points (e.g., -50 to +50 bps around mid-price).
	// Only used when UseMidPrice is true. 100 bps = 1%.
	PriceOffsetBps int

	// UseMidPrice indicates whether to use mid-price calculation or random range.
	UseMidPrice bool

	// ConditionalOrderProbability is the probability (0.0 to 1.0) that an order will be conditional.
	ConditionalOrderProbability float64

	// ConditionalOrderTypes is the list of conditional order types that can be used.
	// If empty, defaults to all available types.
	ConditionalOrderTypes []clobtypes.Order_ConditionType
}

// validate ensures the market configuration is self-consistent.
func (m PerpsMarketConfig) validate() error {
	if m.ClobPairID == 0 {
		return fmt.Errorf("clob pair ID must be > 0")
	}
	if m.MinQuantityQuantums == 0 || m.MaxQuantityQuantums == 0 {
		return fmt.Errorf("quantity quantums must be > 0")
	}
	if m.MinQuantityQuantums > m.MaxQuantityQuantums {
		return fmt.Errorf("min quantity quantums must be <= max quantity quantums")
	}
	if m.MinSubticks == 0 || m.MaxSubticks == 0 {
		return fmt.Errorf("subticks must be > 0")
	}
	if m.MinSubticks > m.MaxSubticks {
		return fmt.Errorf("min subticks must be <= max subticks")
	}
	return nil
}

// PerpsScenarioConfig captures all high-level parameters for a given perps
// scenario (e.g. simple flow, maker/taker, stress/mixed).
type PerpsScenarioConfig struct {
	// Name is the scenario identifier (e.g. "simple_perps", "maker_taker").
	Name string

	// Markets is the list of CLOB markets that this scenario can trade on.
	Markets []PerpsMarketConfig

	// MinLeverage and MaxLeverage describe the leverage range for the scenario.
	// These are currently informational for the strategy but are kept to allow
	// more realistic behavior in future extensions.
	MinLeverage float64
	MaxLeverage float64

	// Actions controls the relative probability of each action type.
	Actions PerpsActionWeights

	// MaxTrackedOrders controls how many outstanding orders a single worker
	// will keep in memory for potential cancels/amends. This is used by the
	// perps client to bound memory usage when tracking open orders.
	MaxTrackedOrders int

	// MidPriceProvider is an optional provider for fetching current market data.
	// If nil, mid-price features are disabled and random price ranges are used.
	MidPriceProvider MarketDataProvider

	// BatchCancelProbability controls the probability (0.0 to 1.0) that a cancel
	// action will use batch cancel instead of single cancel. When batch cancel
	// is attempted but not possible (e.g., not enough orders on same market),
	// the client falls back to single cancel.
	BatchCancelProbability float64

	// LongTermOrderProbability is the probability (0.0 to 1.0) that an order will be long-term.
	// Long-term orders use OrderFlags: 64 and require stateful cancels.
	LongTermOrderProbability float64

	// TWAPOrderProbability is the probability (0.0 to 1.0) that an order will be a TWAP order.
	TWAPOrderProbability float64

	// TWAPIntervalSeconds is the interval duration in seconds for TWAP orders.
	TWAPIntervalSeconds uint32

	// TWAPNumIntervals is the number of intervals for TWAP orders.
	TWAPNumIntervals uint32

	// SubticksPerTick and StepBaseQuantums align sampling with chain CLOB config.
	// If 0, package defaults are used (from env or defaultSubticksPerTick/defaultStepBaseQuantums).
	SubticksPerTick  uint64
	StepBaseQuantums uint64
}

// Validate ensures the scenario configuration is self-consistent.
func (c PerpsScenarioConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("scenario name cannot be empty")
	}
	if len(c.Markets) == 0 {
		return fmt.Errorf("scenario %q must have at least one market", c.Name)
	}
	for i, m := range c.Markets {
		if err := m.validate(); err != nil {
			return fmt.Errorf("scenario %q market[%d] invalid: %w", c.Name, i, err)
		}
	}
	if c.MinLeverage < 0 || c.MaxLeverage < 0 {
		return fmt.Errorf("leverage cannot be negative")
	}
	if c.MinLeverage > c.MaxLeverage {
		return fmt.Errorf("min leverage must be <= max leverage")
	}
	if err := c.Actions.validate(); err != nil {
		return fmt.Errorf("scenario %q has invalid action weights: %w", c.Name, err)
	}
	if c.MaxTrackedOrders <= 0 {
		return fmt.Errorf("scenario %q must have MaxTrackedOrders > 0", c.Name)
	}
	if c.BatchCancelProbability < 0.0 || c.BatchCancelProbability > 1.0 {
		return fmt.Errorf("scenario %q BatchCancelProbability must be between 0.0 and 1.0", c.Name)
	}
	if c.LongTermOrderProbability < 0.0 || c.LongTermOrderProbability > 1.0 {
		return fmt.Errorf("scenario %q LongTermOrderProbability must be between 0.0 and 1.0", c.Name)
	}
	if c.TWAPOrderProbability < 0.0 || c.TWAPOrderProbability > 1.0 {
		return fmt.Errorf("scenario %q TWAPOrderProbability must be between 0.0 and 1.0", c.Name)
	}
	// Validate that conditional order probabilities don't exceed 1.0 when combined
	for i, m := range c.Markets {
		if m.ConditionalOrderProbability < 0.0 || m.ConditionalOrderProbability > 1.0 {
			return fmt.Errorf("scenario %q market[%d] ConditionalOrderProbability must be between 0.0 and 1.0", c.Name, i)
		}
	}
	return nil
}

// SampleAction draws an action according to the configured weights.
// The provided rng must be non-nil.
func (c PerpsScenarioConfig) SampleAction(rng *rand.Rand) PerpsAction {
	total := c.Actions.total()
	// total is guaranteed > 0 by validate().
	n := rng.Intn(total)

	if n < c.Actions.Place {
		return PerpsActionPlace
	}
	n -= c.Actions.Place

	if n < c.Actions.Cancel {
		return PerpsActionCancel
	}
	n -= c.Actions.Cancel

	if n < c.Actions.Amend {
		return PerpsActionAmend
	}
	n -= c.Actions.Amend

	if n < c.Actions.Close {
		return PerpsActionClose
	}

	return PerpsActionNoop
}

// RandomMarket selects a market uniformly at random from the scenario.
// The provided rng must be non-nil and the scenario must have at least one market.
func (c PerpsScenarioConfig) RandomMarket(rng *rand.Rand) PerpsMarketConfig {
	if len(c.Markets) == 0 {
		// This should not happen if Validate is used, but protect against panics.
		return PerpsMarketConfig{}
	}
	idx := rng.Intn(len(c.Markets))
	return c.Markets[idx]
}

// stepBaseQuantums returns the step size for quantity sampling (config or default).
func (c PerpsScenarioConfig) stepBaseQuantums() uint64 {
	if c.StepBaseQuantums > 0 {
		return c.StepBaseQuantums
	}
	return defaultStepBaseQuantums
}

// RandomQuantityQuantums returns a random quantity within the configured range
// for the given market.
func (c PerpsScenarioConfig) RandomQuantityQuantums(rng *rand.Rand, m PerpsMarketConfig) uint64 {
	step := c.stepBaseQuantums()
	if step > 0 {
		// Compute the smallest/largest valid step indices within the configured range.
		minIdx := (m.MinQuantityQuantums + step - 1) / step
		maxIdx := m.MaxQuantityQuantums / step
		if minIdx == 0 {
			minIdx = 1
		}
		if minIdx <= maxIdx {
			// Sample uniformly over valid indices.
			rangeSize := maxIdx - minIdx + 1
			idx := minIdx
			if rangeSize > 1 {
				idx += uint64(rng.Int63n(int64(rangeSize)))
			}
			return idx * step
		}
	}

	// Fallback: if step configuration doesn't fit the range, use simple integer range.
	if m.MinQuantityQuantums == m.MaxQuantityQuantums {
		return m.MinQuantityQuantums
	}
	delta := m.MaxQuantityQuantums - m.MinQuantityQuantums
	return m.MinQuantityQuantums + uint64(rng.Int63n(int64(delta)+1))
}

// subticksPerTick returns the tick size for price sampling (config or default).
func (c PerpsScenarioConfig) subticksPerTick() uint64 {
	if c.SubticksPerTick > 0 {
		return c.SubticksPerTick
	}
	return defaultSubticksPerTick
}

// RandomSubticks returns a random price (in subticks) within the configured
// range for the given market. If UseMidPrice is true and a provider is available,
// it calculates the price based on mid-price with offset; otherwise uses random range.
func (c PerpsScenarioConfig) RandomSubticks(ctx context.Context, rng *rand.Rand, m PerpsMarketConfig) uint64 {
	tick := c.subticksPerTick()

	// If mid-price is enabled and provider is available, use it
	if m.UseMidPrice && c.MidPriceProvider != nil {
		midPrice, err := c.MidPriceProvider.GetMidPrice(ctx, m.ClobPairID)
		if err == nil && midPrice > 0 {
			// Apply offset in basis points
			// offset = midPrice * (PriceOffsetBps / 10000)
			var offset int64
			if m.PriceOffsetBps != 0 {
				offset = int64(midPrice) * int64(m.PriceOffsetBps) / 10000
			}

			// Add some randomness around the offset (within ±10 bps)
			randomOffset := rng.Int63n(21) - 10 // -10 to +10 bps
			randomOffsetAmount := int64(midPrice) * randomOffset / 10000

			price := int64(midPrice) + offset + randomOffsetAmount

			// Ensure price stays within bounds
			if price < int64(m.MinSubticks) {
				price = int64(m.MinSubticks)
			}
			if price > int64(m.MaxSubticks) {
				price = int64(m.MaxSubticks)
			}
			if price < 0 {
				price = int64(m.MinSubticks)
			}

			// Snap to the nearest valid tick within [MinSubticks, MaxSubticks].
			if tick > 0 {
				// If the tick size doesn't fit within the configured range (e.g. tick > MaxSubticks),
				// don't force the price up to tick; just return the clamped price.
				maxTickIdx := int64(m.MaxSubticks) / int64(tick)
				if maxTickIdx <= 0 {
					return uint64(price)
				}

				if price < int64(tick) {
					price = int64(tick)
				}
				idx := int64(price) / int64(tick)
				if idx <= 0 {
					idx = 1
				}
				snapped := idx * int64(tick)
				if snapped < int64(m.MinSubticks) {
					snapped = int64(m.MinSubticks)
				}
				if maxTickIdx > 0 && snapped > maxTickIdx*int64(tick) {
					snapped = maxTickIdx * int64(tick)
				}
				return uint64(snapped)
			}

			return uint64(price)
		}
		// If provider fails, fall back to random range
	}

	// Default: use random range, quantized to the configured tick size when possible.
	if tick > 0 {
		minIdx := (m.MinSubticks + tick - 1) / tick
		maxIdx := m.MaxSubticks / tick
		// If tick doesn't fit in range, fall back to non-quantized sampling.
		if maxIdx == 0 {
			goto fallback
		}
		if minIdx == 0 {
			minIdx = 1
		}
		if minIdx <= maxIdx {
			rangeSize := maxIdx - minIdx + 1
			idx := minIdx
			if rangeSize > 1 {
				idx += uint64(rng.Int63n(int64(rangeSize)))
			}
			return idx * tick
		}
	}

	// Fallback if tick configuration doesn't fit the range.
fallback:
	if m.MinSubticks == m.MaxSubticks {
		return m.MinSubticks
	}
	delta := m.MaxSubticks - m.MinSubticks
	return m.MinSubticks + uint64(rng.Int63n(int64(delta)+1))
}

// SampleClobPairID returns a random CLOB pair ID from the configured markets.
// If no markets are configured (which should not happen if Validate is used),
// it returns 0.
func (c PerpsScenarioConfig) SampleClobPairID(rng *rand.Rand) uint32 {
	mkt := c.RandomMarket(rng)
	return mkt.ClobPairID
}

// SampleQuantums returns a random order size (in quantums) using a randomly
// selected market from the scenario.
func (c PerpsScenarioConfig) SampleQuantums(rng *rand.Rand) uint64 {
	mkt := c.RandomMarket(rng)
	return c.RandomQuantityQuantums(rng, mkt)
}

// SampleSubticks returns a random price (in subticks) using a randomly
// selected market from the scenario.
func (c PerpsScenarioConfig) SampleSubticks(ctx context.Context, rng *rand.Rand) uint64 {
	mkt := c.RandomMarket(rng)
	return c.RandomSubticks(ctx, rng, mkt)
}

// Scenario preset identifiers used by factories / configuration.
const (
	ScenarioSimplePerps   = "simple_perps"
	ScenarioMakerTaker    = "maker_taker"
	ScenarioStressMixed   = "stress_mixed"
	ScenarioAdvancedPerps = "advanced_perps"
)

// NewPerpsScenarioFromPreset constructs a PerpsScenarioConfig using a simple
// built-in preset and the provided markets. The caller is responsible for
// passing at least one valid market.
func NewPerpsScenarioFromPreset(name string, markets []PerpsMarketConfig) (*PerpsScenarioConfig, error) {
	if len(markets) == 0 {
		return nil, fmt.Errorf("perps scenario %q requires at least one market", name)
	}

	cfg := &PerpsScenarioConfig{
		Name:             name,
		Markets:          markets,
		MaxTrackedOrders: 1024, // sensible default for in-memory order tracking
	}

	switch name {
	case ScenarioSimplePerps:
		// Mostly simple open/close market-like flow on a small number of markets.
		cfg.MinLeverage = 1.0
		cfg.MaxLeverage = 5.0
		cfg.Actions = PerpsActionWeights{
			Place:  80,
			Cancel: 5,
			Amend:  5,
			Close:  10,
			Noop:   0,
		}
		cfg.BatchCancelProbability = 0.0 // Keep simple, no batch cancels
	case ScenarioMakerTaker:
		// Emphasize maker vs taker behavior. Post-only style placements and
		// cancels dominate, with some taker-style closes.
		cfg.MinLeverage = 1.0
		cfg.MaxLeverage = 10.0
		// Note: We disable cancel actions by default to avoid frequent
		// "Stateful order does not exist" errors when the load test attempts
		// to cancel locally tracked orders that were never accepted or have
		// already left the book on-chain. If you need explicit cancel traffic,
		// you can re‑enable it via LOADTEST_PERPS_ACTION_WEIGHTS.
		cfg.Actions = PerpsActionWeights{
			Place: 60, // mix of maker/taker placements
			// Cancel: 20, // disabled by default – see note above
			Cancel: 0,
			Amend:  15,
			Close:  25,
			Noop:   0,
		}
		// With cancels disabled by default, keep batch cancel probability at 0.
		cfg.BatchCancelProbability = 0.0
	case ScenarioStressMixed:
		// Heavier mix of everything to generate maximum matching engine load.
		cfg.MinLeverage = 1.0
		cfg.MaxLeverage = 15.0
		cfg.Actions = PerpsActionWeights{
			Place:  50,
			Cancel: 20,
			Amend:  15,
			Close:  10,
			Noop:   5,
		}
		cfg.BatchCancelProbability = 0.15 // 15% of cancels use batch cancel
	case ScenarioAdvancedPerps:
		// Advanced scenario with mix of order types for comprehensive testing
		cfg.MinLeverage = 1.0
		cfg.MaxLeverage = 10.0
		cfg.Actions = PerpsActionWeights{
			Place:  50,
			Cancel: 20,
			Amend:  15,
			Close:  10,
			Noop:   5,
		}
		cfg.BatchCancelProbability = 0.20   // 20% of cancels use batch cancel
		cfg.LongTermOrderProbability = 0.30 // 30% of orders are long-term
		cfg.TWAPOrderProbability = 0.10     // 10% of orders are TWAP
		cfg.TWAPIntervalSeconds = 60        // 60 second intervals
		cfg.TWAPNumIntervals = 5            // 5 intervals
		// Set conditional order probability for all markets
		for i := range cfg.Markets {
			cfg.Markets[i].ConditionalOrderProbability = 0.15 // 15% conditional orders
			// Default conditional order types if not set
			if len(cfg.Markets[i].ConditionalOrderTypes) == 0 {
				cfg.Markets[i].ConditionalOrderTypes = []clobtypes.Order_ConditionType{
					clobtypes.Order_CONDITION_TYPE_STOP_LOSS,
					clobtypes.Order_CONDITION_TYPE_TAKE_PROFIT,
				}
			}
		}
	default:
		return nil, fmt.Errorf("unknown perps scenario preset %q", name)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// DefaultPerpsScenarioConfigFromInputs builds a PerpsScenarioConfig from explicit
// inputs. Used by the loadtest client factory with cfg.Perps (Viper: CLI, YAML, env, defaults).
//
// Any "zero" values (empty strings / 0 ints / nil pointers) are treated as "not set".
func DefaultPerpsScenarioConfigFromInputs(
	scenarioName string,
	marketsSpec string,
	actionWeights string,
	maxTrackedOrders int,
	minLeverageStr string,
	maxLeverageStr string,
	useMidPrice *bool,
	priceOffsetBps int,
	marketDataURL string,
	marketDataCacheTTLSeconds int,
	batchCancelPct int,
	longTermPct int,
	conditionalPct int,
	twapPct int,
	twapIntervalSeconds int,
	twapNumIntervals int,
) (*PerpsScenarioConfig, error) {
	name := strings.TrimSpace(scenarioName)
	if name == "" {
		name = ScenarioSimplePerps
	}

	// Markets: reuse the same parsing semantics as LOADTEST_PERPS_MARKETS.
	var markets []PerpsMarketConfig
	if strings.TrimSpace(marketsSpec) == "" {
		markets = []PerpsMarketConfig{
			{
				ClobPairID:          1,
				Symbol:              "ETH-PERP",
				MinQuantityQuantums: 1_000_000,
				MaxQuantityQuantums: 10_000_000,
				MinSubticks:         100_000,
				MaxSubticks:         200_000,
			},
		}
	} else {
		parsed, err := parseMarketSpecsWithContext(marketsSpec, "inputs")
		if err != nil {
			return nil, err
		}
		markets = parsed
	}

	cfg, err := NewPerpsScenarioFromPreset(name, markets)
	if err != nil {
		return nil, err
	}

	// Action weights override.
	if strings.TrimSpace(actionWeights) != "" {
		weights, err := parseActionWeightsWithContext(actionWeights, "actionWeights")
		if err != nil {
			return nil, err
		}
		cfg.Actions = weights
	}

	if maxTrackedOrders > 0 {
		cfg.MaxTrackedOrders = maxTrackedOrders
	}

	if strings.TrimSpace(minLeverageStr) != "" {
		if v, err := strconv.ParseFloat(strings.TrimSpace(minLeverageStr), 64); err == nil && v > 0 {
			cfg.MinLeverage = v
		} else if err != nil {
			return nil, fmt.Errorf("invalid minLeverage %q: %w", minLeverageStr, err)
		}
	}
	if strings.TrimSpace(maxLeverageStr) != "" {
		if v, err := strconv.ParseFloat(strings.TrimSpace(maxLeverageStr), 64); err == nil && v > 0 {
			cfg.MaxLeverage = v
		} else if err != nil {
			return nil, fmt.Errorf("invalid maxLeverage %q: %w", maxLeverageStr, err)
		}
	}

	// Market-data provider + per-market mid-price toggles.
	if strings.TrimSpace(marketDataURL) != "" {
		ttl := 3 * time.Second
		if marketDataCacheTTLSeconds > 0 {
			ttl = time.Duration(marketDataCacheTTLSeconds) * time.Second
		}
		cfg.MidPriceProvider = NewRESTMarketDataProvider(strings.TrimSpace(marketDataURL), ttl)
	}
	if useMidPrice != nil {
		for i := range cfg.Markets {
			cfg.Markets[i].UseMidPrice = *useMidPrice
			if *useMidPrice && priceOffsetBps != 0 {
				cfg.Markets[i].PriceOffsetBps = priceOffsetBps
			}
		}
	}

	// Percent knobs -> probabilities.
	if batchCancelPct > 0 {
		cfg.BatchCancelProbability = float64(batchCancelPct) / 100.0
	}
	if longTermPct > 0 {
		cfg.LongTermOrderProbability = float64(longTermPct) / 100.0
	}
	if conditionalPct > 0 {
		for i := range cfg.Markets {
			cfg.Markets[i].ConditionalOrderProbability = float64(conditionalPct) / 100.0
		}
	}
	if twapPct > 0 {
		cfg.TWAPOrderProbability = float64(twapPct) / 100.0
	}
	if twapIntervalSeconds > 0 {
		cfg.TWAPIntervalSeconds = uint32(twapIntervalSeconds)
	}
	if twapNumIntervals > 0 {
		cfg.TWAPNumIntervals = uint32(twapNumIntervals)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseMarketSpecsWithContext shares the core parsing logic between env- and
// YAML-driven constructors while allowing source-specific error messages to
// remain stable.
func parseMarketSpecsWithContext(spec string, context string) ([]PerpsMarketConfig, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	var markets []PerpsMarketConfig

	for _, raw := range parts {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		fields := strings.Split(p, ":")
		if len(fields) != 4 {
			switch context {
			case "env":
				return nil, fmt.Errorf("invalid market spec %q in LOADTEST_PERPS_MARKETS", p)
			case "inputs":
				return nil, fmt.Errorf("invalid market spec %q (expected clobPairID:symbol:minQty-maxQty:minSubticks-maxSubticks)", p)
			default:
				return nil, fmt.Errorf("invalid market spec %q", p)
			}
		}

		clobPairID, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid clobPairID %q: %w", fields[0], err)
		}
		symbol := fields[1]

		minMaxQty := strings.Split(fields[2], "-")
		if len(minMaxQty) != 2 {
			return nil, fmt.Errorf("invalid quantity range %q in market %q", fields[2], p)
		}
		minQty, err := strconv.ParseUint(minMaxQty[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid min quantity %q: %w", minMaxQty[0], err)
		}
		maxQty, err := strconv.ParseUint(minMaxQty[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid max quantity %q: %w", minMaxQty[1], err)
		}

		minMaxSubticks := strings.Split(fields[3], "-")
		if len(minMaxSubticks) != 2 {
			return nil, fmt.Errorf("invalid subticks range %q in market %q", fields[3], p)
		}
		minSubticks, err := strconv.ParseUint(minMaxSubticks[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid min subticks %q: %w", minMaxSubticks[0], err)
		}
		maxSubticks, err := strconv.ParseUint(minMaxSubticks[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid max subticks %q: %w", minMaxSubticks[1], err)
		}

		market := PerpsMarketConfig{
			ClobPairID:          uint32(clobPairID),
			Symbol:              symbol,
			MinQuantityQuantums: minQty,
			MaxQuantityQuantums: maxQty,
			MinSubticks:         minSubticks,
			MaxSubticks:         maxSubticks,
		}
		if err := market.validate(); err != nil {
			return nil, err
		}
		markets = append(markets, market)
	}
	return markets, nil
}

// parseActionWeightsWithContext shares the core parsing logic between
// env- and YAML-driven constructors while allowing source-specific error
// messages to remain stable.
func parseActionWeightsWithContext(spec string, context string) (PerpsActionWeights, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return PerpsActionWeights{}, fmt.Errorf("%s must not be empty", context)
	}
	parts := strings.Split(spec, ",")
	if len(parts) != 5 {
		switch context {
		case "LOADTEST_PERPS_ACTION_WEIGHTS":
			return PerpsActionWeights{}, fmt.Errorf("LOADTEST_PERPS_ACTION_WEIGHTS must have 5 comma-separated integers: place,cancel,amend,close,noop")
		default:
			return PerpsActionWeights{}, fmt.Errorf("actionWeights must have 5 comma-separated integers: place,cancel,amend,close,noop")
		}
	}

	parseInt := func(s string) (int, error) {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return 0, err
		}
		return v, nil
	}

	var (
		w   PerpsActionWeights
		err error
	)

	if w.Place, err = parseInt(parts[0]); err != nil {
		return PerpsActionWeights{}, fmt.Errorf("invalid place weight %q: %w", parts[0], err)
	}
	if w.Cancel, err = parseInt(parts[1]); err != nil {
		return PerpsActionWeights{}, fmt.Errorf("invalid cancel weight %q: %w", parts[1], err)
	}
	if w.Amend, err = parseInt(parts[2]); err != nil {
		return PerpsActionWeights{}, fmt.Errorf("invalid amend weight %q: %w", parts[2], err)
	}
	if w.Close, err = parseInt(parts[3]); err != nil {
		return PerpsActionWeights{}, fmt.Errorf("invalid close weight %q: %w", parts[3], err)
	}
	if w.Noop, err = parseInt(parts[4]); err != nil {
		return PerpsActionWeights{}, fmt.Errorf("invalid noop weight %q: %w", parts[4], err)
	}
	return w, nil
}

// NewRandForWorker constructs a per-worker RNG. baseSeed comes from config (Viper);
// 0 means time-based, non-zero gives deterministic per-worker streams.
func NewRandForWorker(workerID int, baseSeed int64) *rand.Rand {
	if baseSeed == 0 {
		baseSeed = time.Now().UnixNano()
	}
	seed := baseSeed + int64(workerID)*1_000_003
	return rand.New(rand.NewSource(seed))
}
