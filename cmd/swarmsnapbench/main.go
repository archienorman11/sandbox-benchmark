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

type agentVM struct {
	id          string
	cid         uint32
	apiSocket   string
	vsockPath   string
	consolePath string
	listener    net.Listener
	conn        net.Conn
	enc         *json.Encoder
	scanner     *bufio.Scanner
	machine     *fcsdk.Machine
	console     *os.File
}

type report struct {
	Timestamp        time.Time       `json:"timestamp"`
	Iterations       int             `json:"iterations"`
	Vcpus            int64           `json:"vcpus"`
	MemMIB           int64           `json:"mem_mib"`
	KernelImage      string          `json:"kernel_image"`
	RootfsPath       string          `json:"rootfs_path"`
	Label            string          `json:"label,omitempty"`
	Source           sourceSample    `json:"source"`
	SnapshotCreateMs float64         `json:"snapshot_create_ms"`
	Restore          []restoreSample `json:"restore"`
	Summary          reportSummary   `json:"summary"`
	HostMeta         map[string]any  `json:"host_meta,omitempty"`
}

type sourceSample struct {
	VMStartMs       float64 `json:"vm_start_ms"`
	AgentReadyMs    float64 `json:"agent_ready_ms"`
	PrepareMs       float64 `json:"prepare_ms"`
	SettleMs        float64 `json:"settle_ms"`
	SnapshotPauseMs float64 `json:"snapshot_pause_ms"`
}

type restoreSample struct {
	Iteration      int     `json:"iteration"`
	RestoreStartMs float64 `json:"restore_start_ms"`
	AgentReadyMs   float64 `json:"agent_ready_ms"`
	PingMs         float64 `json:"ping_ms"`
	TeardownMs     float64 `json:"teardown_ms"`
}

type reportSummary struct {
	RestoreStartP50Ms float64 `json:"restore_start_p50_ms"`
	RestoreStartP95Ms float64 `json:"restore_start_p95_ms"`
	RestoreStartMaxMs float64 `json:"restore_start_max_ms"`
	AgentReadyP50Ms   float64 `json:"agent_ready_p50_ms"`
	AgentReadyP95Ms   float64 `json:"agent_ready_p95_ms"`
	AgentReadyMaxMs   float64 `json:"agent_ready_max_ms"`
	PingP50Ms         float64 `json:"ping_p50_ms"`
	PingP95Ms         float64 `json:"ping_p95_ms"`
	PingMaxMs         float64 `json:"ping_max_ms"`
	TeardownP50Ms     float64 `json:"teardown_p50_ms"`
	TeardownP95Ms     float64 `json:"teardown_p95_ms"`
	TeardownMaxMs     float64 `json:"teardown_max_ms"`
}

func main() {
	var (
		iterations   = flag.Int("iterations", 10, "number of snapshot restore iterations")
		kernel       = flag.String("kernel", "/opt/fc/vmlinux", "guest kernel path")
		rootfs       = flag.String("rootfs", "/opt/fc/swarm-rootfs.ext4", "rootfs containing /sandbox-agent")
		firecracker  = flag.String("firecracker", "firecracker", "firecracker binary")
		workDir      = flag.String("work-dir", "/tmp/swarmsnapbench", "runtime directory")
		snapshotDir  = flag.String("snapshot-dir", "/tmp/swarmsnapbench-snapshot", "snapshot file directory")
		vcpus        = flag.Int64("vcpus", 1, "vCPUs per sandbox")
		memMIB       = flag.Int64("mem-mib", 128, "memory MiB per sandbox")
		readyTimeout = flag.Duration("ready-timeout", 10*time.Second, "agent ready timeout")
		settle       = flag.Duration("settle", 25*time.Millisecond, "time to let agent enter reconnect loop before pause")
		outPath      = flag.String("out", "", "write JSON report to exact path")
		resultsDir   = flag.String("results-dir", "results", "directory to store timestamped reports")
		label        = flag.String("label", "", "optional label included in report filename and metadata")
		keepSnapshot = flag.Bool("keep-snapshot", false, "keep snapshot files after the run")
		quiet        = flag.Bool("quiet", false, "suppress per-iteration logs")
	)
	flag.Parse()

	if *iterations < 1 {
		*iterations = 1
	}
	if err := os.MkdirAll(*workDir, 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*snapshotDir, 0o755); err != nil {
		fatal(err)
	}

	ctx := context.Background()
	snapshotPath := filepath.Join(*snapshotDir, "swarm-agent.snapshot")
	memPath := filepath.Join(*snapshotDir, "swarm-agent.mem")
	_ = os.Remove(snapshotPath)
	_ = os.Remove(memPath)
	if !*keepSnapshot {
		defer os.Remove(snapshotPath)
		defer os.Remove(memPath)
	}

	fmt.Printf("swarmsnapbench: iterations=%d\n", *iterations)
	fmt.Printf("kernel=%s rootfs=%s\n\n", *kernel, *rootfs)

	source, src, snapshotMs, err := createReadySnapshot(ctx, *workDir, *kernel, *rootfs, *firecracker, *vcpus, *memMIB, *readyTimeout, *settle, snapshotPath, memPath, *quiet)
	if err != nil {
		cleanup(source)
		fatal(err)
	}
	cleanup(source)

	var samples []restoreSample
	for i := 0; i < *iterations; i++ {
		sample, err := restoreOnce(ctx, i, *workDir, *kernel, *rootfs, *firecracker, *vcpus, *memMIB, *readyTimeout, snapshotPath, memPath, source.vsockPath, *quiet)
		if err != nil {
			fatal(err)
		}
		samples = append(samples, sample)
	}

	rep := buildReport(*iterations, *vcpus, *memMIB, *kernel, *rootfs, *label, src, snapshotMs, samples)
	savedPath, err := writeReport(*outPath, *resultsDir, rep)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\n[report]\n  saved to %s\n", savedPath)
}

