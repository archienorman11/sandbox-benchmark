//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	hostCID     = 2
	controlPort = 5000
	workspace   = "/workspace"
)

type frame struct {
	Type string `json:"type"`
	Seq  int    `json:"seq,omitempty"`
	Kind string `json:"kind,omitempty"`
	Body string `json:"body,omitempty"`
}

func main() {
	mountErr := mountWorkspace()
	for {
		conn := dialHost(controlPort)
		reconnect := serve(conn, mountErr)
		_ = conn.Close()
		if !reconnect {
			return
		}
	}
}

func serve(conn *os.File, mountErr error) bool {
	enc := json.NewEncoder(conn)
	hello := "workspace-mounted"
	if mountErr != nil {
		hello = "workspace-mount-error: " + mountErr.Error()
	}
	if err := enc.Encode(frame{Type: "hello", Body: hello}); err != nil {
		return true
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg frame
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			_ = enc.Encode(frame{Type: "error", Body: err.Error()})
			continue
		}
		switch msg.Type {
		case "stop":
			_ = enc.Encode(frame{Type: "stopped", Seq: msg.Seq})
			return false
		case "task":
			body := runRole(msg.Kind, mountErr)
			_ = enc.Encode(frame{Type: "result", Seq: msg.Seq, Kind: msg.Kind, Body: body})
		default:
			_ = enc.Encode(frame{Type: "error", Seq: msg.Seq, Body: "unknown message type"})
		}
	}
	return true
}

