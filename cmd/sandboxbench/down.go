package main

import (
	"os"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ayush6624/sandbox-benchmark/internal/config"
	"github.com/ayush6624/sandbox-benchmark/internal/state"
)

func downCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop a running microVM using state file (SIGTERM)",
		RunE:  runDown,
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to JSON config (required)")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

func runDown(cmd *cobra.Command, args []string) error {
	cf, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	st, err := state.Read(cf.StatePath)
	if err != nil {
		return err
	}
	if err := syscall.Kill(st.PID, syscall.SIGTERM); err != nil {
		return err
	}
	_ = os.Remove(cf.StatePath)
	return nil
}
