package bench

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ayush6624/sandbox-benchmark/internal/firecracker"
)

// ConcurrentReport holds results for parallel VM spawn benchmarks.
type ConcurrentReport struct {
	Timestamp   time.Time          `json:"timestamp"`
	Concurrency int                `json:"concurrency"`
	TotalMs     float64            `json:"total_ms"`
	PerVM       []ConcurrentResult `json:"per_vm"`
	Summary     ConcurrentSummary  `json:"summary"`
	HostMeta    map[string]any     `json:"host_meta,omitempty"`
}

// ConcurrentResult is the timing for a single VM in a concurrent batch.
type ConcurrentResult struct {
	Index       int     `json:"index"`
	ColdStartMs float64 `json:"cold_start_ms"`
	TeardownMs  float64 `json:"teardown_ms"`
	Error       string  `json:"error,omitempty"`
}

// ConcurrentSummary holds aggregate stats.
type ConcurrentSummary struct {
	Succeeded      int     `json:"succeeded"`
	Failed         int     `json:"failed"`
	ColdStartP50Ms float64 `json:"cold_start_p50_ms"`
	ColdStartP95Ms float64 `json:"cold_start_p95_ms"`
}

// ConcurrentOptions configures the concurrent spawn benchmark.
type ConcurrentOptions struct {
	Concurrency     int // number of VMs to spawn in parallel
	Opts            firecracker.RunOptions
	DisableValidate bool
	HostMeta        map[string]any
}

// RunConcurrent spawns N VMs simultaneously and measures per-VM cold-start.
func RunConcurrent(ctx context.Context, o ConcurrentOptions) (ConcurrentReport, error) {
	if o.Concurrency < 1 {
		o.Concurrency = 5
	}

	report := ConcurrentReport{
		Timestamp:   time.Now().UTC(),
		Concurrency: o.Concurrency,
		HostMeta:    o.HostMeta,
		PerVM:       make([]ConcurrentResult, o.Concurrency),
	}

	var wg sync.WaitGroup
	wg.Add(o.Concurrency)

	tTotal := time.Now()

	for i := 0; i < o.Concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			result := ConcurrentResult{Index: idx}

			opts := o.Opts
			opts.SocketPath = "" // unique per VM

			t0 := time.Now()
			m, _, err := firecracker.NewMachine(ctx, opts, o.DisableValidate)
			if err != nil {
				result.Error = fmt.Sprintf("NewMachine: %v", err)
				report.PerVM[idx] = result
				return
			}
			if err := firecracker.Start(ctx, m); err != nil {
				cleanup(m)
				result.Error = fmt.Sprintf("Start: %v", err)
				report.PerVM[idx] = result
				return
			}
			result.ColdStartMs = float64(time.Since(t0).Microseconds()) / 1000.0

			t1 := time.Now()
			_ = firecracker.StopForce(m)
			waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			_ = firecracker.Wait(waitCtx, m)
			cancel()
			result.TeardownMs = float64(time.Since(t1).Microseconds()) / 1000.0

			report.PerVM[idx] = result
		}(i)
	}

	wg.Wait()
	report.TotalMs = float64(time.Since(tTotal).Microseconds()) / 1000.0

	// Summarize
	var coldStarts []float64
	succeeded, failed := 0, 0
	for _, r := range report.PerVM {
		if r.Error != "" {
			failed++
		} else {
			succeeded++
			coldStarts = append(coldStarts, r.ColdStartMs)
		}
	}
	report.Summary.Succeeded = succeeded
	report.Summary.Failed = failed
	if len(coldStarts) > 0 {
		report.Summary.ColdStartP50Ms = percentile(copySort(coldStarts), 0.50)
		report.Summary.ColdStartP95Ms = percentile(copySort(coldStarts), 0.95)
	}

	return report, nil
}
