package strategies

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// MarketDataProvider is an interface for fetching current market data from the chain.
type MarketDataProvider interface {
	// GetMidPrice returns the current mid-price for a CLOB pair in subticks.
	GetMidPrice(ctx context.Context, clobPairID uint32) (uint64, error)
	// GetBestBidAsk returns the best bid and ask prices for a CLOB pair in subticks.
	GetBestBidAsk(ctx context.Context, clobPairID uint32) (bid, ask uint64, err error)
}

// MockMarketDataProvider is a test provider that returns configurable prices.
type MockMarketDataProvider struct {
	mu          sync.RWMutex
	midPrices   map[uint32]uint64
	bestBids    map[uint32]uint64
	bestAsks    map[uint32]uint64
	defaultBid  uint64
	defaultAsk  uint64
	defaultMid  uint64
}

// NewMockMarketDataProvider creates a new mock provider with default prices.
func NewMockMarketDataProvider(defaultMid, defaultBid, defaultAsk uint64) *MockMarketDataProvider {
	return &MockMarketDataProvider{
		midPrices:  make(map[uint32]uint64),
		bestBids:   make(map[uint32]uint64),
		bestAsks:   make(map[uint32]uint64),
		defaultMid: defaultMid,
		defaultBid: defaultBid,
		defaultAsk: defaultAsk,
	}
}

// SetMidPrice sets the mid-price for a specific CLOB pair.
func (m *MockMarketDataProvider) SetMidPrice(clobPairID uint32, price uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.midPrices[clobPairID] = price
}

// SetBestBidAsk sets the best bid and ask for a specific CLOB pair.
func (m *MockMarketDataProvider) SetBestBidAsk(clobPairID uint32, bid, ask uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bestBids[clobPairID] = bid
	m.bestAsks[clobPairID] = ask
	// Update mid-price as average of bid and ask
	m.midPrices[clobPairID] = (bid + ask) / 2
}

// GetMidPrice returns the mid-price for the given CLOB pair.
func (m *MockMarketDataProvider) GetMidPrice(ctx context.Context, clobPairID uint32) (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if price, ok := m.midPrices[clobPairID]; ok {
		return price, nil
	}
	return m.defaultMid, nil
}

// GetBestBidAsk returns the best bid and ask for the given CLOB pair.
func (m *MockMarketDataProvider) GetBestBidAsk(ctx context.Context, clobPairID uint32) (bid, ask uint64, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if b, ok := m.bestBids[clobPairID]; ok {
		if a, ok := m.bestAsks[clobPairID]; ok {
			return b, a, nil
		}
	}
	return m.defaultBid, m.defaultAsk, nil
}

// RESTMarketDataProvider queries the PerpX chain REST API for market data.
type RESTMarketDataProvider struct {
	restURL     string
	httpClient  *http.Client
	cache       map[uint32]*cachedPrice
	cacheMtx    sync.RWMutex
	cacheTTL    time.Duration
}

type cachedPrice struct {
	midPrice uint64
	bid      uint64
	ask      uint64
	expires  time.Time
}

// NewRESTMarketDataProvider creates a new REST-based market data provider.
func NewRESTMarketDataProvider(restURL string, cacheTTL time.Duration) *RESTMarketDataProvider {
	return &RESTMarketDataProvider{
		restURL:    restURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		cache:      make(map[uint32]*cachedPrice),
		cacheTTL:   cacheTTL,
	}
}

// GetMidPrice returns the mid-price for the given CLOB pair, using cache if available.
func (r *RESTMarketDataProvider) GetMidPrice(ctx context.Context, clobPairID uint32) (uint64, error) {
	bid, ask, err := r.GetBestBidAsk(ctx, clobPairID)
	if err != nil {
		return 0, err
	}
	// Calculate mid-price as average of bid and ask
	if bid == 0 && ask == 0 {
		return 0, fmt.Errorf("no market data available for clob pair %d", clobPairID)
	}
	if bid == 0 {
		return ask, nil
	}
	if ask == 0 {
		return bid, nil
	}
	return (bid + ask) / 2, nil
}

// GetBestBidAsk returns the best bid and ask for the given CLOB pair.
func (r *RESTMarketDataProvider) GetBestBidAsk(ctx context.Context, clobPairID uint32) (bid, ask uint64, err error) {
	// Check cache first
	r.cacheMtx.RLock()
	if cached, ok := r.cache[clobPairID]; ok && time.Now().Before(cached.expires) {
		bid, ask := cached.bid, cached.ask
		r.cacheMtx.RUnlock()
		return bid, ask, nil
	}
	r.cacheMtx.RUnlock()

	// Query the chain REST API
	// Note: The actual endpoint structure may vary. This is a placeholder that should
	// be adjusted based on the actual PerpX chain REST API structure.
	url := fmt.Sprintf("%s/dydxprotocol/clob/v1/orderbook/%d", r.restURL, clobPairID)
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query orderbook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("orderbook query failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var orderbookResp struct {
		Bids []struct {
			Price string `json:"price"`
		} `json:"bids"`
		Asks []struct {
			Price string `json:"price"`
		} `json:"asks"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&orderbookResp); err != nil {
		return 0, 0, fmt.Errorf("failed to decode orderbook response: %w", err)
	}

	// Parse best bid (first element, highest price)
	var bestBid uint64
	if len(orderbookResp.Bids) > 0 {
		// The price format may vary; adjust parsing as needed
		// For now, assume it's a string representation of subticks
		if _, err := fmt.Sscanf(orderbookResp.Bids[0].Price, "%d", &bestBid); err != nil {
			// If parsing fails, try to extract numeric value
			bestBid = 0
		}
	}

	// Parse best ask (first element, lowest price)
	var bestAsk uint64
	if len(orderbookResp.Asks) > 0 {
		if _, err := fmt.Sscanf(orderbookResp.Asks[0].Price, "%d", &bestAsk); err != nil {
			bestAsk = 0
		}
	}

	// Update cache
	r.cacheMtx.Lock()
	r.cache[clobPairID] = &cachedPrice{
		bid:     bestBid,
		ask:     bestAsk,
		expires: time.Now().Add(r.cacheTTL),
	}
	r.cacheMtx.Unlock()

	return bestBid, bestAsk, nil
}


