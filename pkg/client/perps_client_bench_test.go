package client

import (
	"testing"

	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/strategies"
)

// BenchmarkPerpsClient_GenerateTx benchmarks transaction generation
// without order tracking overhead.
func BenchmarkPerpsClient_GenerateTx(b *testing.B) {
	market := strategies.PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario, err := strategies.NewPerpsScenarioFromPreset(strategies.ScenarioSimplePerps, []strategies.PerpsMarketConfig{market})
	if err != nil {
		b.Fatalf("Failed to create scenario: %v", err)
	}

	strategyCfg := strategies.PerpsOrderStrategyConfig{
		ChainID:      "localperpxprotocol",
		Denom:        "aperpx",
		Markets:      scenario.Markets,
		ScenarioName: scenario.Name,
	}
	strategy, err := strategies.NewPerpsOrderStrategy(strategyCfg)
	if err != nil {
		b.Fatalf("Failed to create strategy: %v", err)
	}

	cfg := loadtest.Config{
		Endpoints: []string{"ws://localhost:36657/websocket"},
	}

	client, err := NewPerpxPerpsClient(cfg, strategy, 0)
	if err != nil {
		b.Fatalf("Failed to create client: %v", err)
	}

	// Mock account query to avoid network calls
	client.accountQueried = true
	client.accountNum = 1
	client.sequence = 0

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := client.GenerateTx()
		if err != nil {
			b.Fatalf("GenerateTx failed: %v", err)
		}
	}
}

// BenchmarkPerpsClient_GenerateTx_WithTracking benchmarks transaction generation
// with order tracking enabled (more realistic scenario).
func BenchmarkPerpsClient_GenerateTx_WithTracking(b *testing.B) {
	market := strategies.PerpsMarketConfig{
		ClobPairID:          1,
		Symbol:              "ETH-PERP",
		MinQuantityQuantums: 1,
		MaxQuantityQuantums: 10,
		MinSubticks:         100,
		MaxSubticks:         200,
	}

	scenario, err := strategies.NewPerpsScenarioFromPreset(strategies.ScenarioMakerTaker, []strategies.PerpsMarketConfig{market})
	if err != nil {
		b.Fatalf("Failed to create scenario: %v", err)
	}

	strategyCfg := strategies.PerpsOrderStrategyConfig{
		ChainID:      "localperpxprotocol",
		Denom:        "aperpx",
		Markets:      scenario.Markets,
		ScenarioName: scenario.Name,
	}
	strategy, err := strategies.NewPerpsOrderStrategy(strategyCfg)
	if err != nil {
		b.Fatalf("Failed to create strategy: %v", err)
	}

	cfg := loadtest.Config{
		Endpoints: []string{"ws://localhost:36657/websocket"},
	}

	client, err := NewPerpxPerpsClient(cfg, strategy, 0)
	if err != nil {
		b.Fatalf("Failed to create client: %v", err)
	}

	// Mock account query
	client.accountQueried = true
	client.accountNum = 1
	client.sequence = 0

	// Pre-populate some tracked orders to simulate realistic scenario
	for i := 0; i < 100; i++ {
		client.trackOrder(trackedOrder{
			ClobPairID: 1,
			ClientID:   uint32(i + 1),
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := client.GenerateTx()
		if err != nil {
			b.Fatalf("GenerateTx failed: %v", err)
		}
	}
}

