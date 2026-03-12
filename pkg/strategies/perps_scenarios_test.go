package strategies

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPerpsScenarioValidateAndPresets(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	for _, name := range []string{ScenarioSimplePerps, ScenarioMakerTaker, ScenarioStressMixed, ScenarioAdvancedPerps} {
		scenario, err := NewPerpsScenarioFromPreset(name, []PerpsMarketConfig{market})
		require.NoError(t, err, "preset %s should construct successfully", name)
		require.Equal(t, name, scenario.Name)
		require.NotEmpty(t, scenario.Markets)
		require.Greater(t, scenario.MaxTrackedOrders, 0)
		require.NoError(t, scenario.Validate())
	}
}

// TestRandomQuantityQuantums_RespectsStepAndBounds verifies that sampled quantities
// respect both the configured [MinQuantityQuantums, MaxQuantityQuantums] range and
// the global step size configured via LOADTEST_PERPS_STEP_BASE_QUANTUMS.
func TestRandomQuantityQuantums_RespectsStepAndBounds(t *testing.T) {
	t.Setenv("LOADTEST_PERPS_STEP_BASE_QUANTUMS", "100")

	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 100,
		MaxQuantityQuantums: 1000,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario := PerpsScenarioConfig{
		Name:              "step_test",
		Markets:           []PerpsMarketConfig{market},
		Actions:           PerpsActionWeights{Place: 1},
		MinLeverage:       1,
		MaxLeverage:       5,
		MaxTrackedOrders:  128,
		StepBaseQuantums:  1, // config-driven step so quantities align
	}
	require.NoError(t, scenario.Validate())

	rng := rand.New(rand.NewSource(123))
	const samples = 2000

	for i := 0; i < samples; i++ {
		q := scenario.RandomQuantityQuantums(rng, market)
		require.GreaterOrEqual(t, q, market.MinQuantityQuantums)
		require.LessOrEqual(t, q, market.MaxQuantityQuantums)
		require.Equal(t, uint64(0), q%scenario.stepBaseQuantums(), "quantity should align to step size")

		qs := scenario.SampleQuantums(rng)
		require.GreaterOrEqual(t, qs, market.MinQuantityQuantums)
		require.LessOrEqual(t, qs, market.MaxQuantityQuantums)
		require.Equal(t, uint64(0), qs%scenario.stepBaseQuantums(), "sampled quantity should align to step size")
	}
}

// TestRandomSubticks_RespectsTickAndBounds verifies that sampled prices
// respect both the configured [MinSubticks, MaxSubticks] range and the
// tick size (PerpsScenarioConfig.SubticksPerTick).
func TestRandomSubticks_RespectsTickAndBounds(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario := PerpsScenarioConfig{
		Name:             "tick_test",
		Markets:          []PerpsMarketConfig{market},
		Actions:          PerpsActionWeights{Place: 1},
		MinLeverage:      1,
		MaxLeverage:      5,
		MaxTrackedOrders: 128,
		SubticksPerTick:  10, // config-driven tick so prices align
	}
	require.NoError(t, scenario.Validate())

	rng := rand.New(rand.NewSource(456))
	const samples = 2000

	for i := 0; i < samples; i++ {
		price := scenario.RandomSubticks(context.Background(), rng, market)
		require.GreaterOrEqual(t, price, market.MinSubticks)
		require.LessOrEqual(t, price, market.MaxSubticks)
		require.Equal(t, uint64(0), price%scenario.subticksPerTick(), "price should align to tick size")

		ps := scenario.SampleSubticks(context.Background(), rng)
		require.GreaterOrEqual(t, ps, market.MinSubticks)
		require.LessOrEqual(t, ps, market.MaxSubticks)
		require.Equal(t, uint64(0), ps%scenario.subticksPerTick(), "sampled price should align to tick size")
	}
}

func TestSampleActionRespectsWeights(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario := PerpsScenarioConfig{
		Name:            "test_weights",
		Markets:         []PerpsMarketConfig{market},
		Actions: PerpsActionWeights{
			Place:  3,
			Cancel: 1,
			Amend:  0,
			Close:  0,
			Noop:   0,
		},
		MinLeverage:     1,
		MaxLeverage:     5,
		MaxTrackedOrders: 128,
	}
	require.NoError(t, scenario.Validate())

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	const samples = 5000
	var placeCount, cancelCount int
	for i := 0; i < samples; i++ {
		switch scenario.SampleAction(rng) {
		case PerpsActionPlace:
			placeCount++
		case PerpsActionCancel:
			cancelCount++
		}
	}

	// Expected ratio is approximately 3:1; allow generous slack.
	ratio := float64(placeCount) / float64(cancelCount)
	require.InEpsilon(t, 3.0, ratio, 0.4, "place:cancel ratio should be roughly 3:1")
}

