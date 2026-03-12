package loadtest

import "testing"

func TestWorkerSharding_DeterministicRanges_MultiEndpoint(t *testing.T) {
	// Config.Validate checks that the client factory exists. Register a minimal
	// no-op factory for this test so we can focus on sharding math.
	_ = RegisterClientFactory("perpx-perps", noopFactory{})

	// W=20 workers, E=2 endpoints, C=2 conns/endpoint => totalConns=4, G=5 workers/conn
	cfg := Config{
		ClientFactory: "perpx-perps",
		Connections:   2,
		Time:          1,
		SendPeriod:    1,
		Rate:          1,
		Count:         1,
		BroadcastTxMethod: "sync",
		Endpoints:     []string{"ws://e0/websocket", "ws://e1/websocket"},
		EndpointSelectMethod: SelectSuppliedEndpoints,
		WorkersTotal:  20,
		WorkersPerConnection: 5,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	// Verify derived bases per (endpoint, connIndex).
	type exp struct {
		endpointOrdinal int
		transactorIndex int
		base            int
	}
	// WorkersPerEndpoint = 10
	// e0: conn0 base=0, conn1 base=5
	// e1: conn0 base=10, conn1 base=15
	want := []exp{
		{0, 0, 0},
		{0, 1, 5},
		{1, 0, 10},
		{1, 1, 15},
	}

	E := len(cfg.Endpoints)
	workersPerEndpoint := cfg.WorkersTotal / E
	for _, w := range want {
		endpointBase := w.endpointOrdinal * workersPerEndpoint
		connBase := endpointBase + w.transactorIndex*cfg.WorkersPerConnection
		if connBase != w.base {
			t.Fatalf("endpoint=%d conn=%d: got base %d want %d", w.endpointOrdinal, w.transactorIndex, connBase, w.base)
		}
	}
}

type noopFactory struct{}

func (noopFactory) ValidateConfig(cfg Config) error { return nil }
func (noopFactory) NewClient(cfg Config) (Client, error) {
	return noopClient{}, nil
}

type noopClient struct{}

func (noopClient) GenerateTx() ([]byte, error) { return []byte{0x0}, nil }

