//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "swarmslice-agent only runs inside a Linux Firecracker guest")
	os.Exit(1)
}
