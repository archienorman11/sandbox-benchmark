package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sandboxbench",
		Short: "Firecracker microVM benchmarking tool",
	}
	root.AddCommand(doctorCmd(), upCmd(), downCmd(), benchCmd(), benchSnapshotCmd(), benchConcurrentCmd(), benchAllCmd())
	return root
}
