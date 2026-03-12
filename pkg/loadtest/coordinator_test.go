package loadtest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCoordinatorWaitForWorkersTimeout simulates a slow or missing worker
// population and asserts that the coordinator times out cleanly instead of
// hanging indefinitely.
func TestCoordinatorWaitForWorkersTimeout(t *testing.T) {
	cfg := &Config{
		ClientFactory:        "kvstore",
		Connections:          1,
		Time:                 1,
		SendPeriod:           1,
		Rate:                 1,
		Size:                 100,
		Count:                1,
		BroadcastTxMethod:    "async",
		Endpoints:            []string{"ws://localhost:0"},
		EndpointSelectMethod: SelectSuppliedEndpoints,
		UI:                   "plain",
	}
	coordCfg := &CoordinatorConfig{
		BindAddr:             "localhost:0",
		ExpectWorkers:        1,
		WorkerConnectTimeout: 1,
		ShutdownWait:         0,
		LoadTestID:           0,
	}

	coord := NewCoordinator(cfg, coordCfg)

	done := make(chan struct{})
	var err error
	go func() {
		err = coord.waitForWorkers()
		close(done)
	}()

	select {
	case <-done:
		if err == nil {
			t.Fatalf("expected timeout error from waitForWorkers, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("waitForWorkers did not return within expected time window")
	}
}

// TestCoordinatorReceiveTestingUpdates_RemoteWorkerFailure injects a worker
// unregister event with an error and asserts that the coordinator propagates it
// as a terminal failure, simulating a worker disconnect/failure mid-test.
func TestCoordinatorReceiveTestingUpdates_RemoteWorkerFailure(t *testing.T) {
	// Reset the default Prometheus registry to avoid duplicate metric
	// registrations across tests that construct new coordinators.
	resetDefaultPrometheusRegistry()
	cfg := &Config{
		ClientFactory:        "kvstore",
		Connections:          1,
		Time:                 1,
		SendPeriod:           1,
		Rate:                 1,
		Size:                 100,
		Count:                1,
		BroadcastTxMethod:    "async",
		Endpoints:            []string{"ws://localhost:0"},
		EndpointSelectMethod: SelectSuppliedEndpoints,
		UI:                   "plain",
		StatsOutputFile:      filepath.Join(t.TempDir(), "stats.csv"),
	}
	coordCfg := &CoordinatorConfig{
		BindAddr:             "localhost:0",
		ExpectWorkers:        1,
		WorkerConnectTimeout: 10,
		ShutdownWait:         0,
		LoadTestID:           1,
	}

	coord := NewCoordinator(cfg, coordCfg)

	// Pretend one worker is registered so receiveTestingUpdates will treat
	// unregister events as meaningful.
	coord.workers["worker-1"] = nil

	done := make(chan struct{})
	var err error
	go func() {
		err = coord.receiveTestingUpdates()
		close(done)
	}()

	coord.workerUnregister <- remoteWorkerUnregisterRequest{
		id:  "worker-1",
		err: fmt.Errorf("simulated disconnect"),
	}

	select {
	case <-done:
		if err == nil {
			t.Fatalf("expected error from receiveTestingUpdates, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("receiveTestingUpdates did not return after worker failure")
	}

	// Ensure that a stats file was written and is non-empty when the
	// coordinator has partial per-worker data.
	info, statErr := os.Stat(cfg.StatsOutputFile)
	if statErr == nil && info.Size() == 0 {
		t.Fatalf("expected non-empty stats file at %s", cfg.StatsOutputFile)
	}
}

