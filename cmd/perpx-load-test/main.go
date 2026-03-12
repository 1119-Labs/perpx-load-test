package main

import (
	"fmt"
	"os"

	"github.com/1119-Labs/perpx-load-test/internal/logging"
	"github.com/1119-Labs/perpx-load-test/pkg/client"
	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/seed"
)

func main() {
	logger := logging.NewLogrusLogger("main")

	if err := loadtest.RegisterClientFactory("perpx-bank", client.NewPerpxBankClientFactory()); err != nil {
		panic(fmt.Sprintf("failed to register client factory: %v", err))
	}
	if err := loadtest.RegisterClientFactory("perpx-perps", client.NewPerpxPerpsClientFactory()); err != nil {
		panic(fmt.Sprintf("failed to register perps client factory: %v", err))
	}

	cliConfig := &loadtest.CLIConfig{
		AppName:              "perpx-load-test",
		AppShortDesc:         "Load testing tool for PerpX Protocol",
		AppLongDesc:          "Load testing tool for PerpX Protocol localnet using cometbft-load-test.",
		DefaultClientFactory: "perpx-bank",
	}

	root := loadtest.BuildCLI(cliConfig, logger)
	root.AddCommand(seed.NewSeedCommand())

	if err := root.Execute(); err != nil {
		logger.Error("Error", "err", err)
		os.Exit(1)
	}
}
