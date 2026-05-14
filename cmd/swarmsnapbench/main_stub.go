//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "swarmsnapbench must run on Linux with KVM/Firecracker")
	os.Exit(1)
}
