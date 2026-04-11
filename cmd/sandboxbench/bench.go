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
	benchIter      int
	benchOut       string
	benchResultDir string
	benchLabel     string
)

func benchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Run cold-start / teardown benchmark loop",
		RunE:  runBench,
	}
	addVMFlags(cmd)
	cmd.Flags().IntVarP(&benchIter, "iterations", "n", 3, "number of benchmark iterations")
	cmd.Flags().StringVarP(&benchOut, "out", "o", "", "write JSON report to exact path (overrides --results-dir)")
	cmd.Flags().StringVar(&benchResultDir, "results-dir", "results", "directory to store timestamped reports")
	cmd.Flags().StringVarP(&benchLabel, "label", "l", "", "optional label included in filename (e.g. baseline, no-systemd)")
	return cmd
}

func runBench(cmd *cobra.Command, args []string) error {
	_, opts, err := loadAndMerge()
	if err != nil {
		return err
	}

	meta := map[string]any{
		"go_os":   runtime.GOOS,
		"go_arch": runtime.GOARCH,
	}
	if benchLabel != "" {
		meta["label"] = benchLabel
	}

	report, err := bench.RunColdStart(context.Background(), bench.Options{
		Iterations:      benchIter,
		Opts:            opts,
		DisableValidate: noValidate,
		HostMeta:        meta,
	})
	if err != nil {
		return err
	}

	// Determine output path
	outPath := benchOut
	if outPath == "" {
		if err := os.MkdirAll(benchResultDir, 0o755); err != nil {
			return fmt.Errorf("create results dir: %w", err)
		}
		ts := report.Timestamp.Format("20060102-150405")
		name := "bench-" + ts
		if benchLabel != "" {
			name += "-" + benchLabel
		}
		name += ".json"
		outPath = filepath.Join(benchResultDir, name)
	}

	if err := bench.WriteJSON(outPath, report); err != nil {
		return err
	}

	// Print summary to stdout
	bold := color.New(color.Bold).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	fmt.Printf("\n  %s  p50 = %s  p95 = %s\n", bold("cold start"), cyan(fmt.Sprintf("%.2f ms", report.Summary.ColdStartP50Ms)), cyan(fmt.Sprintf("%.2f ms", report.Summary.ColdStartP95Ms)))
	fmt.Printf("  %s    p50 = %s  p95 = %s\n", bold("teardown"), cyan(fmt.Sprintf("%.2f ms", report.Summary.TeardownP50Ms)), cyan(fmt.Sprintf("%.2f ms", report.Summary.TeardownP95Ms)))
	fmt.Printf("  %s  %d\n", bold("iterations"), report.Iterations)
	fmt.Printf("\n  report saved to %s\n", cyan(outPath))
	return nil
}
