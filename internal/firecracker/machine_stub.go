//go:build !linux

package firecracker

import (
	"context"
	"errors"
	"fmt"
)

// ErrLinuxOnly is returned when Firecracker helpers are used off Linux.
var ErrLinuxOnly = errors.New("firecracker: requires Linux with KVM (build and run sandboxbench on the remote host, or use GOOS=linux)")

// Machine is a placeholder on non-Linux builds.
type Machine struct{}

// PID is unavailable off Linux.
func (m *Machine) PID() (int, error) {
	return 0, ErrLinuxOnly
}

// NewMachine always fails on non-Linux platforms (the SDK does not build on Darwin/Windows).
func NewMachine(ctx context.Context, opts RunOptions, disableValidation bool) (*Machine, RuntimeConfig, error) {
	return nil, RuntimeConfig{}, ErrLinuxOnly
}

func Start(ctx context.Context, m *Machine) error {
	return ErrLinuxOnly
}

func StopForce(m *Machine) error {
	return nil
}

func ShutdownGuest(ctx context.Context, m *Machine) error {
	return ErrLinuxOnly
}

func Wait(ctx context.Context, m *Machine) error {
	if m == nil {
		return fmt.Errorf("nil machine")
	}
	return ErrLinuxOnly
}

func Pause(ctx context.Context, m *Machine) error {
	return ErrLinuxOnly
}

func Resume(ctx context.Context, m *Machine) error {
	return ErrLinuxOnly
}

func CreateSnapshot(ctx context.Context, m *Machine, paths SnapshotPaths) error {
	return ErrLinuxOnly
}

func NewMachineFromSnapshot(ctx context.Context, opts RunOptions, snap SnapshotPaths, disableValidation bool) (*Machine, RuntimeConfig, error) {
	return nil, RuntimeConfig{}, ErrLinuxOnly
}
