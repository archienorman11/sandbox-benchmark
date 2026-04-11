package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// File is JSON configuration for a microVM (paths are resolved on the Linux host).
type File struct {
	FirecrackerBin string `json:"firecracker_bin"`
	KernelImage    string `json:"kernel_image"`
	RootfsPath     string `json:"rootfs_path"`
	KernelArgs     string `json:"kernel_args"`

	Vcpus  int64 `json:"vcpus"`
	MemMIB int64 `json:"mem_mib"`

	SocketPath string `json:"socket_path"`
	StatePath  string `json:"state_path"`
}

// Defaults fills zero values with conservative defaults.
func (c *File) Defaults() {
	if c.KernelArgs == "" {
		c.KernelArgs = "reboot=k panic=1 pci=off root=/dev/vda rw console=ttyS0"
	}
	if c.Vcpus == 0 {
		c.Vcpus = 1
	}
	if c.MemMIB == 0 {
		c.MemMIB = 128
	}
	if c.StatePath == "" {
		c.StatePath = "/tmp/sandboxbench-state.json"
	}
}

// Load reads and decodes path as JSON.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c File
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	c.Defaults()
	return &c, nil
}
