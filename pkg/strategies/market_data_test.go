package strategies

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMockMarketDataProvider_GetMidPrice(t *testing.T) {
	provider := NewMockMarketDataProvider(150, 149, 151)

	// Test default mid-price
	mid, err := provider.GetMidPrice(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(150), mid)

	// Test custom mid-price
	provider.SetMidPrice(1, 200)
	mid, err = provider.GetMidPrice(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(200), mid)
}

func TestMockMarketDataProvider_GetBestBidAsk(t *testing.T) {
	provider := NewMockMarketDataProvider(150, 149, 151)

	// Test default bid/ask
	bid, ask, err := provider.GetBestBidAsk(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(149), bid)
	require.Equal(t, uint64(151), ask)

	// Test custom bid/ask
	provider.SetBestBidAsk(1, 199, 201)
	bid, ask, err = provider.GetBestBidAsk(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(199), bid)
	require.Equal(t, uint64(201), ask)

	// Verify mid-price is updated
	mid, err := provider.GetMidPrice(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(200), mid) // (199 + 201) / 2
}

func TestRandomSubticks_WithMidPrice(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
		UseMidPrice:         true,
		PriceOffsetBps:      50, // +0.5%
	}

	provider := NewMockMarketDataProvider(150, 149, 151)
	scenario := &PerpsScenarioConfig{
		Name:            "test",
		Markets:         []PerpsMarketConfig{market},
		MidPriceProvider: provider,
	}

	ctx := context.Background()
	rng := NewRandForWorker(0, 0)

	// Generate multiple prices and verify they're within bounds
	for i := 0; i < 100; i++ {
		price := scenario.RandomSubticks(ctx, rng, market)
		require.GreaterOrEqual(t, price, market.MinSubticks)
		require.LessOrEqual(t, price, market.MaxSubticks)
	}
}

func TestRandomSubticks_WithoutMidPrice(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
		UseMidPrice:         false,
	}

	scenario := &PerpsScenarioConfig{
		Name:    "test",
		Markets: []PerpsMarketConfig{market},
	}

	ctx := context.Background()
	rng := NewRandForWorker(0, 0)

	// Generate multiple prices and verify they're within bounds
	for i := 0; i < 100; i++ {
		price := scenario.RandomSubticks(ctx, rng, market)
		require.GreaterOrEqual(t, price, market.MinSubticks)
		require.LessOrEqual(t, price, market.MaxSubticks)
	}
}

func TestRandomSubticks_PriceOffset(t *testing.T) {
	market := PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
		UseMidPrice:         true,
		PriceOffsetBps:      100, // +1%
	}

	provider := NewMockMarketDataProvider(150, 149, 151)
	scenario := &PerpsScenarioConfig{
		Name:            "test",
		Markets:         []PerpsMarketConfig{market},
		MidPriceProvider: provider,
	}

	ctx := context.Background()
	rng := NewRandForWorker(0, 0)

	// With +1% offset on 150, expected price is around 151.5
	// Allow some variance due to random offset
	prices := make([]uint64, 100)
	for i := 0; i < 100; i++ {
		prices[i] = scenario.RandomSubticks(ctx, rng, market)
		require.GreaterOrEqual(t, prices[i], market.MinSubticks)
		require.LessOrEqual(t, prices[i], market.MaxSubticks)
	}

	// Average should be close to 150 * 1.01 = 151.5
	var sum uint64
	for _, p := range prices {
		sum += p
	}
	avg := float64(sum) / float64(len(prices))
	require.InEpsilon(t, 151.5, avg, 0.1) // 10% tolerance
}

func TestRESTMarketDataProvider_Cache(t *testing.T) {
	// This test would require a mock HTTP server
	// For now, we just verify the cache structure exists
	provider := NewRESTMarketDataProvider("http://localhost:1317", 1*time.Second)
	require.NotNil(t, provider)
	require.NotNil(t, provider.cache)
}