func createReadySnapshot(ctx context.Context, workDir, kernel, rootfs, fcBin string, vcpus, memMIB int64, timeout, settle time.Duration, snapshotPath, memPath string, quiet bool) (*agentVM, sourceSample, float64, error) {
	vm, err := newAgentVM(ctx, "source", 10, workDir, "", kernel, rootfs, fcBin, vcpus, memMIB, "", "")
	if err != nil {
		return nil, sourceSample{}, 0, err
	}

	startedAt := time.Now()
	if err := vm.machine.Start(ctx); err != nil {
		return vm, sourceSample{}, 0, fmt.Errorf("source start: %w", err)
	}
	vmStartMs := ms(time.Since(startedAt))

	if err := vm.acceptHello(timeout); err != nil {
		return vm, sourceSample{}, 0, fmt.Errorf("source ready: %w", err)
	}
	readyMs := ms(time.Since(startedAt))

	prepareStart := time.Now()
	if err := vm.enc.Encode(frame{Type: "prepare_snapshot", Seq: 1}); err != nil {
		return vm, sourceSample{}, 0, fmt.Errorf("prepare send: %w", err)
	}
	if !vm.scanner.Scan() {
		return vm, sourceSample{}, 0, fmt.Errorf("prepare receive: %v", vm.scanner.Err())
	}
	var prepared frame
	if err := json.Unmarshal(vm.scanner.Bytes(), &prepared); err != nil {
		return vm, sourceSample{}, 0, fmt.Errorf("prepare decode: %w", err)
	}
	if prepared.Type != "snapshot_ready" {
		return vm, sourceSample{}, 0, fmt.Errorf("unexpected prepare response: %s", prepared.Type)
	}
	prepareMs := ms(time.Since(prepareStart))

	_ = vm.conn.Close()
	vm.conn = nil
	vm.enc = nil
	vm.scanner = nil
	_ = vm.listener.Close()
	vm.listener = nil
	_ = os.Remove(listenPath(vm.vsockPath))

	time.Sleep(settle)

	pauseStart := time.Now()
	if err := vm.machine.PauseVM(ctx); err != nil {
		return vm, sourceSample{}, 0, fmt.Errorf("pause source: %w", err)
	}
	pauseMs := ms(time.Since(pauseStart))

	snapshotStart := time.Now()
	if err := vm.machine.CreateSnapshot(ctx, memPath, snapshotPath); err != nil {
		return vm, sourceSample{}, 0, fmt.Errorf("create snapshot: %w", err)
	}
	snapshotMs := ms(time.Since(snapshotStart))

	src := sourceSample{
		VMStartMs:       vmStartMs,
		AgentReadyMs:    readyMs,
		PrepareMs:       prepareMs,
		SettleMs:        ms(settle),
		SnapshotPauseMs: pauseMs,
	}
	if !quiet {
		fmt.Printf("[source] vm=%6.2fms ready=%6.2fms prepare=%6.2fms pause=%6.2fms snapshot=%6.2fms\n", vmStartMs, readyMs, prepareMs, pauseMs, snapshotMs)
	}
	return vm, src, snapshotMs, nil
}

