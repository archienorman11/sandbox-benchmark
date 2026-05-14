//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

const controlPort = 5000

type frame struct {
	Type string `json:"type"`
	Seq  int    `json:"seq,omitempty"`
	Kind string `json:"kind,omitempty"`
	Body string `json:"body,omitempty"`
}

type report struct {
	Timestamp   time.Time       `json:"timestamp"`
	Agents      int             `json:"agents"`
	Rounds      int             `json:"rounds"`
	Vcpus       int64           `json:"vcpus"`
	MemMIB      int64           `json:"mem_mib"`
	KernelImage string          `json:"kernel_image"`
	RootfsPath  string          `json:"rootfs_path"`
	Label       string          `json:"label,omitempty"`
	Startup     []startupSample `json:"startup"`
	RoundsData  []roundReport   `json:"rounds_data"`
	Summary     reportSummary   `json:"summary"`
	HostMeta    map[string]any  `json:"host_meta,omitempty"`
}

type startupSample struct {
	Index        int     `json:"index"`
	Role         string  `json:"role"`
	CID          uint32  `json:"cid"`
	VMStartMs    float64 `json:"vm_start_ms"`
	AgentReadyMs float64 `json:"agent_ready_ms"`
}

type roundReport struct {
	Round       int             `json:"round"`
	Plan        messageSample   `json:"plan"`
	Workers     []messageSample `json:"workers"`
	Critic      messageSample   `json:"critic"`
	TotalMs     float64         `json:"total_ms"`
	WorkerMaxMs float64         `json:"worker_max_ms"`
}

type messageSample struct {
	Seq          int     `json:"seq"`
	AgentIndex   int     `json:"agent_index"`
	Role         string  `json:"role"`
	Kind         string  `json:"kind"`
	LatencyMs    float64 `json:"latency_ms"`
	RequestBody  string  `json:"request_body,omitempty"`
	ResponseBody string  `json:"response_body,omitempty"`
}

type reportSummary struct {
	VMStartP50Ms    float64 `json:"vm_start_p50_ms"`
	VMStartP95Ms    float64 `json:"vm_start_p95_ms"`
	VMStartMaxMs    float64 `json:"vm_start_max_ms"`
	AgentReadyP50Ms float64 `json:"agent_ready_p50_ms"`
	AgentReadyP95Ms float64 `json:"agent_ready_p95_ms"`
	AgentReadyMaxMs float64 `json:"agent_ready_max_ms"`
	MessageP50Ms    float64 `json:"message_p50_ms"`
	MessageP95Ms    float64 `json:"message_p95_ms"`
	MessageMaxMs    float64 `json:"message_max_ms"`
	RoundP50Ms      float64 `json:"round_p50_ms"`
	RoundP95Ms      float64 `json:"round_p95_ms"`
	RoundMaxMs      float64 `json:"round_max_ms"`
}

type agent struct {
	index       int
	role        string
	cid         uint32
	apiSocket   string
	vsockPath   string
	consolePath string
	listener    net.Listener
	conn        net.Conn
	enc         *json.Encoder
	scanner     *bufio.Scanner
	machine     *fcsdk.Machine
	startedAt   time.Time
	vmStartedAt time.Time
	readyAt     time.Time
}

