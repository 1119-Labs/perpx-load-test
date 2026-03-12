package seed

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Run executes the seed command with the given args (for use by main or tests).
// It builds a Cobra root with the seed subcommand and runs it.
func Run(args []string) {
	root := &cobra.Command{
		Use:   "perpx-load-test",
		Short: "Load testing tool for PerpX Protocol",
	}
	root.AddCommand(NewSeedCommand())
	root.SetArgs(append([]string{"seed"}, args...))

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
