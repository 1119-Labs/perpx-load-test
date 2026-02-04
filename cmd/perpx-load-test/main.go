package main

import (
	"fmt"
	"os"

	"github.com/1119-Labs/perpx-load-test/pkg/client"
	"github.com/1119-Labs/perpx-load-test/pkg/loadtest"
	"github.com/1119-Labs/perpx-load-test/pkg/seed"
)

func main() {
	// Lightweight subcommand shim: if the first arg is "seed", run the seeder.
	// Otherwise, defer to cometbft-load-test's CLI handling.
	if len(os.Args) > 1 && os.Args[1] == "seed" {
		seed.Run(os.Args[2:])
		return
	}

	// Register the PerpX bank client factory
	if err := loadtest.RegisterClientFactory("perpx-bank", client.NewPerpxBankClientFactory()); err != nil {
		panic(fmt.Sprintf("failed to register client factory: %v", err))
	}

	loadtest.Run(&loadtest.CLIConfig{
		AppName:              "perpx-load-test",
		AppShortDesc:         "Load testing tool for PerpX Protocol",
		AppLongDesc:          "Load testing tool for PerpX Protocol localnet using cometbft-load-test.",
		DefaultClientFactory: "perpx-bank",
	})
}