func main() {
	var (
		agents       = flag.Int("agents", 4, "number of sandbox agents")
		rounds       = flag.Int("rounds", 5, "message rounds after startup")
		kernel       = flag.String("kernel", "/opt/fc/vmlinux", "guest kernel path")
		rootfs       = flag.String("rootfs", "/opt/fc/swarm-rootfs.ext4", "rootfs containing /sandbox-agent")
		firecracker  = flag.String("firecracker", "firecracker", "firecracker binary")
		workDir      = flag.String("work-dir", "/tmp/swarmbench", "runtime directory")
		vcpus        = flag.Int64("vcpus", 1, "vCPUs per sandbox")
		memMIB       = flag.Int64("mem-mib", 128, "memory MiB per sandbox")
		keepRunning  = flag.Bool("keep-running", false, "leave VMs running after the run")
		startTimeout = flag.Duration("start-timeout", 15*time.Second, "agent ready timeout")
		outPath      = flag.String("out", "", "write JSON report to exact path")
		resultsDir   = flag.String("results-dir", "results", "directory to store timestamped reports")
		label        = flag.String("label", "", "optional label included in report filename and metadata")
		quiet        = flag.Bool("quiet", false, "suppress per-agent and per-message logs")
	)
	flag.Parse()

	if *agents < 1 {
		*agents = 1
	}
	if *rounds < 1 {
		*rounds = 1
	}
	if err := os.MkdirAll(*workDir, 0o755); err != nil {
		fatal(err)
	}

	ctx := context.Background()
	roles := assignRoles(*agents)
	as := make([]*agent, *agents)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	fmt.Printf("swarmbench: starting %d sandbox agents, rounds=%d\n", *agents, *rounds)
	fmt.Printf("kernel=%s rootfs=%s\n\n", *kernel, *rootfs)

	for i := 0; i < *agents; i++ {
		a := &agent{index: i, role: roles[i], cid: uint32(10 + i)}
		as[i] = a
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := startAgent(ctx, a, *workDir, *kernel, *rootfs, *firecracker, *vcpus, *memMIB, *startTimeout, *quiet); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		cleanup(ctx, as)
		fatal(firstErr)
	}

	sort.Slice(as, func(i, j int) bool { return as[i].index < as[j].index })
	printReadySummary(as)

	roundsData, err := runSwarm(as, *rounds, *quiet)
	if err != nil {
		cleanup(ctx, as)
		fatal(err)
	}

	rep := buildReport(as, roundsData, *agents, *rounds, *vcpus, *memMIB, *kernel, *rootfs, *label)
	savedPath, err := writeReport(*outPath, *resultsDir, rep)
	if err != nil {
		cleanup(ctx, as)
		fatal(err)
	}
	fmt.Printf("\n[report]\n  saved to %s\n", savedPath)

	if !*keepRunning {
		cleanup(ctx, as)
	}
}