func restoreOnce(ctx context.Context, iteration int, workDir, kernel, rootfs, fcBin string, vcpus, memMIB int64, timeout time.Duration, snapshotPath, memPath, snapshotVsockPath string, quiet bool) (restoreSample, error) {
	iterMem := filepath.Join(workDir, fmt.Sprintf("restore-%03d.mem", iteration))
	if err := copyFile(memPath, iterMem); err != nil {
		return restoreSample{}, fmt.Errorf("copy mem for iteration %d: %w", iteration, err)
	}
	defer os.Remove(iterMem)

	vm, err := newAgentVM(ctx, fmt.Sprintf("restore-%03d", iteration), 10, workDir, snapshotVsockPath, kernel, rootfs, fcBin, vcpus, memMIB, snapshotPath, iterMem)
	if err != nil {
		return restoreSample{}, err
	}
	defer cleanup(vm)

	startedAt := time.Now()
	if err := vm.machine.Start(ctx); err != nil {
		return restoreSample{}, fmt.Errorf("restore %d start: %w", iteration, err)
	}
	restoreStartMs := ms(time.Since(startedAt))

	if err := vm.acceptHello(timeout); err != nil {
		return restoreSample{}, fmt.Errorf("restore %d ready: %w", iteration, err)
	}
	readyMs := ms(time.Since(startedAt))

	pingStart := time.Now()
	if err := vm.enc.Encode(frame{Type: "task", Seq: 1, Kind: "ping", Body: "snapshot restore ping"}); err != nil {
		return restoreSample{}, fmt.Errorf("restore %d ping send: %w", iteration, err)
	}
	if !vm.scanner.Scan() {
		return restoreSample{}, fmt.Errorf("restore %d ping receive: %v", iteration, vm.scanner.Err())
	}
	pingMs := ms(time.Since(pingStart))

	teardownStart := time.Now()
	cleanup(vm)
	teardownMs := ms(time.Since(teardownStart))

	sample := restoreSample{
		Iteration:      iteration,
		RestoreStartMs: restoreStartMs,
		AgentReadyMs:   readyMs,
		PingMs:         pingMs,
		TeardownMs:     teardownMs,
	}
	if !quiet {
		fmt.Printf("[restore %03d] start=%6.2fms ready=%6.2fms ping=%6.3fms teardown=%6.2fms\n", iteration, restoreStartMs, readyMs, pingMs, teardownMs)
	}
	return sample, nil
}

func newAgentVM(ctx context.Context, name string, cid uint32, workDir, vsockPath, kernel, rootfs, fcBin string, vcpus, memMIB int64, snapshotPath, memPath string) (*agentVM, error) {
	id := fmt.Sprintf("%s-%s", name, uuid.NewString()[:8])
	if vsockPath == "" {
		vsockPath = filepath.Join(workDir, id+".vsock")
	}
	vm := &agentVM{
		id:          id,
		cid:         cid,
		apiSocket:   filepath.Join(workDir, id+".api.sock"),
		vsockPath:   vsockPath,
		consolePath: filepath.Join(workDir, id+".console.log"),
	}
	_ = os.Remove(vm.apiSocket)
	_ = os.Remove(vm.vsockPath)
	_ = os.Remove(listenPath(vm.vsockPath))

	ln, err := net.Listen("unix", listenPath(vm.vsockPath))
	if err != nil {
		return nil, fmt.Errorf("%s listen: %w", name, err)
	}
	vm.listener = ln

	cfg := fcsdk.Config{
		VMID:            id,
		SocketPath:      vm.apiSocket,
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
		LogLevel: "Error",
		Seccomp:  fcsdk.SeccompConfig{Enabled: false},
	}
	if snapshotPath == "" {
		cfg.VsockDevices = []fcsdk.VsockDevice{{
			ID:   "vsock0",
			Path: vm.vsockPath,
			CID:  cid,
		}}
	}

	cmd := fcsdk.VMCommandBuilder{}.
		WithBin(fcBin).
		WithSocketPath(cfg.SocketPath).
		AddArgs("--id", cfg.VMID, "--no-seccomp").
		Build(ctx)
	console, err := os.Create(vm.consolePath)
	if err != nil {
		return nil, fmt.Errorf("%s create console log: %w", name, err)
	}
	cmd.Stdout = console
	cmd.Stderr = console
	vm.console = console

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	opts := []fcsdk.Opt{
		fcsdk.WithProcessRunner(cmd),
		fcsdk.WithLogger(logrus.NewEntry(logger)),
	}
	if snapshotPath != "" {
		opts = append(opts, fcsdk.WithSnapshot(memPath, snapshotPath, func(c *fcsdk.SnapshotConfig) {
			c.ResumeVM = true
		}))
	}
	m, err := fcsdk.NewMachine(ctx, cfg, opts...)
	if err != nil {
		_ = console.Close()
		vm.console = nil
		return nil, fmt.Errorf("%s new machine: %w", name, err)
	}
	vm.machine = m
	return vm, nil
}

