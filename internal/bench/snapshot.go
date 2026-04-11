package bench

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ayush6624/sandbox-benchmark/internal/firecracker"
)

// SnapshotReport holds results for snapshot create + restore benchmarks.
type SnapshotReport struct {
	Timestamp      time.Time      `json:"timestamp"`
	Iterations     int            `json:"iterations"`
	SnapshotMs     float64        `json:"snapshot_create_ms"`
	RestoreMs      []float64      `json:"restore_ms"`
	TeardownMs     []float64      `json:"teardown_ms"`
	Summary        SnapshotSummary `json:"summary"`
	HostMeta       map[string]any `json:"host_meta,omitempty"`
}

// SnapshotSummary holds percentile stats for restore benchmarks.
type SnapshotSummary struct {
	RestoreP50Ms  float64 `json:"restore_p50_ms"`
	RestoreP95Ms  float64 `json:"restore_p95_ms"`
	TeardownP50Ms float64 `json:"teardown_p50_ms"`
	TeardownP95Ms float64 `json:"teardown_p95_ms"`
}

// SnapshotOptions configures the snapshot benchmark.
type SnapshotOptions struct {
	Iterations      int
	Opts            firecracker.RunOptions
	DisableValidate bool
	HostMeta        map[string]any
	SnapshotDir     string // directory to store snapshot files
}

// RunSnapshotRestore boots a VM once, snapshots it, then measures restore time.
//
// Flow:
//  1. Cold-boot a VM with the given config
//  2. Pause the VM
//  3. Create a full snapshot (measured as snapshot_create_ms)
//  4. Tear down the source VM
//  5. For each iteration: restore from snapshot, measure time, tear down
func RunSnapshotRestore(ctx context.Context, o SnapshotOptions) (SnapshotReport, error) {
	if o.Iterations < 1 {
		o.Iterations = 3
	}
	if o.SnapshotDir == "" {
		o.SnapshotDir = os.TempDir()
	}

	report := SnapshotReport{
		Timestamp:  time.Now().UTC(),
		Iterations: o.Iterations,
		HostMeta:   o.HostMeta,
		RestoreMs:  make([]float64, 0, o.Iterations),
		TeardownMs: make([]float64, 0, o.Iterations),
	}

	snapPaths := firecracker.SnapshotPaths{
		SnapshotPath: filepath.Join(o.SnapshotDir, "bench-snapshot"),
		MemFilePath:  filepath.Join(o.SnapshotDir, "bench-memory"),
	}

	// --- Phase 1: Boot source VM and create snapshot ---
	fmt.Println("  creating snapshot from fresh VM...")

	sourceOpts := o.Opts
	sourceOpts.SocketPath = ""
	m, _, err := firecracker.NewMachine(ctx, sourceOpts, o.DisableValidate)
	if err != nil {
		return report, fmt.Errorf("source NewMachine: %w", err)
	}
	if err := firecracker.Start(ctx, m); err != nil {
		cleanup(m)
		return report, fmt.Errorf("source Start: %w", err)
	}

	// Pause before snapshot
	if err := firecracker.Pause(ctx, m); err != nil {
		cleanup(m)
		return report, fmt.Errorf("source Pause: %w", err)
	}

	// Create snapshot (timed)
	t0 := time.Now()
	if err := firecracker.CreateSnapshot(ctx, m, snapPaths); err != nil {
		cleanup(m)
		return report, fmt.Errorf("CreateSnapshot: %w", err)
	}
	report.SnapshotMs = float64(time.Since(t0).Microseconds()) / 1000.0

	// Tear down source VM
	cleanup(m)

	defer func() {
		os.Remove(snapPaths.SnapshotPath)
		os.Remove(snapPaths.MemFilePath)
	}()

	fmt.Printf("  snapshot created in %.2f ms\n", report.SnapshotMs)

	// --- Phase 2: Restore iterations ---
	// Each restore needs its own copy of the rootfs (source VM modified it)
	// and mem file (Firecracker may consume it on load).
	for i := 0; i < o.Iterations; i++ {
		// Copy rootfs for this iteration
		iterRootfs := filepath.Join(o.SnapshotDir, fmt.Sprintf("bench-rootfs-%d.ext4", i))
		if err := copyFile(o.Opts.RootfsPath, iterRootfs); err != nil {
			return report, fmt.Errorf("iteration %d copy rootfs: %w", i, err)
		}

		// Copy mem file for this iteration
		iterMem := filepath.Join(o.SnapshotDir, fmt.Sprintf("bench-memory-%d", i))
		if err := copyFile(snapPaths.MemFilePath, iterMem); err != nil {
			os.Remove(iterRootfs)
			return report, fmt.Errorf("iteration %d copy mem: %w", i, err)
		}

		iterSnap := firecracker.SnapshotPaths{
			SnapshotPath: snapPaths.SnapshotPath,
			MemFilePath:  iterMem,
		}

		opts := o.Opts
		opts.SocketPath = ""
		opts.RootfsPath = iterRootfs

		t0 := time.Now()
		rm, _, err := firecracker.NewMachineFromSnapshot(ctx, opts, iterSnap, o.DisableValidate)
		if err != nil {
			os.Remove(iterRootfs)
			os.Remove(iterMem)
			return report, fmt.Errorf("iteration %d NewMachineFromSnapshot: %w", i, err)
		}
		if err := firecracker.Start(ctx, rm); err != nil {
			cleanup(rm)
			os.Remove(iterRootfs)
			os.Remove(iterMem)
			return report, fmt.Errorf("iteration %d Start (restore): %w", i, err)
		}
		report.RestoreMs = append(report.RestoreMs, float64(time.Since(t0).Microseconds())/1000.0)

		t1 := time.Now()
		if err := firecracker.StopForce(rm); err != nil {
			os.Remove(iterRootfs)
			os.Remove(iterMem)
			return report, fmt.Errorf("iteration %d StopVMM: %w", i, err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err = firecracker.Wait(waitCtx, rm)
		cancel()
		if err != nil && !isSignalError(err) {
			os.Remove(iterRootfs)
			os.Remove(iterMem)
			return report, fmt.Errorf("iteration %d Wait: %w", i, err)
		}
		report.TeardownMs = append(report.TeardownMs, float64(time.Since(t1).Microseconds())/1000.0)

		os.Remove(iterRootfs)
		os.Remove(iterMem)
	}

	report.Summary = snapshotSummarize(report.RestoreMs, report.TeardownMs)
	return report, nil
}

func snapshotSummarize(restore, teardown []float64) SnapshotSummary {
	s := SnapshotSummary{}
	if len(restore) > 0 {
		s.RestoreP50Ms = percentile(copySort(restore), 0.50)
		s.RestoreP95Ms = percentile(copySort(restore), 0.95)
	}
	if len(teardown) > 0 {
		s.TeardownP50Ms = percentile(copySort(teardown), 0.50)
		s.TeardownP95Ms = percentile(copySort(teardown), 0.95)
	}
	return s
}

func cleanup(m *firecracker.Machine) {
	_ = firecracker.StopForce(m)
	_ = firecracker.Wait(context.Background(), m)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