func startAgent(ctx context.Context, a *agent, workDir, kernel, rootfs, fcBin string, vcpus, memMIB int64, timeout time.Duration, quiet bool) error {
	id := fmt.Sprintf("agent-%02d-%s", a.index, uuid.NewString()[:8])
	a.apiSocket = filepath.Join(workDir, id+".api.sock")
	a.vsockPath = filepath.Join(workDir, id+".vsock")
	listenPath := fmt.Sprintf("%s_%d", a.vsockPath, controlPort)
	_ = os.Remove(a.apiSocket)
	_ = os.Remove(a.vsockPath)
	_ = os.Remove(listenPath)

	ln, err := net.Listen("unix", listenPath)
	if err != nil {
		return fmt.Errorf("%s listen %s: %w", a.role, listenPath, err)
	}
	a.listener = ln

	cfg := fcsdk.Config{
		VMID:            id,
		SocketPath:      a.apiSocket,
		KernelImagePath: kernel,
		KernelArgs:      "reboot=k panic=1 pci=off root=/dev/vda ro console=ttyS0 init=/sandbox-agent",
		Drives: []models.Drive{{
			DriveID:      fcsdk.String("rootfs"),
			PathOnHost:   fcsdk.String(rootfs),
			IsRootDevice: fcsdk.Bool(true),
			IsReadOnly:   fcsdk.Bool(true),
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  fcsdk.Int64(vcpus),
			MemSizeMib: fcsdk.Int64(memMIB),
		},
		VsockDevices: []fcsdk.VsockDevice{{
			ID:   "vsock0",
			Path: a.vsockPath,
			CID:  a.cid,
		}},
		LogLevel: "Error",
		Seccomp:  fcsdk.SeccompConfig{Enabled: false},
	}

	cmd := fcsdk.VMCommandBuilder{}.
		WithBin(fcBin).
		WithSocketPath(cfg.SocketPath).
		AddArgs("--id", cfg.VMID, "--no-seccomp").
		Build(ctx)
	a.consolePath = filepath.Join(workDir, id+".console.log")
	console, err := os.Create(a.consolePath)
	if err != nil {
		return fmt.Errorf("%s create console log: %w", a.role, err)
	}
	defer console.Close()
	cmd.Stdout = console
	cmd.Stderr = console

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	m, err := fcsdk.NewMachine(ctx, cfg, fcsdk.WithProcessRunner(cmd), fcsdk.WithLogger(logrus.NewEntry(logger)))
	if err != nil {
		return fmt.Errorf("%s new machine: %w", a.role, err)
	}
	a.machine = m

	a.startedAt = time.Now()
	if err := m.Start(ctx); err != nil {
		return fmt.Errorf("%s start: %w", a.role, err)
	}
	a.vmStartedAt = time.Now()

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, err := ln.Accept()
		acceptCh <- acceptResult{conn: conn, err: err}
	}()

	select {
	case res := <-acceptCh:
		if res.err != nil {
			return fmt.Errorf("%s accept: %w", a.role, res.err)
		}
		a.conn = res.conn
	case <-time.After(timeout):
		return fmt.Errorf("%s did not connect within %s", a.role, timeout)
	}

	a.enc = json.NewEncoder(a.conn)
	a.scanner = bufio.NewScanner(a.conn)
	if !a.scanner.Scan() {
		return fmt.Errorf("%s no hello (console: %s)", a.role, a.consolePath)
	}
	var hello frame
	if err := json.Unmarshal(a.scanner.Bytes(), &hello); err != nil {
		return fmt.Errorf("%s hello decode: %w", a.role, err)
	}
	if hello.Type != "hello" {
		return fmt.Errorf("%s unexpected first frame: %s", a.role, hello.Type)
	}
	a.readyAt = time.Now()

	if !quiet {
		fmt.Printf("[ready] %-9s vm=%6.2fms ready=%6.2fms cid=%d\n",
			a.role,
			ms(a.vmStartedAt.Sub(a.startedAt)),
			ms(a.readyAt.Sub(a.startedAt)),
			a.cid,
		)
	}
	return nil
}

