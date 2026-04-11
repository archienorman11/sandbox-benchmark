package main

import (
	"github.com/spf13/cobra"

	"github.com/ayush6624/sandbox-benchmark/internal/config"
	"github.com/ayush6624/sandbox-benchmark/internal/firecracker"
)

// Shared persistent flags across commands that need a config file.
var (
	cfgPath    string
	fcBin      string
	kernel     string
	rootfs     string
	socket     string
	vcpus      int64
	memMIB     int64
	noValidate bool
)

// addVMFlags registers the shared flags for commands that create a VM.
func addVMFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to JSON config (required)")
	_ = cmd.MarkFlagRequired("config")
	cmd.Flags().StringVar(&fcBin, "firecracker", "", "override firecracker binary")
	cmd.Flags().StringVar(&kernel, "kernel", "", "override guest kernel (uncompressed vmlinux)")
	cmd.Flags().StringVar(&rootfs, "rootfs", "", "override rootfs disk path")
	cmd.Flags().StringVar(&socket, "socket", "", "override API socket path")
	cmd.Flags().Int64Var(&vcpus, "vcpus", 0, "override vCPU count")
	cmd.Flags().Int64Var(&memMIB, "mem-mib", 0, "override memory (MiB)")
	cmd.Flags().BoolVar(&noValidate, "no-validate", false, "skip SDK path validation (for dry runs on non-Linux)")
}

// loadAndMerge loads the config file and merges CLI flag overrides.
func loadAndMerge() (*config.File, firecracker.RunOptions, error) {
	cf, err := config.Load(cfgPath)
	if err != nil {
		return nil, firecracker.RunOptions{}, err
	}
	opts := firecracker.RunOptions{
		FirecrackerBin: cf.FirecrackerBin,
		KernelImage:    cf.KernelImage,
		RootfsPath:     cf.RootfsPath,
		KernelArgs:     cf.KernelArgs,
		Vcpus:          cf.Vcpus,
		MemMIB:         cf.MemMIB,
		SocketPath:     cf.SocketPath,
	}
	if fcBin != "" {
		opts.FirecrackerBin = fcBin
	}
	if kernel != "" {
		opts.KernelImage = kernel
	}
	if rootfs != "" {
		opts.RootfsPath = rootfs
	}
	if socket != "" {
		opts.SocketPath = socket
	}
	if vcpus > 0 {
		opts.Vcpus = vcpus
	}
	if memMIB > 0 {
		opts.MemMIB = memMIB
	}
	return cf, opts, nil
}
