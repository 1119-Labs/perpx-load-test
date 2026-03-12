package client

import (
	"sync"
	"testing"

	clobtypes "github.com/1119-Labs/perpx-chain/protocol/x/clob/types"
	"github.com/stretchr/testify/require"
)

func TestPerpxPerpsClient_PositionTracking(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Initially no position
	require.False(t, client.hasPosition(1))
	require.Nil(t, client.getPosition(1))

	// Add a long position (BUY side)
	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 100, 150)
	require.True(t, client.hasPosition(1))
	pos := client.getPosition(1)
	require.NotNil(t, pos)
	require.Equal(t, uint32(1), pos.ClobPairID)
	require.Equal(t, clobtypes.Order_SIDE_BUY, pos.Side)
	require.Equal(t, uint64(100), pos.Size)
	require.Equal(t, uint64(150), pos.EntryPrice)

	// Increase position size
	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 50, 160)
	pos = client.getPosition(1)
	require.Equal(t, uint64(150), pos.Size) // 100 + 50
	// Entry price should be weighted average: (100*150 + 50*160) / 150 = 153.33...
	require.GreaterOrEqual(t, pos.EntryPrice, uint64(153))
	require.LessOrEqual(t, pos.EntryPrice, uint64(154))

	// Partial close
	client.updatePosition(1, clobtypes.Order_SIDE_SELL, 75, 155)
	pos = client.getPosition(1)
	require.Equal(t, uint64(75), pos.Size) // 150 - 75
	require.Equal(t, clobtypes.Order_SIDE_BUY, pos.Side) // Side doesn't change on partial close

	// Full close
	client.updatePosition(1, clobtypes.Order_SIDE_SELL, 75, 155)
	require.False(t, client.hasPosition(1))
	require.Nil(t, client.getPosition(1))
}

func TestPerpxPerpsClient_PositionFlip(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Start with long position
	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 100, 150)

	// Close more than position size (flip to short)
	client.updatePosition(1, clobtypes.Order_SIDE_SELL, 150, 155)
	pos := client.getPosition(1)
	require.NotNil(t, pos)
	require.Equal(t, clobtypes.Order_SIDE_SELL, pos.Side)
	require.Equal(t, uint64(50), pos.Size) // 150 - 100 = 50
	require.Equal(t, uint64(155), pos.EntryPrice)
}

func TestPerpxPerpsClient_MultipleMarkets(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Positions on different markets should be independent
	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 100, 150)
	client.updatePosition(2, clobtypes.Order_SIDE_SELL, 200, 250)

	pos1 := client.getPosition(1)
	require.NotNil(t, pos1)
	require.Equal(t, uint32(1), pos1.ClobPairID)
	require.Equal(t, clobtypes.Order_SIDE_BUY, pos1.Side)

	pos2 := client.getPosition(2)
	require.NotNil(t, pos2)
	require.Equal(t, uint32(2), pos2.ClobPairID)
	require.Equal(t, clobtypes.Order_SIDE_SELL, pos2.Side)
}

func TestPerpxPerpsClient_PositionIncrease_SameSideWeightedAverage_LongAndShort(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	type step struct {
		side     clobtypes.Order_Side
		size     uint64
		price    uint64
		wantSize uint64
	}

	tests := []struct {
		name         string
		clobPairID   uint32
		steps        []step
		wantSide     clobtypes.Order_Side
		wantPriceMin uint64
		wantPriceMax uint64
	}{
		{
			name:       "long increase with weighted average",
			clobPairID: 1,
			steps: []step{
				{side: clobtypes.Order_SIDE_BUY, size: 100, price: 150, wantSize: 100},
				{side: clobtypes.Order_SIDE_BUY, size: 50, price: 160, wantSize: 150},
			},
			wantSide:     clobtypes.Order_SIDE_BUY,
			// (100*150 + 50*160) / 150 = 153.33... -> integer division truncates to 153
			wantPriceMin: 153,
			wantPriceMax: 153,
		},
		{
			name:       "short increase with weighted average",
			clobPairID: 2,
			steps: []step{
				{side: clobtypes.Order_SIDE_SELL, size: 80, price: 200, wantSize: 80},
				{side: clobtypes.Order_SIDE_SELL, size: 120, price: 220, wantSize: 200},
			},
			wantSide:     clobtypes.Order_SIDE_SELL,
			// (80*200 + 120*220) / 200 = 212
			wantPriceMin: 212,
			wantPriceMax: 212,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, s := range tc.steps {
				client.updatePosition(tc.clobPairID, s.side, s.size, s.price)
				pos := client.getPosition(tc.clobPairID)
				require.NotNil(t, pos)
				require.Equal(t, s.wantSize, pos.Size)
				require.Equal(t, s.side, pos.Side)
			}

			pos := client.getPosition(tc.clobPairID)
			require.NotNil(t, pos)
			require.Equal(t, tc.wantSide, pos.Side)
			require.GreaterOrEqual(t, pos.EntryPrice, tc.wantPriceMin)
			require.LessOrEqual(t, pos.EntryPrice, tc.wantPriceMax)
		})
	}
}

