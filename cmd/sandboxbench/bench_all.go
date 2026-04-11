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
	allIter        int
	allConcurrency int
	allResultDir   string
	allLabel       string
	allSnapDir     string
)

func benchAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench-all",
		Short: "Run all benchmarks (cold-start, snapshot, concurrent)",
		RunE:  runBenchAll,
	}
	addVMFlags(cmd)
	cmd.Flags().IntVarP(&allIter, "iterations", "n", 5, "iterations per benchmark")
	cmd.Flags().IntVarP(&allConcurrency, "concurrency", "c", 5, "VMs for concurrent bench")
	cmd.Flags().StringVar(&allResultDir, "results-dir", "results", "directory for reports")
	cmd.Flags().StringVarP(&allLabel, "label", "l", "", "label included in filenames")
	cmd.Flags().StringVar(&allSnapDir, "snapshot-dir", "/tmp", "directory for snapshot files")
	return cmd
}

func runBenchAll(cmd *cobra.Command, args []string) error {
	_, opts, err := loadAndMerge()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(allResultDir, 0o755); err != nil {
		return fmt.Errorf("create results dir: %w", err)
	}

	meta := map[string]any{
		"go_os":   runtime.GOOS,
		"go_arch": runtime.GOARCH,
	}
	if allLabel != "" {
		meta["label"] = allLabel
	}

	bold := color.New(color.Bold).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()

	suffix := ""
	if allLabel != "" {
		suffix = "-" + allLabel
	}

	// ---- 1. Cold-start ----
	fmt.Printf("\n%s\n", bold("━━━ Cold Start ━━━"))

	coldMeta := copyMeta(meta)
	coldMeta["bench"] = "cold-start"
	coldReport, err := bench.RunColdStart(context.Background(), bench.Options{
		Iterations:      allIter,
		Opts:            opts,
		DisableValidate: noValidate,
		HostMeta:        coldMeta,
	})
	if err != nil {
		return fmt.Errorf("cold-start bench: %w", err)
	}

	coldPath := filepath.Join(allResultDir, fmt.Sprintf("cold-start%s.json", suffix))
	if err := bench.WriteJSON(coldPath, coldReport); err != nil {
		return err
	}

	fmt.Printf("  cold start  p50 = %s  p95 = %s\n",
		cyan(fmt.Sprintf("%.2f ms", coldReport.Summary.ColdStartP50Ms)),
		cyan(fmt.Sprintf("%.2f ms", coldReport.Summary.ColdStartP95Ms)))
	fmt.Printf("  teardown    p50 = %s  p95 = %s\n",
		cyan(fmt.Sprintf("%.2f ms", coldReport.Summary.TeardownP50Ms)),
		cyan(fmt.Sprintf("%.2f ms", coldReport.Summary.TeardownP95Ms)))

	// ---- 2. Snapshot/Restore ----
	fmt.Printf("\n%s\n", bold("━━━ Snapshot / Restore ━━━"))

	snapMeta := copyMeta(meta)
	snapMeta["bench"] = "snapshot-restore"
	snapReport, err := bench.RunSnapshotRestore(context.Background(), bench.SnapshotOptions{
		Iterations:      allIter,
		Opts:            opts,
		DisableValidate: noValidate,
		HostMeta:        snapMeta,
		SnapshotDir:     allSnapDir,
	})
	if err != nil {
		return fmt.Errorf("snapshot bench: %w", err)
	}

	snapPath := filepath.Join(allResultDir, fmt.Sprintf("snapshot%s.json", suffix))
	if err := bench.WriteJSON(snapPath, snapReport); err != nil {
		return err
	}

	fmt.Printf("  create      %s\n", cyan(fmt.Sprintf("%.2f ms", snapReport.SnapshotMs)))
	fmt.Printf("  restore     p50 = %s  p95 = %s\n",
		cyan(fmt.Sprintf("%.2f ms", snapReport.Summary.RestoreP50Ms)),
		cyan(fmt.Sprintf("%.2f ms", snapReport.Summary.RestoreP95Ms)))
	fmt.Printf("  teardown    p50 = %s  p95 = %s\n",
		cyan(fmt.Sprintf("%.2f ms", snapReport.Summary.TeardownP50Ms)),
		cyan(fmt.Sprintf("%.2f ms", snapReport.Summary.TeardownP95Ms)))

	// ---- 3. Concurrent ----
	fmt.Printf("\n%s\n", bold("━━━ Concurrent Spawn ━━━"))

	concMeta := copyMeta(meta)
	concMeta["bench"] = "concurrent"
	concMeta["concurrency"] = allConcurrency

	fmt.Printf("  spawning %d VMs in parallel...\n", allConcurrency)

	concReport, err := bench.RunConcurrent(context.Background(), bench.ConcurrentOptions{
		Concurrency:     allConcurrency,
		Opts:            opts,
		DisableValidate: noValidate,
		HostMeta:        concMeta,
	})
	if err != nil {
		return fmt.Errorf("concurrent bench: %w", err)
	}

	concPath := filepath.Join(allResultDir, fmt.Sprintf("concurrent-c%d%s.json", allConcurrency, suffix))
	if err := bench.WriteJSON(concPath, concReport); err != nil {
		return err
	}

	fmt.Printf("  wall time   %s\n", cyan(fmt.Sprintf("%.2f ms", concReport.TotalMs)))
	fmt.Printf("  cold start  p50 = %s  p95 = %s\n",
		cyan(fmt.Sprintf("%.2f ms", concReport.Summary.ColdStartP50Ms)),
		cyan(fmt.Sprintf("%.2f ms", concReport.Summary.ColdStartP95Ms)))
	fmt.Printf("  succeeded   %s  failed %s\n",
		green(fmt.Sprintf("%d", concReport.Summary.Succeeded)),
		red(fmt.Sprintf("%d", concReport.Summary.Failed)))

	// ---- Summary ----
	fmt.Printf("\n%s\n", bold("━━━ Summary ━━━"))
	fmt.Printf("  %-22s %s\n", "cold start p50:", cyan(fmt.Sprintf("%.2f ms", coldReport.Summary.ColdStartP50Ms)))
	fmt.Printf("  %-22s %s\n", "snapshot restore p50:", cyan(fmt.Sprintf("%.2f ms", snapReport.Summary.RestoreP50Ms)))
	fmt.Printf("  %-22s %s\n", "concurrent p50:", cyan(fmt.Sprintf("%.2f ms", concReport.Summary.ColdStartP50Ms)))
	fmt.Printf("  %-22s %sx\n", "snapshot speedup:", cyan(fmt.Sprintf("%.1f", coldReport.Summary.ColdStartP50Ms/snapReport.Summary.RestoreP50Ms)))

	fmt.Printf("\n  reports saved to %s\n", cyan(allResultDir+"/"))
	return nil
}

func copyMeta(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