func mountWorkspace() error {
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return err
	}
	for i := 0; i < 200; i++ {
		if _, err := os.Stat("/dev/vdb"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return unix.Mount("/dev/vdb", workspace, "ext4", unix.MS_RDONLY, "")
}

func runRole(role string, mountErr error) string {
	if mountErr != nil {
		return "error: workspace mount failed: " + mountErr.Error()
	}
	switch role {
	case "repo-mapper":
		return limit(repoMapper(), 5000)
	case "benchmark-runner":
		return limit(benchmarkRunner(), 5000)
	case "docs-summarizer":
		return limit(docsSummarizer(), 5000)
	case "risk-reviewer":
		return limit(riskReviewer(), 5000)
	default:
		return "unknown role: " + role
	}
}

func repoMapper() string {
	files := collectFiles()
	byExt := map[string]int{}
	top := map[string]int{}
	var cmds, scripts []string
	for _, file := range files {
		ext := filepath.Ext(file)
		if ext == "" {
			ext = "(none)"
		}
		byExt[ext]++
		parts := strings.Split(file, string(os.PathSeparator))
		if len(parts) > 1 {
			top[parts[0]]++
		}
		if strings.HasPrefix(file, "cmd/") && strings.Count(file, "/") >= 2 {
			cmd := strings.Split(file, "/")[1]
			cmds = appendUnique(cmds, cmd)
		}
		if strings.HasPrefix(file, "scripts/") {
			scripts = append(scripts, file)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "repo mapper\n")
	fmt.Fprintf(&b, "- files scanned: %d\n", len(files))
	fmt.Fprintf(&b, "- top directories: %s\n", topCounts(top, 8))
	fmt.Fprintf(&b, "- file types: %s\n", topCounts(byExt, 8))
	fmt.Fprintf(&b, "- commands: %s\n", strings.Join(sorted(cmds), ", "))
	fmt.Fprintf(&b, "- scripts: %s\n", strings.Join(sorted(limitStrings(scripts, 12)), ", "))
	return b.String()
}

func benchmarkRunner() string {
	files := collectFiles()
	packages := map[string]bool{}
	var testFiles, benchFuncs []string
	benchRE := regexp.MustCompile(`func\s+(Benchmark[A-Za-z0-9_]+)\s*\(`)
	for _, file := range files {
		if filepath.Ext(file) != ".go" {
			continue
		}
		packages[filepath.Dir(file)] = true
		if strings.HasSuffix(file, "_test.go") {
			testFiles = append(testFiles, file)
		}
		for _, name := range benchRE.FindAllStringSubmatch(readSmall(filepath.Join(workspace, file), 256*1024), -1) {
			benchFuncs = append(benchFuncs, fmt.Sprintf("%s:%s", file, name[1]))
		}
	}

	targets := makefileTargets()
	var b strings.Builder
	fmt.Fprintf(&b, "benchmark runner\n")
	fmt.Fprintf(&b, "- go.mod present: %t\n", exists(filepath.Join(workspace, "go.mod")))
	fmt.Fprintf(&b, "- go packages detected: %d\n", len(packages))
	fmt.Fprintf(&b, "- test files detected: %d\n", len(testFiles))
	fmt.Fprintf(&b, "- benchmark funcs: %s\n", noneIfEmpty(strings.Join(sorted(benchFuncs), ", ")))
	fmt.Fprintf(&b, "- make targets: %s\n", noneIfEmpty(strings.Join(limitStrings(sorted(targets), 12), ", ")))
	fmt.Fprintf(&b, "- suggested host checks: go test ./...; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./...; build guest binaries\n")
	return b.String()
}

func docsSummarizer() string {
	var docs []string
	for _, file := range collectFiles() {
		if file == "README.md" || strings.HasPrefix(file, "docs/") && filepath.Ext(file) == ".md" || file == "CLAUDE.md" {
			docs = append(docs, file)
		}
	}
	sort.Strings(docs)

	var b strings.Builder
	fmt.Fprintf(&b, "docs summarizer\n")
	fmt.Fprintf(&b, "- docs found: %d\n", len(docs))
	for _, doc := range limitStrings(docs, 8) {
		headings := markdownHeadings(filepath.Join(workspace, doc), 6)
		fmt.Fprintf(&b, "- %s: %s\n", doc, noneIfEmpty(strings.Join(headings, "; ")))
	}
	return b.String()
}

func riskReviewer() string {
	patterns := []string{"TODO", "FIXME", "panic(", "sudo ", "curl ", "rm -rf", "SeccompConfig{Enabled: false}", "--no-seccomp"}
	counts := map[string]int{}
	var examples []string
	for _, file := range collectFiles() {
		if !interestingForRisk(file) {
			continue
		}
		text := readSmall(filepath.Join(workspace, file), 512*1024)
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "patterns := []string{") {
				continue
			}
			for _, p := range patterns {
				if !strings.Contains(line, p) {
					continue
				}
				counts[p]++
				if len(examples) < 10 {
					examples = append(examples, fmt.Sprintf("%s contains %q", file, p))
				}
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "risk reviewer\n")
	fmt.Fprintf(&b, "- license file present: %t\n", exists(filepath.Join(workspace, "LICENSE")))
	fmt.Fprintf(&b, "- risk keyword hits: %s\n", topCounts(counts, len(patterns)))
	for _, ex := range examples {
		fmt.Fprintf(&b, "- example: %s\n", ex)
	}
	if len(examples) == 0 {
		fmt.Fprintf(&b, "- examples: none from basic keyword scan\n")
	}
	return b.String()
}

func collectFiles() []string {
	var files []string
	_ = filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "results", "node_modules", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".DS_Store" {
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func makefileTargets() []string {
	text := readSmall(filepath.Join(workspace, "Makefile"), 256*1024)
	var targets []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "\t") || !strings.Contains(line, ":") {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
		if name != "" && !strings.ContainsAny(name, " $()/") {
			targets = append(targets, name)
		}
	}
	return targets
}

func markdownHeadings(path string, max int) []string {
	text := readSmall(path, 256*1024)
	var headings []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			headings = append(headings, strings.TrimSpace(strings.TrimLeft(line, "#")))
			if len(headings) >= max {
				return headings
			}
		}
	}
	return headings
}

func interestingForRisk(file string) bool {
	ext := filepath.Ext(file)
	switch ext {
	case ".go", ".sh", ".md", ".json", ".yaml", ".yml":
		return true
	default:
		return file == "Makefile" || file == "README.md"
	}
}

func readSmall(path string, max int64) string {
	info, err := os.Stat(path)
	if err != nil || info.Size() > max {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func topCounts(m map[string]int, max int) string {
	type kv struct {
		k string
		v int
	}
	var xs []kv
	for k, v := range m {
		xs = append(xs, kv{k: k, v: v})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].v == xs[j].v {
			return xs[i].k < xs[j].k
		}
		return xs[i].v > xs[j].v
	})
	var parts []string
	for i, x := range xs {
		if i >= max {
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%d", x.k, x.v))
	}
	return noneIfEmpty(strings.Join(parts, ", "))
}

func sorted(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}

func limitStrings(xs []string, max int) []string {
	if len(xs) <= max {
		return xs
	}
	return xs[:max]
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func noneIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

func limit(s string, max int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\r' }), "")
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...truncated..."
}

func dialHost(port uint32) *os.File {
	for {
		conn, err := dialVsock(hostCID, port)
		if err == nil {
			return conn
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dialVsock(cid, port uint32) (*os.File, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	sa := &unix.SockaddrVM{CID: cid, Port: port}
	if err := unix.Connect(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), "vsock"), nil
}