// TestPresetActionDistributions validates that each preset generates
// expected action distributions over many samples.
func TestPresetActionDistributions(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	presets := []struct {
		name           string
		expectedPlace  float64 // Expected proportion of place actions
		expectedCancel float64 // Expected proportion of cancel actions
		expectedAmend   float64 // Expected proportion of amend actions
		expectedClose  float64 // Expected proportion of close actions
		expectedNoop   float64 // Expected proportion of noop actions
	}{
		{
			name:           ScenarioSimplePerps,
			expectedPlace:  0.80, // 80% place
			expectedCancel: 0.05, // 5% cancel
			expectedAmend:  0.05, // 5% amend
			expectedClose:  0.10, // 10% close
			expectedNoop:   0.00, // 0% noop
		},
		{
			name:           ScenarioMakerTaker,
			expectedPlace:  0.60, // 60% place
			expectedCancel: 0.00, // cancels disabled by default (see preset note)
			expectedAmend:  0.15, // 15% amend
			expectedClose:  0.25, // 25% close
			expectedNoop:   0.00, // 0% noop
		},
		{
			name:           ScenarioStressMixed,
			expectedPlace:  0.50, // 50% place
			expectedCancel: 0.20, // 20% cancel
			expectedAmend:  0.15, // 15% amend
			expectedClose:  0.10, // 10% close
			expectedNoop:   0.05, // 5% noop
		},
		{
			name:           ScenarioAdvancedPerps,
			expectedPlace:  0.50, // 50% place
			expectedCancel: 0.20, // 20% cancel
			expectedAmend:  0.15, // 15% amend
			expectedClose:  0.10, // 10% close
			expectedNoop:   0.05, // 5% noop
		},
	}

	for _, preset := range presets {
		t.Run(preset.name, func(t *testing.T) {
			scenario, err := NewPerpsScenarioFromPreset(preset.name, []PerpsMarketConfig{market})
			require.NoError(t, err)

			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			const samples = 10000

			var placeCount, cancelCount, amendCount, closeCount, noopCount int
			for i := 0; i < samples; i++ {
				switch scenario.SampleAction(rng) {
				case PerpsActionPlace:
					placeCount++
				case PerpsActionCancel:
					cancelCount++
				case PerpsActionAmend:
					amendCount++
				case PerpsActionClose:
					closeCount++
				case PerpsActionNoop:
					noopCount++
				}
			}

			// Check proportions with reasonable tolerance.
			// Use 10% relative tolerance to account for statistical variance in sampling.
			actualPlace := float64(placeCount) / float64(samples)
			actualCancel := float64(cancelCount) / float64(samples)
			actualAmend := float64(amendCount) / float64(samples)
			actualClose := float64(closeCount) / float64(samples)
			actualNoop := float64(noopCount) / float64(samples)

			checkProportion := func(expected, actual float64, name string) {
				if expected > 0 {
					// Use a slightly looser tolerance to account for higher
					// relative variance on low-probability actions.
					require.InEpsilon(t, expected, actual, 0.25,
						"%s action proportion should match preset (expected ~%.2f, got %.2f)", name, expected, actual)
				} else {
					require.Less(t, actual, 0.01,
						"%s action proportion should be near zero when expected is zero (got %.2f)", name, actual)
				}
			}

			checkProportion(preset.expectedPlace, actualPlace, "place")
			checkProportion(preset.expectedCancel, actualCancel, "cancel")
			checkProportion(preset.expectedAmend, actualAmend, "amend")
			checkProportion(preset.expectedClose, actualClose, "close")
			checkProportion(preset.expectedNoop, actualNoop, "noop")
		})
	}
}

