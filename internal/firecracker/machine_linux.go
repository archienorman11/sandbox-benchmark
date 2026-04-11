//go:build linux

package firecracker

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

// Machine wraps the Firecracker SDK machine (Linux only).
type Machine struct {
	*fcsdk.Machine
}

func (o RunOptions) fcConfig() (fcsdk.Config, error) {
	if err := o.applyDefaults(); err != nil {
		return fcsdk.Config{}, err
	}

	uid, err := uuid.NewRandom()
	if err != nil {
		return fcsdk.Config{}, err
	}
	logFIFO := filepath.Join(o.LogDir, fmt.Sprintf("sandboxbench-log-%s.fifo", uid.String()))

	vmID, err := uuid.NewRandom()
	if err != nil {
		return fcsdk.Config{}, err
	}

	drives := []models.Drive{
		{
			DriveID:      fcsdk.String("rootfs"),
			PathOnHost:   fcsdk.String(o.RootfsPath),
			IsRootDevice: fcsdk.Bool(true),
			IsReadOnly:   fcsdk.Bool(false),
		},
	}

	cfg := fcsdk.Config{
		VMID:            vmID.String(),
		SocketPath:      o.SocketPath,
		KernelImagePath: o.KernelImage,
		KernelArgs:      o.KernelArgs,
		Drives:          drives,
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  fcsdk.Int64(o.Vcpus),
			MemSizeMib: fcsdk.Int64(o.MemMIB),
		},
		LogFifo:  logFIFO,
		LogLevel: "Warn",
		Seccomp:  fcsdk.SeccompConfig{Enabled: false},
	}
	return cfg, nil
}

func buildCommand(ctx context.Context, fcCfg fcsdk.Config, fcBin string) *exec.Cmd {
	builder := fcsdk.VMCommandBuilder{}.
		WithBin(fcBin).
		WithSocketPath(fcCfg.SocketPath).
		AddArgs("--id", fcCfg.VMID)
	if !fcCfg.Seccomp.Enabled {
		builder = builder.AddArgs("--no-seccomp")
	} else if len(fcCfg.Seccomp.Filter) > 0 {
		builder = builder.AddArgs("--seccomp-filter", fcCfg.Seccomp.Filter)
	}
	return builder.Build(ctx)
}

func silentLog() *logrus.Entry {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return logrus.NewEntry(l)
}

// NewMachine builds a Machine from RunOptions (validates paths via SDK unless DisableValidation).
func NewMachine(ctx context.Context, opts RunOptions, disableValidation bool) (*Machine, RuntimeConfig, error) {
	fcCfg, err := opts.fcConfig()
	if err != nil {
		return nil, RuntimeConfig{}, err
	}
	fcCfg.DisableValidation = disableValidation

	cmd := buildCommand(ctx, fcCfg, opts.FirecrackerBin)
	m, err := fcsdk.NewMachine(ctx, fcCfg, fcsdk.WithProcessRunner(cmd), fcsdk.WithLogger(silentLog()))
	if err != nil {
		return nil, RuntimeConfig{}, err
	}
	rt := RuntimeConfig{SocketPath: fcCfg.SocketPath, VMID: fcCfg.VMID}
	return &Machine{m}, rt, nil
}

// Start boots the VMM and sends InstanceStart.
func Start(ctx context.Context, m *Machine) error {
	if m == nil || m.Machine == nil {
		return fmt.Errorf("nil machine")
	}
	return m.Machine.Start(ctx)
}

// StopForce sends SIGTERM to the Firecracker process (fast teardown).
func StopForce(m *Machine) error {
	if m == nil || m.Machine == nil {
		return nil
	}
	return m.Machine.StopVMM()
}

// ShutdownGuest requests ACPI-style shutdown via CtrlAltDel.
func ShutdownGuest(ctx context.Context, m *Machine) error {
	if m == nil || m.Machine == nil {
		return fmt.Errorf("nil machine")
	}
	return m.Machine.Shutdown(ctx)
}

// Wait blocks until the Firecracker process exits.
func Wait(ctx context.Context, m *Machine) error {
	if m == nil || m.Machine == nil {
		return fmt.Errorf("nil machine")
	}
	return m.Machine.Wait(ctx)
}

// Pause pauses the VM (required before creating a snapshot).
func Pause(ctx context.Context, m *Machine) error {
	if m == nil || m.Machine == nil {
		return fmt.Errorf("nil machine")
	}
	return m.Machine.PauseVM(ctx)
}

// Resume resumes a paused VM.
func Resume(ctx context.Context, m *Machine) error {
	if m == nil || m.Machine == nil {
		return fmt.Errorf("nil machine")
	}
	return m.Machine.ResumeVM(ctx)
}

// CreateSnapshot pauses the VM and creates a full snapshot.
func CreateSnapshot(ctx context.Context, m *Machine, paths SnapshotPaths) error {
	if m == nil || m.Machine == nil {
		return fmt.Errorf("nil machine")
	}
	return m.Machine.CreateSnapshot(ctx, paths.MemFilePath, paths.SnapshotPath)
}

// NewMachineFromSnapshot creates a new Machine that restores from a snapshot.
//
// The SDK's NewMachine always sets defaultHandlers (which do a fresh boot).
// For snapshot restore we must swap to the snapshot handler chain:
//
//	StartVMM → CreateLogFiles → BootstrapLogging → LoadSnapshot
//
// instead of the default:
//
//	SetupNetwork → SetupKernelArgs → StartVMM → ... → CreateBootSource → ...
func NewMachineFromSnapshot(ctx context.Context, opts RunOptions, snap SnapshotPaths, disableValidation bool) (*Machine, RuntimeConfig, error) {
	if err := opts.applyDefaults(); err != nil {
		return nil, RuntimeConfig{}, err
	}

	cfg := fcsdk.Config{
		SocketPath:        opts.SocketPath,
		DisableValidation: disableValidation,
		Snapshot: fcsdk.SnapshotConfig{
			MemFilePath:  snap.MemFilePath,
			SnapshotPath: snap.SnapshotPath,
			ResumeVM:     true,
		},
		Drives: []models.Drive{
			{
				DriveID:      fcsdk.String("rootfs"),
				PathOnHost:   fcsdk.String(opts.RootfsPath),
				IsRootDevice: fcsdk.Bool(true),
				IsReadOnly:   fcsdk.Bool(false),
			},
		},
		Seccomp: fcsdk.SeccompConfig{Enabled: false},
	}

	vmID, _ := uuid.NewRandom()
	cfg.VMID = vmID.String()

	cmd := buildCommand(ctx, cfg, opts.FirecrackerBin)
	m, err := fcsdk.NewMachine(ctx, cfg, fcsdk.WithProcessRunner(cmd), fcsdk.WithLogger(silentLog()))
	if err != nil {
		return nil, RuntimeConfig{}, err
	}

	// Swap handlers from default (fresh boot) to snapshot restore chain.
	m.Handlers = fcsdk.Handlers{
		Validation: fcsdk.HandlerList{},
		FcInit: fcsdk.HandlerList{}.Append(
			fcsdk.StartVMMHandler,
			fcsdk.CreateLogFilesHandler,
			fcsdk.BootstrapLoggingHandler,
			fcsdk.LoadSnapshotHandler,
		),
	}

	rt := RuntimeConfig{SocketPath: cfg.SocketPath, VMID: cfg.VMID}
	return &Machine{m}, rt, nil
}
