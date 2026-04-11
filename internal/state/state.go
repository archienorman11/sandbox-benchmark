package state

import (
	"encoding/json"
	"os"
)

// VM records a running Firecracker process started by sandboxbench up.
type VM struct {
	PID        int    `json:"pid"`
	SocketPath string `json:"socket_path"`
	VMID       string `json:"vm_id,omitempty"`
}

// Write atomically writes path as JSON.
func Write(path string, v VM) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Read loads VM state from path.
func Read(path string) (VM, error) {
	var v VM
	b, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	return v, nil
}
