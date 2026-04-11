package firecracker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// RunOptions configures a one-shot or long-lived microVM run.
type RunOptions struct {
	FirecrackerBin string
	SocketPath     string
	KernelImage    string
	RootfsPath     string
	KernelArgs     string
	Vcpus          int64
	MemMIB         int64

	LogDir string
}

// RuntimeConfig captures identifiers after the SDK config is built.
type RuntimeConfig struct {
	SocketPath string
	VMID       string
}

// SnapshotPaths holds the file paths for a Firecracker snapshot.
type SnapshotPaths struct {
	SnapshotPath string // microVM state file
	MemFilePath  string // guest memory dump
}

func (o *RunOptions) applyDefaults() error {
	if o.FirecrackerBin == "" {
		o.FirecrackerBin = "firecracker"
	}
	if o.SocketPath == "" {
		id, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		o.SocketPath = filepath.Join(os.TempDir(), fmt.Sprintf("sandboxbench-%s.sock", id.String()))
	}
	if o.LogDir == "" {
		o.LogDir = os.TempDir()
	}
	return nil
}
