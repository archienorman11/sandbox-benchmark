package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ayush6624/sandbox-benchmark/internal/firecracker"
	"github.com/ayush6624/sandbox-benchmark/internal/state"
)

func upCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start a microVM and block until signal (writes state file)",
		RunE:  runUp,
	}
	addVMFlags(cmd)
	return cmd
}

func runUp(cmd *cobra.Command, args []string) error {
	cf, opts, err := loadAndMerge()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	m, rtCfg, err := firecracker.NewMachine(ctx, opts, noValidate)
	if err != nil {
		return err
	}
	if err := firecracker.Start(ctx, m); err != nil {
		return err
	}

	pid, err := m.PID()
	if err != nil {
		_ = firecracker.StopForce(m)
		return err
	}
	if err := state.Write(cf.StatePath, state.VM{PID: pid, SocketPath: rtCfg.SocketPath, VMID: rtCfg.VMID}); err != nil {
		fmt.Fprintf(os.Stderr, "write state: %v\n", err)
	}

	<-ctx.Done()
	shCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_ = firecracker.ShutdownGuest(shCtx, m)
	cancel()
	waitCtx, wcancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err = firecracker.Wait(waitCtx, m)
	wcancel()
	if err != nil {
		_ = firecracker.StopForce(m)
		_ = firecracker.Wait(context.Background(), m)
	}
	_ = os.Remove(cf.StatePath)
	return nil
}
