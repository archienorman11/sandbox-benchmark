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
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

type roleAgent struct {
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
	hello       string
}

type roleResult struct {
	Role      string
	Seq       int
	SendAt    time.Time
	ReceiveAt time.Time
	Body      string
	Err       error
}

type timelineEvent struct {
	At   time.Time
	Text string
}

func main() {
	var (
		repo         = flag.String("repo", ".", "target repository to mount read-only into each sandbox")
		kernel       = flag.String("kernel", "/opt/fc/vmlinux", "guest kernel path")
		rootfs       = flag.String("rootfs", "/opt/fc/swarmslice-rootfs.ext4", "rootfs containing /sandbox-agent")
		firecracker  = flag.String("firecracker", "firecracker", "firecracker binary")
		workDir      = flag.String("work-dir", "/tmp/swarmslice", "runtime directory")
		imageSizeMIB = flag.Int("image-size-mib", 256, "workspace image size in MiB")
		vcpus        = flag.Int64("vcpus", 1, "vCPUs per sandbox")
		memMIB       = flag.Int64("mem-mib", 128, "memory MiB per sandbox")
		startTimeout = flag.Duration("start-timeout", 15*time.Second, "agent ready timeout")
		keepRunning  = flag.Bool("keep-running", false, "leave VMs running after the run")
	)
	flag.Parse()

	runStart := time.Now()
	var events []timelineEvent
	event := func(format string, args ...any) {
		events = append(events, timelineEvent{At: time.Now(), Text: fmt.Sprintf(format, args...)})
	}

	repoAbs, err := filepath.Abs(*repo)
	if err != nil {
		fatal(err)
	}
	runtimeDir := filepath.Join(*workDir, "run-"+uuid.NewString()[:8])
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		fatal(err)
	}
	event("prepared runtime dir %s", runtimeDir)

	imageStart := time.Now()
	workspaceImage, copiedFiles, err := buildWorkspaceImage(repoAbs, runtimeDir, *imageSizeMIB)
	if err != nil {
		fatal(err)
	}
	imageDuration := time.Since(imageStart)
	event("built read-only workspace image from %s (%d files, %.2fms)", repoAbs, copiedFiles, ms(imageDuration))

	roles := []string{"repo-mapper", "benchmark-runner", "docs-summarizer", "risk-reviewer"}
	agents := make([]*roleAgent, len(roles))
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	event("starting %d sandbox agents", len(roles))
	for i, role := range roles {
		a := &roleAgent{index: i, role: role, cid: uint32(40 + i)}
		agents[i] = a
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := startAgent(ctx, a, runtimeDir, *kernel, *rootfs, workspaceImage, *firecracker, *vcpus, *memMIB, *startTimeout); err != nil {
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
		cleanup(ctx, agents)
		fatal(firstErr)
	}
	event("all agents ready")

	for _, a := range agents {
		event("%s ready vm-start=%.2fms ready=%.2fms cid=%d hello=%s",
			a.role,
			ms(a.vmStartedAt.Sub(a.startedAt)),
			ms(a.readyAt.Sub(a.startedAt)),
			a.cid,
			a.hello,
		)
	}

	event("dispatching role tasks")
	results := runRoles(agents)
	for _, result := range results {
		if result.Err != nil {
			event("%s failed after %.2fms: %v", result.Role, ms(result.ReceiveAt.Sub(result.SendAt)), result.Err)
		} else {
			event("%s completed in %.2fms", result.Role, ms(result.ReceiveAt.Sub(result.SendAt)))
		}
	}

	printTimeline(runStart, events)
	printLatency(imageDuration, agents, results, time.Since(runStart))
	printResults(results)

	if !*keepRunning {
		cleanup(ctx, agents)
		_ = os.RemoveAll(runtimeDir)
	}
}