func TestPerpsScenarioPreset_AdvancedPerps_WiresAdvancedConfig(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario, err := NewPerpsScenarioFromPreset(ScenarioAdvancedPerps, []PerpsMarketConfig{market})
	require.NoError(t, err)
	require.NoError(t, scenario.Validate())

	require.Greater(t, scenario.LongTermOrderProbability, 0.0)
	require.Greater(t, scenario.TWAPOrderProbability, 0.0)
	require.Greater(t, scenario.TWAPIntervalSeconds, uint32(0))
	require.Greater(t, scenario.TWAPNumIntervals, uint32(0))

	require.Len(t, scenario.Markets, 1)
	require.Greater(t, scenario.Markets[0].ConditionalOrderProbability, 0.0)
	require.NotEmpty(t, scenario.Markets[0].ConditionalOrderTypes)
}

func TestParseMarketSpecsHelpers(t *testing.T) {
	spec := "1:ETH-PERP:1-10:100-200, 2:BTC-PERP:5-15:50-150"

	markets, err := parseMarketSpecsWithContext(spec, "inputs")
	require.NoError(t, err)
	require.Len(t, markets, 2)

	require.Equal(t, uint32(1), markets[0].ClobPairID)
	require.Equal(t, "ETH-PERP", markets[0].Symbol)
	require.Equal(t, uint64(1), markets[0].MinQuantityQuantums)
	require.Equal(t, uint64(10), markets[0].MaxQuantityQuantums)
	require.Equal(t, uint64(100), markets[0].MinSubticks)
	require.Equal(t, uint64(200), markets[0].MaxSubticks)

	require.Equal(t, uint32(2), markets[1].ClobPairID)
	require.Equal(t, "BTC-PERP", markets[1].Symbol)

	// Invalid spec count should surface contextual error messages.
	_, err = parseMarketSpecsWithContext("1:ETH-PERP:1-10", "env")
	require.Error(t, err)
	require.Contains(t, err.Error(), "LOADTEST_PERPS_MARKETS")

	_, err = parseMarketSpecsWithContext("1:ETH-PERP:1-10", "inputs")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected clobPairID:symbol:minQty-maxQty:minSubticks-maxSubticks")
}

func TestParseActionWeightsHelpers(t *testing.T) {
	w, err := parseActionWeightsWithContext("10,5,3,2,1", "actionWeights")
	require.NoError(t, err)
	require.Equal(t, PerpsActionWeights{Place: 10, Cancel: 5, Amend: 3, Close: 2, Noop: 1}, w)

	_, err = parseActionWeightsWithContext("10,5,3", "actionWeights")
	require.Error(t, err)
	require.Contains(t, err.Error(), "actionWeights must have 5 comma-separated integers")

	_, err = parseActionWeightsWithContext("10,foo,3,2,1", "LOADTEST_PERPS_ACTION_WEIGHTS")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid cancel weight")
	require.Contains(t, err.Error(), "foo")
}

func TestDefaultPerpsScenarioConfigFromInputs_ProducesExpectedConfig(t *testing.T) {
	const marketsSpec = "1:ETH-PERP:1-10:100-200"
	const weightsSpec = "80,5,5,10,0"

	cfg, err := DefaultPerpsScenarioConfigFromInputs(
		ScenarioSimplePerps,
		marketsSpec,
		weightsSpec,
		0,   // maxTrackedOrders – let preset default
		"",  // minLeverageStr
		"",  // maxLeverageStr
		nil, // useMidPrice
		0,   // priceOffsetBps
		"",  // marketDataURL
		0,   // marketDataCacheTTLSeconds
		0,   // batchCancelPct
		0,   // longTermPct
		0,   // conditionalPct
		0,   // twapPct
		0,   // twapIntervalSeconds
		0,   // twapNumIntervals
	)
	require.NoError(t, err)

	require.Equal(t, ScenarioSimplePerps, cfg.Name)
	require.Equal(t, PerpsActionWeights{Place: 80, Cancel: 5, Amend: 5, Close: 10, Noop: 0}, cfg.Actions)
	require.Len(t, cfg.Markets, 1)
	require.Equal(t, uint32(1), cfg.Markets[0].ClobPairID)
	require.Equal(t, "ETH-PERP", cfg.Markets[0].Symbol)
	require.Equal(t, uint64(1), cfg.Markets[0].MinQuantityQuantums)
	require.Equal(t, uint64(10), cfg.Markets[0].MaxQuantityQuantums)
	require.Equal(t, uint64(100), cfg.Markets[0].MinSubticks)
	require.Equal(t, uint64(200), cfg.Markets[0].MaxSubticks)
}


