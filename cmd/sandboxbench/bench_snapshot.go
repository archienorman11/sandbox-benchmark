package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/ayush6624/sandbox-benchmark/internal/bench"
)

var (
	snapIter      int
	snapOut       string
	snapResultDir string
	snapLabel     string
	snapDir       string
)

func benchSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench-snapshot",
		Short: "Benchmark snapshot create + restore latency",
		Long: `Boots a VM, creates a snapshot, then measures restore time over N iterations.
Compares snapshot/restore vs cold-start performance.`,
		RunE: runBenchSnapshot,
	}
	addVMFlags(cmd)
	cmd.Flags().IntVarP(&snapIter, "iterations", "n", 3, "number of restore iterations")
	cmd.Flags().StringVarP(&snapOut, "out", "o", "", "write JSON report to exact path")
	cmd.Flags().StringVar(&snapResultDir, "results-dir", "results", "directory to store timestamped reports")
	cmd.Flags().StringVarP(&snapLabel, "label", "l", "", "optional label in filename")
	cmd.Flags().StringVar(&snapDir, "snapshot-dir", "/tmp", "directory to store snapshot files")
	return cmd
}

func runBenchSnapshot(cmd *cobra.Command, args []string) error {
	_, opts, err := loadAndMerge()
	if err != nil {
		return err
	}

	meta := map[string]any{
		"go_os":   runtime.GOOS,
		"go_arch": runtime.GOARCH,
		"bench":   "snapshot-restore",
	}
	if snapLabel != "" {
		meta["label"] = snapLabel
	}

	report, err := bench.RunSnapshotRestore(context.Background(), bench.SnapshotOptions{
		Iterations:      snapIter,
		Opts:            opts,
		DisableValidate: noValidate,
		HostMeta:        meta,
		SnapshotDir:     snapDir,
	})
	if err != nil {
		return err
	}

	outPath := snapOut
	if outPath == "" {
		if err := os.MkdirAll(snapResultDir, 0o755); err != nil {
			return fmt.Errorf("create results dir: %w", err)
		}
		ts := report.Timestamp.Format("20060102-150405")
		name := "snapshot-" + ts
		if snapLabel != "" {
			name += "-" + snapLabel
		}
		name += ".json"
		outPath = filepath.Join(snapResultDir, name)
	}

	if err := bench.WriteJSON(outPath, report); err != nil {
		return err
	}

	bold := color.New(color.Bold).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	fmt.Printf("\n  %s   %s\n", bold("snapshot create"), cyan(fmt.Sprintf("%.2f ms", report.SnapshotMs)))
	fmt.Printf("  %s  p50 = %s  p95 = %s\n", bold("restore"), cyan(fmt.Sprintf("%.2f ms", report.Summary.RestoreP50Ms)), cyan(fmt.Sprintf("%.2f ms", report.Summary.RestoreP95Ms)))
	fmt.Printf("  %s p50 = %s  p95 = %s\n", bold("teardown"), cyan(fmt.Sprintf("%.2f ms", report.Summary.TeardownP50Ms)), cyan(fmt.Sprintf("%.2f ms", report.Summary.TeardownP95Ms)))
	fmt.Printf("  %s %d\n", bold("iterations"), report.Iterations)
	fmt.Printf("\n  report saved to %s\n", cyan(outPath))
	return nil
}
