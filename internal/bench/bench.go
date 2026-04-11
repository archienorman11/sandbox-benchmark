package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ayush6624/sandbox-benchmark/internal/firecracker"
)

// Report is written as JSON (one object) for each bench invocation.
type Report struct {
	Timestamp   time.Time      `json:"timestamp"`
	Iterations  int            `json:"iterations"`
	ColdStartMs []float64     `json:"cold_start_ms"`
	TeardownMs  []float64     `json:"teardown_ms"`
	Summary     Summary        `json:"summary"`
	HostMeta    map[string]any `json:"host_meta,omitempty"`
}

// Summary holds simple statistics over cold start and teardown samples.
type Summary struct {
	ColdStartP50Ms float64 `json:"cold_start_p50_ms"`
	ColdStartP95Ms float64 `json:"cold_start_p95_ms"`
	TeardownP50Ms  float64 `json:"teardown_p50_ms"`
	TeardownP95Ms  float64 `json:"teardown_p95_ms"`
}

// Options configures the benchmark loop (paths must exist on the Linux host).
type Options struct {
	Iterations      int
	Opts            firecracker.RunOptions
	DisableValidate bool
	HostMeta        map[string]any
}

// RunColdStart measures full Machine.Start (VMM boot + InstanceStart) and StopVMM + Wait per iteration.
func RunColdStart(ctx context.Context, o Options) (Report, error) {
	if o.Iterations < 1 {
		o.Iterations = 3
	}
	report := Report{
		Timestamp:  time.Now().UTC(),
		Iterations: o.Iterations,
		HostMeta:   o.HostMeta,
		ColdStartMs: make([]float64, 0, o.Iterations),
		TeardownMs:  make([]float64, 0, o.Iterations),
	}

	for i := 0; i < o.Iterations; i++ {
		opts := o.Opts
		opts.SocketPath = ""

		t0 := time.Now()
		m, _, err := firecracker.NewMachine(ctx, opts, o.DisableValidate)
		if err != nil {
			return report, fmt.Errorf("iteration %d NewMachine: %w", i, err)
		}
		if err := firecracker.Start(ctx, m); err != nil {
			_ = firecracker.StopForce(m)
			_ = firecracker.Wait(context.Background(), m)
			return report, fmt.Errorf("iteration %d Start: %w", i, err)
		}
		report.ColdStartMs = append(report.ColdStartMs, float64(time.Since(t0).Microseconds()) / 1000.0)

		t1 := time.Now()
		if err := firecracker.StopForce(m); err != nil {
			return report, fmt.Errorf("iteration %d StopVMM: %w", i, err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err = firecracker.Wait(waitCtx, m)
		cancel()
		if err != nil && !isSignalError(err) {
			return report, fmt.Errorf("iteration %d Wait: %w", i, err)
		}
		report.TeardownMs = append(report.TeardownMs, float64(time.Since(t1).Microseconds()) / 1000.0)
	}

	report.Summary = summarize(report.ColdStartMs, report.TeardownMs)
	return report, nil
}

// isSignalError returns true if the error is from a process killed by a signal
// (expected when we StopForce/SIGTERM the VM).
func isSignalError(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return true
	}
	// The SDK wraps errors in a multierror; check the message as fallback.
	return strings.Contains(err.Error(), "signal:")
}

func summarize(cold, teardown []float64) Summary {
	s := Summary{}
	if len(cold) > 0 {
		s.ColdStartP50Ms = percentile(copySort(cold), 0.50)
		s.ColdStartP95Ms = percentile(copySort(cold), 0.95)
	}
	if len(teardown) > 0 {
		s.TeardownP50Ms = percentile(copySort(teardown), 0.50)
		s.TeardownP95Ms = percentile(copySort(teardown), 0.95)
	}
	return s
}

func copySort(x []float64) []float64 {
	y := append([]float64(nil), x...)
	sort.Float64s(y)
	return y
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// WriteJSON writes any report struct to path as indented JSON.
func WriteJSON(path string, r any) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