func TestPerpxPerpsClient_PositionIncrease_FromZeroToNonZero(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	require.False(t, client.hasPosition(1))
	require.Nil(t, client.getPosition(1))

	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 50, 175)

	require.True(t, client.hasPosition(1))
	pos := client.getPosition(1)
	require.NotNil(t, pos)
	require.Equal(t, uint32(1), pos.ClobPairID)
	require.Equal(t, clobtypes.Order_SIDE_BUY, pos.Side)
	require.Equal(t, uint64(50), pos.Size)
	require.Equal(t, uint64(175), pos.EntryPrice)
}

func TestPerpxPerpsClient_PositionDecrease_PartialAndFullClose(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Start with a 100-quantum long position
	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 100, 150)
	orig := client.getPosition(1)
	require.NotNil(t, orig)
	origPrice := orig.EntryPrice

	type closeCase struct {
		name          string
		closeSize     uint64
		wantSize      uint64
		wantHasPos    bool
		wantFlip      bool
		wantFlipSide  clobtypes.Order_Side
		wantEntrySame bool
	}

	tests := []closeCase{
		{
			name:          "25 percent close",
			closeSize:     25,
			wantSize:      75,
			wantHasPos:    true,
			wantEntrySame: true,
		},
		{
			name:          "50 percent close",
			closeSize:     50,
			wantSize:      50,
			wantHasPos:    true,
			wantEntrySame: true,
		},
		{
			name:          "75 percent close",
			closeSize:     75,
			wantSize:      25,
			wantHasPos:    true,
			wantEntrySame: true,
		},
		{
			name:       "100 percent close",
			closeSize:  100,
			wantSize:   0,
			wantHasPos: false,
		},
		{
			name:         "oversized close flips to short",
			closeSize:    150,
			wantSize:     50,
			wantHasPos:   true,
			wantFlip:     true,
			wantFlipSide: clobtypes.Order_SIDE_SELL,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Reset to original 100-long position for each subtest
			client.positions = map[uint32]*trackedPosition{
				1: {
					ClobPairID: 1,
					Side:       clobtypes.Order_SIDE_BUY,
					Size:       100,
					EntryPrice: origPrice,
				},
			}

			client.updatePosition(1, clobtypes.Order_SIDE_SELL, tc.closeSize, 155)

			if !tc.wantHasPos {
				require.False(t, client.hasPosition(1))
				require.Nil(t, client.getPosition(1))
				return
			}

			pos := client.getPosition(1)
			require.NotNil(t, pos)
			require.Equal(t, tc.wantSize, pos.Size)

			if tc.wantFlip {
				require.Equal(t, tc.wantFlipSide, pos.Side)
				require.Equal(t, uint64(155), pos.EntryPrice)
			} else {
				require.Equal(t, clobtypes.Order_SIDE_BUY, pos.Side)
				if tc.wantEntrySame {
					require.Equal(t, origPrice, pos.EntryPrice)
				}
			}
		})
	}
}

func TestPerpxPerpsClient_PositionFlip_ShortToLong(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Start with a short position
	client.updatePosition(1, clobtypes.Order_SIDE_SELL, 120, 220)

	// Buy more than current short size -> flip to long
	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 200, 210)

	pos := client.getPosition(1)
	require.NotNil(t, pos)
	require.Equal(t, clobtypes.Order_SIDE_BUY, pos.Side)
	require.Equal(t, uint64(80), pos.Size)     // 200 - 120
	require.Equal(t, uint64(210), pos.EntryPrice) // new position at flip price
}

