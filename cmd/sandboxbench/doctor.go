package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func doctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check Linux/KVM and firecracker binary availability",
		RunE:  runDoctor,
	}
	cmd.Flags().StringVar(&fcBin, "firecracker", "", "path to firecracker binary (default: search PATH)")
	return cmd
}

func runDoctor(cmd *cobra.Command, args []string) error {
	pass := 0
	fail := 0
	warn := 0

	red := color.New(color.FgRed).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()

	check := func(name string, fn func() (string, error)) {
		detail, err := fn()
		if err != nil {
			fmt.Printf("  %s %s — %v\n", red("✗"), name, err)
			fail++
		} else {
			fmt.Printf("  %s %s%s\n", green("✓"), name, detail)
			pass++
		}
	}

	warnCheck := func(name string, fn func() (string, error)) {
		detail, err := fn()
		if err != nil {
			fmt.Printf("  %s %s — %v\n", yellow("!"), name, err)
			warn++
		} else {
			fmt.Printf("  %s %s%s\n", green("✓"), name, detail)
			pass++
		}
	}

	fmt.Println()
	fmt.Println("sandboxbench doctor")
	fmt.Println()

	// Platform
	fmt.Println("Platform")
	check("Linux", func() (string, error) {
		if runtime.GOOS != "linux" {
			return "", fmt.Errorf("running on %s (need linux)", runtime.GOOS)
		}
		return fmt.Sprintf(" (%s/%s)", runtime.GOOS, runtime.GOARCH), nil
	})

	// KVM
	check("KVM", func() (string, error) {
		info, err := os.Stat("/dev/kvm")
		if err != nil {
			return "", fmt.Errorf("not accessible")
		}
		_ = info
		return " (/dev/kvm)", nil
	})
	fmt.Println()

	// Firecracker
	fmt.Println("Firecracker")
	check("Binary", func() (string, error) {
		bin := fcBin
		if bin == "" {
			var err error
			bin, err = exec.LookPath("firecracker")
			if err != nil {
				return "", fmt.Errorf("not found in PATH")
			}
		}
		return fmt.Sprintf(" (%s)", bin), nil
	})

	warnCheck("Version", func() (string, error) {
		bin := fcBin
		if bin == "" {
			bin, _ = exec.LookPath("firecracker")
		}
		if bin == "" {
			return "", fmt.Errorf("skipped (no binary)")
		}
		out, err := exec.Command(bin, "--version").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("could not determine")
		}
		ver := strings.TrimSpace(string(out))
		return fmt.Sprintf(" (%s)", ver), nil
	})
	fmt.Println()

	// Kernel / rootfs (optional hints)
	fmt.Println("Guest assets")
	warnCheck("Kernel", func() (string, error) {
		paths := []string{"/opt/fc/vmlinux", "/var/lib/firecracker/vmlinux"}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return fmt.Sprintf(" (%s)", p), nil
			}
		}
		return "", fmt.Errorf("not found at common paths (provide via --config)")
	})
	warnCheck("Rootfs", func() (string, error) {
		paths := []string{"/opt/fc/rootfs.ext4", "/var/lib/firecracker/rootfs.ext4"}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return fmt.Sprintf(" (%s)", p), nil
			}
		}
		return "", fmt.Errorf("not found at common paths (provide via --config)")
	})

	// Summary
	fmt.Println()
	if fail > 0 {
		fmt.Printf("%s, %d passed", red(fmt.Sprintf("%d failed", fail)), pass)
		if warn > 0 {
			fmt.Printf(", %s", yellow(fmt.Sprintf("%d warnings", warn)))
		}
		fmt.Println()
		return fmt.Errorf("%d check(s) failed", fail)
	}
	if warn > 0 {
		fmt.Printf("%s, %s\n", green(fmt.Sprintf("%d passed", pass)), yellow(fmt.Sprintf("%d warnings", warn)))
	} else {
		fmt.Printf("%s\n", green(fmt.Sprintf("All %d checks passed", pass)))
	}
	return nil
}