func (vm *agentVM) acceptHello(timeout time.Duration) error {
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, err := vm.listener.Accept()
		acceptCh <- acceptResult{conn: conn, err: err}
	}()

	select {
	case res := <-acceptCh:
		if res.err != nil {
			return res.err
		}
		vm.conn = res.conn
	case <-time.After(timeout):
		return fmt.Errorf("timed out after %s (console: %s)", timeout, vm.consolePath)
	}

	vm.enc = json.NewEncoder(vm.conn)
	vm.scanner = bufio.NewScanner(vm.conn)
	if !vm.scanner.Scan() {
		return fmt.Errorf("no hello: %v", vm.scanner.Err())
	}
	var hello frame
	if err := json.Unmarshal(vm.scanner.Bytes(), &hello); err != nil {
		return err
	}
	if hello.Type != "hello" {
		return fmt.Errorf("unexpected first frame: %s", hello.Type)
	}
	return nil
}

func cleanup(vm *agentVM) {
	if vm == nil {
		return
	}
	if vm.enc != nil {
		_ = vm.enc.Encode(frame{Type: "stop"})
	}
	if vm.conn != nil {
		_ = vm.conn.Close()
		vm.conn = nil
	}
	if vm.listener != nil {
		_ = vm.listener.Close()
		vm.listener = nil
	}
	if vm.machine != nil {
		_ = vm.machine.StopVMM()
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = vm.machine.Wait(waitCtx)
		cancel()
		vm.machine = nil
	}
	if vm.console != nil {
		_ = vm.console.Close()
		vm.console = nil
	}
	_ = os.Remove(vm.apiSocket)
	_ = os.Remove(vm.vsockPath)
	_ = os.Remove(listenPath(vm.vsockPath))
}

func buildReport(iterations int, vcpus, memMIB int64, kernel, rootfs, label string, source sourceSample, snapshotMs float64, samples []restoreSample) report {
	hostname, _ := os.Hostname()
	rep := report{
		Timestamp:        time.Now().UTC(),
		Iterations:       iterations,
		Vcpus:            vcpus,
		MemMIB:           memMIB,
		KernelImage:      kernel,
		RootfsPath:       rootfs,
		Label:            label,
		Source:           source,
		SnapshotCreateMs: snapshotMs,
		Restore:          samples,
		HostMeta: map[string]any{
			"hostname": hostname,
			"go_os":    runtime.GOOS,
			"go_arch":  runtime.GOARCH,
		},
	}
	rep.Summary = summarize(samples)
	return rep
}

func summarize(samples []restoreSample) reportSummary {
	var starts, ready, pings, teardown []float64
	for _, s := range samples {
		starts = append(starts, s.RestoreStartMs)
		ready = append(ready, s.AgentReadyMs)
		pings = append(pings, s.PingMs)
		teardown = append(teardown, s.TeardownMs)
	}
	sort.Float64s(starts)
	sort.Float64s(ready)
	sort.Float64s(pings)
	sort.Float64s(teardown)
	return reportSummary{
		RestoreStartP50Ms: percentile(starts, 0.50),
		RestoreStartP95Ms: percentile(starts, 0.95),
		RestoreStartMaxMs: maxSorted(starts),
		AgentReadyP50Ms:   percentile(ready, 0.50),
		AgentReadyP95Ms:   percentile(ready, 0.95),
		AgentReadyMaxMs:   maxSorted(ready),
		PingP50Ms:         percentile(pings, 0.50),
		PingP95Ms:         percentile(pings, 0.95),
		PingMaxMs:         maxSorted(pings),
		TeardownP50Ms:     percentile(teardown, 0.50),
		TeardownP95Ms:     percentile(teardown, 0.95),
		TeardownMaxMs:     maxSorted(teardown),
	}
}

func writeReport(outPath, resultsDir string, rep report) (string, error) {
	if outPath == "" {
		if err := os.MkdirAll(resultsDir, 0o755); err != nil {
			return "", fmt.Errorf("create results dir: %w", err)
		}
		ts := rep.Timestamp.Format("20060102-150405")
		name := fmt.Sprintf("swarmsnap-%s-n%d", ts, rep.Iterations)
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

func listenPath(vsockPath string) string {
	return fmt.Sprintf("%s_%d", vsockPath, controlPort)
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
