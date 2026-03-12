package strategies

import (
	"context"
	"math/rand"
	"testing"
)

// BenchmarkScenario_SampleAction benchmarks action sampling performance.
func BenchmarkScenario_SampleAction(b *testing.B) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario, err := NewPerpsScenarioFromPreset(ScenarioSimplePerps, []PerpsMarketConfig{market})
	if err != nil {
		b.Fatalf("Failed to create scenario: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = scenario.SampleAction(rng)
	}
}

// BenchmarkScenario_SampleMarket benchmarks market selection performance.
func BenchmarkScenario_SampleMarket(b *testing.B) {
	markets := []PerpsMarketConfig{
		{
			ClobPairID:          1,
			Symbol:              "ETH-PERP",
			MinQuantityQuantums: 1,
			MaxQuantityQuantums: 10,
			MinSubticks:         100,
			MaxSubticks:         200,
		},
		{
			ClobPairID:          2,
			Symbol:              "BTC-PERP",
			MinQuantityQuantums: 1,
			MaxQuantityQuantums: 5,
			MinSubticks:         50,
			MaxSubticks:         150,
		},
		{
			ClobPairID:          3,
			Symbol:              "SOL-PERP",
			MinQuantityQuantums: 1,
			MaxQuantityQuantums: 8,
			MinSubticks:         80,
			MaxSubticks:         180,
		},
	}

	scenario, err := NewPerpsScenarioFromPreset(ScenarioSimplePerps, markets)
	if err != nil {
		b.Fatalf("Failed to create scenario: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = scenario.RandomMarket(rng)
	}
}

// BenchmarkScenario_SampleQuantums benchmarks quantity sampling performance.
func BenchmarkScenario_SampleQuantums(b *testing.B) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 1000,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario, err := NewPerpsScenarioFromPreset(ScenarioSimplePerps, []PerpsMarketConfig{market})
	if err != nil {
		b.Fatalf("Failed to create scenario: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = scenario.RandomQuantityQuantums(rng, market)
	}
}

// BenchmarkScenario_SampleSubticks benchmarks price sampling performance.
func BenchmarkScenario_SampleSubticks(b *testing.B) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         10000,
	}

	scenario, err := NewPerpsScenarioFromPreset(ScenarioSimplePerps, []PerpsMarketConfig{market})
	if err != nil {
		b.Fatalf("Failed to create scenario: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = scenario.RandomSubticks(ctx, rng, market)
	}
}


