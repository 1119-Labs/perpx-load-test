package client

import (
	"testing"

	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
)

func TestMultiWorkerPerpsScheduler_G10_R2(t *testing.T) {
	cfg := testDefaultCfg()
	cfg.Rate = 2
	cfg.WorkersPerConnection = 10
	cfg.WorkersTotal = 20
	cfg.Connections = 1
	cfg.Endpoints = []string{"ws://example/websocket"}

	m := &MultiWorkerPerpsClient{
		config:       cfg,
		workers:      make([]*PerpxPerpsClient, 10),
		baseWorkerID: 0,
		ratePerConn:  2,
		txCounter:    0,
	}

	// Expect:
	// tick0 (k=0): senders 0,1
	// tick1 (k=4): senders 4,5
	// tick2 (k=8): senders 8,9
	// tick3 (k=2): senders 2,3 (wrap)
	want := []int{0, 1, 4, 5, 8, 9, 2, 3}
	for i, w := range want {
		got := m.selectWorkerIndex()
		if got != w {
			t.Fatalf("slot %d: got worker %d, want %d", i, got, w)
		}
	}
}

func TestBankSenderReceiverIndices_NoSelfSend_G10_R2(t *testing.T) {
	c := &PerpxBankClient{
		workersPerConnection: 10,
		perConnectionRate:    2,
		connectionIndex:      0,
		workerIndex:          0,
	}

	// Check a handful of ticks/j values; with 2*R<=G, sender and receiver should never collide.
	for tick := uint64(0); tick < 10; tick++ {
		for j := 0; j < 2; j++ {
			s, r := c.senderReceiverIndices(tick, j)
			if s == r {
				t.Fatalf("tick=%d j=%d: sender==receiver==%d", tick, j, s)
			}
		}
	}
}

func testDefaultCfg() loadtest.Config { return loadtest.Config{} }

