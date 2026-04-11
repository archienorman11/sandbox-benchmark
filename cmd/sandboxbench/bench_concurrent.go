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
	concurrency      int
	concOut          string
	concResultDir    string
	concLabel        string
)

func benchConcurrentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench-concurrent",
		Short: "Benchmark parallel VM cold-start under contention",
		Long:  `Spawns N VMs simultaneously and measures per-VM cold-start latency and total wall time.`,
		RunE:  runBenchConcurrent,
	}
	addVMFlags(cmd)
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 5, "number of VMs to spawn in parallel")
	cmd.Flags().StringVarP(&concOut, "out", "o", "", "write JSON report to exact path")
	cmd.Flags().StringVar(&concResultDir, "results-dir", "results", "directory to store timestamped reports")
	cmd.Flags().StringVarP(&concLabel, "label", "l", "", "optional label in filename")
	return cmd
}

func runBenchConcurrent(cmd *cobra.Command, args []string) error {
	_, opts, err := loadAndMerge()
	if err != nil {
		return err
	}

	meta := map[string]any{
		"go_os":       runtime.GOOS,
		"go_arch":     runtime.GOARCH,
		"bench":       "concurrent",
		"concurrency": concurrency,
	}
	if concLabel != "" {
		meta["label"] = concLabel
	}

	fmt.Printf("  spawning %d VMs in parallel...\n", concurrency)

	report, err := bench.RunConcurrent(context.Background(), bench.ConcurrentOptions{
		Concurrency:     concurrency,
		Opts:            opts,
		DisableValidate: noValidate,
		HostMeta:        meta,
	})
	if err != nil {
		return err
	}

	outPath := concOut
	if outPath == "" {
		if err := os.MkdirAll(concResultDir, 0o755); err != nil {
			return fmt.Errorf("create results dir: %w", err)
		}
		ts := report.Timestamp.Format("20060102-150405")
		name := fmt.Sprintf("concurrent-%s-c%d", ts, concurrency)
		if concLabel != "" {
			name += "-" + concLabel
		}
		name += ".json"
		outPath = filepath.Join(concResultDir, name)
	}

	if err := bench.WriteJSON(outPath, report); err != nil {
		return err
	}

	bold := color.New(color.Bold).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	fmt.Printf("\n  %s  %d\n", bold("concurrency"), report.Concurrency)
	fmt.Printf("  %s   %s\n", bold("wall time"), cyan(fmt.Sprintf("%.2f ms", report.TotalMs)))
	fmt.Printf("  %s  p50 = %s  p95 = %s\n", bold("cold start"), cyan(fmt.Sprintf("%.2f ms", report.Summary.ColdStartP50Ms)), cyan(fmt.Sprintf("%.2f ms", report.Summary.ColdStartP95Ms)))
	fmt.Printf("  %s %s  %s %s\n", bold("succeeded"), green(fmt.Sprintf("%d", report.Summary.Succeeded)), bold("failed"), red(fmt.Sprintf("%d", report.Summary.Failed)))
	fmt.Printf("\n  report saved to %s\n", cyan(outPath))
	return nil
}