func runSwarm(as []*agent, rounds int, quiet bool) ([]roundReport, error) {
	planner := as[0]
	critic := as[len(as)-1]
	var workers []*agent
	if len(as) > 2 {
		workers = as[1 : len(as)-1]
	}

	var hops []float64
	reports := make([]roundReport, 0, rounds)
	seq := 1
	for r := 1; r <= rounds; r++ {
		roundStart := time.Now()
		if !quiet {
			fmt.Printf("\n[round %02d]\n", r)
		}
		plan, err := send(planner, seq, "plan", fmt.Sprintf("round %d: choose worker checks", r))
		if err != nil {
			return nil, err
		}
		hops = append(hops, plan.LatencyMs)
		if !quiet {
			fmt.Printf("  coordinator -> %-9s -> coordinator  %7.3fms  %s\n", planner.role, plan.LatencyMs, plan.ResponseBody)
		}
		seq++

		workerReports := make([]messageSample, len(workers))
		var workerBodies []string
		var workerMax float64
		if len(workers) > 0 {
			var wg sync.WaitGroup
			errCh := make(chan error, len(workers))
			for i, w := range workers {
				i, w, workerSeq := i, w, seq+i
				wg.Add(1)
				go func() {
					defer wg.Done()
					msg, err := send(w, workerSeq, "work", plan.ResponseBody)
					if err != nil {
						errCh <- err
						return
					}
					workerReports[i] = msg
				}()
			}
			wg.Wait()
			close(errCh)
			if err := <-errCh; err != nil {
				return nil, err
			}
			seq += len(workers)

			sort.Slice(workerReports, func(i, j int) bool {
				return workerReports[i].AgentIndex < workerReports[j].AgentIndex
			})
			workerBodies = make([]string, 0, len(workerReports))
			for _, msg := range workerReports {
				hops = append(hops, msg.LatencyMs)
				if msg.LatencyMs > workerMax {
					workerMax = msg.LatencyMs
				}
				workerBodies = append(workerBodies, msg.ResponseBody)
				if !quiet {
					fmt.Printf("  coordinator -> %-9s -> coordinator  %7.3fms  %s\n", msg.Role, msg.LatencyMs, msg.ResponseBody)
				}
			}
		}

		criticMsg, err := send(critic, seq, "critic", fmt.Sprintf("%v", workerBodies))
		if err != nil {
			return nil, err
		}
		hops = append(hops, criticMsg.LatencyMs)
		if !quiet {
			fmt.Printf("  coordinator -> %-9s -> coordinator  %7.3fms  %s\n", critic.role, criticMsg.LatencyMs, criticMsg.ResponseBody)
		}
		seq++

		reports = append(reports, roundReport{
			Round:       r,
			Plan:        plan,
			Workers:     workerReports,
			Critic:      criticMsg,
			TotalMs:     ms(time.Since(roundStart)),
			WorkerMaxMs: workerMax,
		})
	}

	sort.Float64s(hops)
	fmt.Printf("\n[message latency]\n")
	fmt.Printf("  samples=%d p50=%.3fms p95=%.3fms max=%.3fms\n", len(hops), percentile(hops, 0.50), percentile(hops, 0.95), hops[len(hops)-1])
	return reports, nil
}

func send(a *agent, seq int, kind, body string) (messageSample, error) {
	start := time.Now()
	if err := a.enc.Encode(frame{Type: "task", Seq: seq, Kind: kind, Body: body}); err != nil {
		return messageSample{}, fmt.Errorf("%s send: %w", a.role, err)
	}
	if !a.scanner.Scan() {
		return messageSample{}, fmt.Errorf("%s receive: %v", a.role, a.scanner.Err())
	}
	var resp frame
	if err := json.Unmarshal(a.scanner.Bytes(), &resp); err != nil {
		return messageSample{}, fmt.Errorf("%s decode: %w", a.role, err)
	}
	if resp.Seq != seq {
		return messageSample{}, fmt.Errorf("%s response seq mismatch: got %d want %d", a.role, resp.Seq, seq)
	}
	return messageSample{
		Seq:          seq,
		AgentIndex:   a.index,
		Role:         a.role,
		Kind:         kind,
		LatencyMs:    ms(time.Since(start)),
		RequestBody:  body,
		ResponseBody: resp.Body,
	}, nil
}

func cleanup(ctx context.Context, as []*agent) {
	for _, a := range as {
		if a == nil {
			continue
		}
		if a.enc != nil {
			_ = a.enc.Encode(frame{Type: "stop"})
		}
		if a.conn != nil {
			_ = a.conn.Close()
		}
		if a.listener != nil {
			_ = a.listener.Close()
		}
		if a.machine != nil {
			_ = a.machine.StopVMM()
			waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = a.machine.Wait(waitCtx)
			cancel()
		}
		_ = os.Remove(a.apiSocket)
		_ = os.Remove(a.vsockPath)
		_ = os.Remove(fmt.Sprintf("%s_%d", a.vsockPath, controlPort))
	}
}

func assignRoles(n int) []string {
	roles := make([]string, n)
	for i := 0; i < n; i++ {
		switch {
		case i == 0:
			roles[i] = "planner"
		case i == n-1 && n > 1:
			roles[i] = "critic"
		default:
			roles[i] = fmt.Sprintf("worker-%d", i)
		}
	}
	return roles
}

