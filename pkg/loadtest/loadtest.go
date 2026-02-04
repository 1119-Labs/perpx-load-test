package loadtest

import (
	"fmt"
	"os"
	"time"

	"github.com/1119-Labs/perpx-chain/protocol/loadtest/internal/logging"
	"github.com/sirupsen/logrus"
)

// ExecuteStandalone will run a standalone (non-coordinator/worker) load test.
func ExecuteStandalone(cfg Config) error {
	// If we're in TUI mode, keep logging extremely quiet to avoid corrupting the screen.
	// We'll print errors after the UI stops.
	tuiMode := cfg.UI == "tui"
	if tuiMode {
		logrus.SetLevel(logrus.ErrorLevel)
	}

	logger := logging.NewLogrusLogger("loadtest")
	if tuiMode {
		logger = logging.NewNoopLogger()
	}

	logger.Debug("Attempting standalone load test against endpoints", "endpoints", cfg.Endpoints)

	// if we need to wait for the network to stabilize first
	if cfg.ExpectPeers > 0 {
		peers, err := waitForNetworkPeers(
			cfg.Endpoints,
			cfg.EndpointSelectMethod,
			cfg.ExpectPeers,
			cfg.MinConnectivity,
			cfg.MaxEndpoints,
			time.Duration(cfg.PeerConnectTimeout)*time.Second,
			logger,
		)
		if err != nil {
			logger.Error("Failed while waiting for peers to connect", "err", err)
			return err
		}
		cfg.Endpoints = peers
		logger.Debug("Updated list of endpoints for test", "endpoints", cfg.Endpoints)
	}

	logger.Info("Connecting to remote endpoints")
	tg := NewTransactorGroup()
	tg.SetLogger(logger)
	if err := tg.AddAll(&cfg); err != nil {
		return err
	}
	logger.Info("Initiating load test")
	tg.Start()

	var stopTUI func()
	if tuiMode {
		stopTUI = startStandaloneTUI(&cfg, tg)
		defer stopTUI()
	}

	var cancelTrap chan struct{}
	if !cfg.NoTrapInterrupts {
		// we want to know if the user hits Ctrl+Break
		cancelTrap = trapInterrupts(func() { tg.Cancel() }, logger)
		defer close(cancelTrap)
	} else {
		logger.Debug("Skipping trapping of interrupts (e.g. Ctrl+Break)")
	}

	if err := tg.Wait(); err != nil {
		if stopTUI != nil {
			stopTUI()
		}
		if tuiMode {
			fmt.Fprintln(os.Stderr, err.Error())
		} else {
			logger.Error("Failed to execute load test", "err", err)
		}
		return err
	}

	// if we need to write the final statistics
	if len(cfg.StatsOutputFile) > 0 {
		if !tuiMode {
			logger.Info("Writing aggregate statistics", "outputFile", cfg.StatsOutputFile)
		}
		if err := tg.WriteAggregateStats(cfg.StatsOutputFile); err != nil {
			if tuiMode {
				fmt.Fprintln(os.Stderr, err.Error())
			} else {
				logger.Error("Failed to write aggregate statistics", "err", err)
			}
			return err
		}
	}

	if !tuiMode {
		logger.Info("Load test complete!")
	}
	return nil
}