func buildWorkspaceImage(repo, runtimeDir string, sizeMIB int) (string, int, error) {
	stage := filepath.Join(runtimeDir, "workspace-src")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", 0, err
	}
	var copied int
	err := filepath.WalkDir(repo, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "results", "node_modules", ".cache":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(stage, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > 4*1024*1024 {
			return nil
		}
		if err := copyFile(path, filepath.Join(stage, rel), info.Mode()); err != nil {
			return err
		}
		copied++
		return nil
	})
	if err != nil {
		return "", 0, err
	}

	image := filepath.Join(runtimeDir, "workspace.ext4")
	if sizeMIB < 64 {
		sizeMIB = 64
	}
	if err := exec.Command("truncate", "-s", fmt.Sprintf("%dM", sizeMIB), image).Run(); err != nil {
		return "", 0, fmt.Errorf("truncate workspace image: %w", err)
	}
	cmd := exec.Command("mkfs.ext4", "-q", "-F", "-d", stage, image)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", 0, fmt.Errorf("mkfs.ext4 workspace image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return image, copied, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func startAgent(ctx context.Context, a *roleAgent, workDir, kernel, rootfs, workspaceImage, fcBin string, vcpus, memMIB int64, timeout time.Duration) error {
	id := fmt.Sprintf("%s-%s", a.role, uuid.NewString()[:8])
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
		Drives: []models.Drive{
			{
				DriveID:      fcsdk.String("rootfs"),
				PathOnHost:   fcsdk.String(rootfs),
				IsRootDevice: fcsdk.Bool(true),
				IsReadOnly:   fcsdk.Bool(true),
			},
			{
				DriveID:      fcsdk.String("workspace"),
				PathOnHost:   fcsdk.String(workspaceImage),
				IsRootDevice: fcsdk.Bool(false),
				IsReadOnly:   fcsdk.Bool(true),
			},
		},
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
		return fmt.Errorf("%s did not connect within %s (console: %s)", a.role, timeout, a.consolePath)
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
	a.hello = hello.Body
	a.readyAt = time.Now()
	return nil
}

func runRoles(agents []*roleAgent) []roleResult {
	results := make([]roleResult, len(agents))
	var wg sync.WaitGroup
	for i, a := range agents {
		i, a := i, a
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = send(a, i+1, a.role)
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].Role < results[j].Role })
	return results
}

func send(a *roleAgent, seq int, kind string) roleResult {
	res := roleResult{Role: a.role, Seq: seq, SendAt: time.Now()}
	if err := a.enc.Encode(frame{Type: "task", Seq: seq, Kind: kind}); err != nil {
		res.ReceiveAt = time.Now()
		res.Err = fmt.Errorf("send: %w", err)
		return res
	}
	if !a.scanner.Scan() {
		res.ReceiveAt = time.Now()
		res.Err = fmt.Errorf("receive: %v", a.scanner.Err())
		return res
	}
	res.ReceiveAt = time.Now()
	var resp frame
	if err := json.Unmarshal(a.scanner.Bytes(), &resp); err != nil {
		res.Err = fmt.Errorf("decode: %w", err)
		return res
	}
	if resp.Seq != seq {
		res.Err = fmt.Errorf("response seq mismatch: got %d want %d", resp.Seq, seq)
		return res
	}
	res.Body = resp.Body
	return res
}

func cleanup(ctx context.Context, agents []*roleAgent) {
	for _, a := range agents {
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

func printTimeline(start time.Time, events []timelineEvent) {
	sort.Slice(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	fmt.Printf("\n[event timeline]\n")
	for _, event := range events {
		fmt.Printf("  +%9.3fms  %s\n", ms(event.At.Sub(start)), event.Text)
	}
}

func printLatency(imageDuration time.Duration, agents []*roleAgent, results []roleResult, total time.Duration) {
	var vmStarts, ready, tasks []float64
	for _, a := range agents {
		vmStarts = append(vmStarts, ms(a.vmStartedAt.Sub(a.startedAt)))
		ready = append(ready, ms(a.readyAt.Sub(a.startedAt)))
	}
	for _, r := range results {
		tasks = append(tasks, ms(r.ReceiveAt.Sub(r.SendAt)))
	}
	sort.Float64s(vmStarts)
	sort.Float64s(ready)
	sort.Float64s(tasks)
	fmt.Printf("\n[latency breakdown]\n")
	fmt.Printf("  workspace image build: %.2fms\n", ms(imageDuration))
	fmt.Printf("  vm-start:              p50=%.2fms p95=%.2fms max=%.2fms\n", percentile(vmStarts, 0.50), percentile(vmStarts, 0.95), vmStarts[len(vmStarts)-1])
	fmt.Printf("  agent-ready:           p50=%.2fms p95=%.2fms max=%.2fms\n", percentile(ready, 0.50), percentile(ready, 0.95), ready[len(ready)-1])
	fmt.Printf("  role task:             p50=%.2fms p95=%.2fms max=%.2fms\n", percentile(tasks, 0.50), percentile(tasks, 0.95), tasks[len(tasks)-1])
	fmt.Printf("  total wall:            %.2fms\n", ms(total))
}

func printResults(results []roleResult) {
	fmt.Printf("\n[agent outputs]\n")
	for _, result := range results {
		fmt.Printf("\n## %s\n", result.Role)
		if result.Err != nil {
			fmt.Printf("error: %v\n", result.Err)
			continue
		}
		fmt.Print(strings.TrimSpace(result.Body))
		fmt.Println()
	}
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	idx := int(float64(len(xs)-1) * p)
	return xs[idx]
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "swarmslice: %v\n", err)
	os.Exit(1)
}
