//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "swarmslice requires Linux with KVM and Firecracker")
	os.Exit(1)
}