func printReadySummary(as []*agent) {
	var starts, ready []float64
	for _, a := range as {
		starts = append(starts, ms(a.vmStartedAt.Sub(a.startedAt)))
		ready = append(ready, ms(a.readyAt.Sub(a.startedAt)))
	}
	sort.Float64s(starts)
	sort.Float64s(ready)
	fmt.Printf("\n[startup latency]\n")
	fmt.Printf("  vm-start    p50=%.2fms p95=%.2fms max=%.2fms\n", percentile(starts, 0.50), percentile(starts, 0.95), starts[len(starts)-1])
	fmt.Printf("  agent-ready p50=%.2fms p95=%.2fms max=%.2fms\n", percentile(ready, 0.50), percentile(ready, 0.95), ready[len(ready)-1])
}

func buildReport(as []*agent, rounds []roundReport, agentCount, roundCount int, vcpus, memMIB int64, kernel, rootfs, label string) report {
	startup := make([]startupSample, 0, len(as))
	for _, a := range as {
		startup = append(startup, startupSample{
			Index:        a.index,
			Role:         a.role,
			CID:          a.cid,
			VMStartMs:    ms(a.vmStartedAt.Sub(a.startedAt)),
			AgentReadyMs: ms(a.readyAt.Sub(a.startedAt)),
		})
	}

	hostname, _ := os.Hostname()
	return report{
		Timestamp:   time.Now().UTC(),
		Agents:      agentCount,
		Rounds:      roundCount,
		Vcpus:       vcpus,
		MemMIB:      memMIB,
		KernelImage: kernel,
		RootfsPath:  rootfs,
		Label:       label,
		Startup:     startup,
		RoundsData:  rounds,
		Summary:     summarizeReport(startup, rounds),
		HostMeta: map[string]any{
			"hostname": hostname,
			"go_os":    runtime.GOOS,
			"go_arch":  runtime.GOARCH,
		},
	}
}

func summarizeReport(startup []startupSample, rounds []roundReport) reportSummary {
	var vmStarts, ready, messages, roundTotals []float64
	for _, s := range startup {
		vmStarts = append(vmStarts, s.VMStartMs)
		ready = append(ready, s.AgentReadyMs)
	}
	for _, r := range rounds {
		roundTotals = append(roundTotals, r.TotalMs)
		messages = append(messages, r.Plan.LatencyMs, r.Critic.LatencyMs)
		for _, w := range r.Workers {
			messages = append(messages, w.LatencyMs)
		}
	}

	sort.Float64s(vmStarts)
	sort.Float64s(ready)
	sort.Float64s(messages)
	sort.Float64s(roundTotals)

	return reportSummary{
		VMStartP50Ms:    percentile(vmStarts, 0.50),
		VMStartP95Ms:    percentile(vmStarts, 0.95),
		VMStartMaxMs:    maxSorted(vmStarts),
		AgentReadyP50Ms: percentile(ready, 0.50),
		AgentReadyP95Ms: percentile(ready, 0.95),
		AgentReadyMaxMs: maxSorted(ready),
		MessageP50Ms:    percentile(messages, 0.50),
		MessageP95Ms:    percentile(messages, 0.95),
		MessageMaxMs:    maxSorted(messages),
		RoundP50Ms:      percentile(roundTotals, 0.50),
		RoundP95Ms:      percentile(roundTotals, 0.95),
		RoundMaxMs:      maxSorted(roundTotals),
	}
}

func writeReport(outPath, resultsDir string, rep report) (string, error) {
	if outPath == "" {
		if err := os.MkdirAll(resultsDir, 0o755); err != nil {
			return "", fmt.Errorf("create results dir: %w", err)
		}
		ts := rep.Timestamp.Format("20060102-150405")
		name := fmt.Sprintf("swarm-%s-a%d-r%d", ts, rep.Agents, rep.Rounds)
		if rep.Label != "" {
			name += "-" + rep.Label
		}
		outPath = filepath.Join(resultsDir, name+".json")
	}

	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func maxSorted(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[len(sorted)-1]
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