func TestPerpxPerpsClient_PositionFlip_FromZeroCreatesPosition(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	require.False(t, client.hasPosition(3))

	client.updatePosition(3, clobtypes.Order_SIDE_SELL, 75, 190)

	require.True(t, client.hasPosition(3))
	pos := client.getPosition(3)
	require.NotNil(t, pos)
	require.Equal(t, uint32(3), pos.ClobPairID)
	require.Equal(t, clobtypes.Order_SIDE_SELL, pos.Side)
	require.Equal(t, uint64(75), pos.Size)
	require.Equal(t, uint64(190), pos.EntryPrice)
}

func TestPerpxPerpsClient_PositionZeroSizeUpdateIsNoopForExistingPosition(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 100, 150)
	posBefore := client.getPosition(1)
	require.NotNil(t, posBefore)

	client.updatePosition(1, clobtypes.Order_SIDE_SELL, 0, 160)

	posAfter := client.getPosition(1)
	require.NotNil(t, posAfter)
	require.Equal(t, posBefore.Side, posAfter.Side)
	require.Equal(t, posBefore.Size, posAfter.Size)
	require.Equal(t, posBefore.EntryPrice, posAfter.EntryPrice)
}

func TestPerpxPerpsClient_PositionTracking_InvalidClobPairID(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	require.False(t, client.hasPosition(9999))
	require.Nil(t, client.getPosition(9999))

	// Creating a position on an arbitrary ID should behave like any other
	client.updatePosition(9999, clobtypes.Order_SIDE_BUY, 10, 123)

	require.True(t, client.hasPosition(9999))
	pos := client.getPosition(9999)
	require.NotNil(t, pos)
	require.Equal(t, uint32(9999), pos.ClobPairID)
}

func TestPerpxPerpsClient_PositionTracking_ClearOrderTrackingDoesNotAffectPositions(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Create a position and some orders
	client.updatePosition(1, clobtypes.Order_SIDE_BUY, 100, 150)
	client.trackOrder(trackedOrder{ClobPairID: 1, ClientID: 1})
	client.trackOrder(trackedOrder{ClobPairID: 1, ClientID: 2})
	require.Len(t, client.orders, 2)
	require.True(t, client.hasPosition(1))

	client.ClearOrderTracking()

	require.Len(t, client.orders, 0)
	require.True(t, client.hasPosition(1))
	require.NotNil(t, client.getPosition(1))
}

func TestPerpxPerpsClient_PositionTracking_ConcurrentUpdatesAndReads(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	const (
		clobPairID         = uint32(1)
		numUpdateGoroutine = 10
		numReadGoroutine   = 10
		iterations         = 1000
	)

	var wg sync.WaitGroup

	// Writers
	for i := 0; i < numUpdateGoroutine; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				client.updatePosition(clobPairID, clobtypes.Order_SIDE_BUY, 1, 150+uint64(j%10))
			}
		}()
	}

	// Readers
	for i := 0; i < numReadGoroutine; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = client.getPosition(clobPairID)
				_ = client.hasPosition(clobPairID)
			}
		}()
	}

	wg.Wait()

	pos := client.getPosition(clobPairID)
	require.NotNil(t, pos)
	require.Equal(t, uint64(numUpdateGoroutine*iterations), pos.Size)
	require.GreaterOrEqual(t, pos.EntryPrice, uint64(150))
	require.LessOrEqual(t, pos.EntryPrice, uint64(160))
}

func TestPerpxPerpsClient_PositionTracking_ConcurrentDifferentMarkets(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	const goroutines = 10
	const iterations = 500

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			clobPairID := uint32(id + 1)
			for j := 0; j < iterations; j++ {
				client.updatePosition(clobPairID, clobtypes.Order_SIDE_BUY, 1, 100+uint64(id))
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < goroutines; i++ {
		clobPairID := uint32(i + 1)
		pos := client.getPosition(clobPairID)
		require.NotNil(t, pos, "expected position for market %d", clobPairID)
		require.Equal(t, uint64(iterations), pos.Size)
		require.Equal(t, clobtypes.Order_SIDE_BUY, pos.Side)
	}
}


