//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const hostCID = 2
const controlPort = 5000

type frame struct {
	Type string `json:"type"`
	Seq  int    `json:"seq,omitempty"`
	Kind string `json:"kind,omitempty"`
	Body string `json:"body,omitempty"`
}

func main() {
	fmt.Fprintln(os.Stderr, "swarm-agent: booted")

	for {
		fmt.Fprintln(os.Stderr, "swarm-agent: dialing host vsock")
		conn := dialHost(controlPort)
		fmt.Fprintln(os.Stderr, "swarm-agent: connected")

		reconnect := serve(conn)
		_ = conn.Close()
		if !reconnect {
			return
		}
	}
}

func serve(conn *os.File) bool {
	enc := json.NewEncoder(conn)
	if err := enc.Encode(frame{Type: "hello", Body: "agent-ready"}); err != nil {
		fmt.Fprintf(os.Stderr, "swarm-agent: hello: %v\n", err)
		return true
	}
	fmt.Fprintln(os.Stderr, "swarm-agent: hello sent")

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg frame
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			_ = enc.Encode(frame{Type: "error", Body: err.Error()})
			continue
		}
		if msg.Type == "stop" {
			_ = enc.Encode(frame{Type: "stopped", Seq: msg.Seq})
			return false
		}
		if msg.Type == "prepare_snapshot" {
			_ = enc.Encode(frame{Type: "snapshot_ready", Seq: msg.Seq, Body: "reconnecting"})
			return true
		}
		if msg.Type != "task" {
			_ = enc.Encode(frame{Type: "error", Seq: msg.Seq, Body: "unknown message type"})
			continue
		}
		_ = enc.Encode(frame{
			Type: "result",
			Seq:  msg.Seq,
			Kind: msg.Kind,
			Body: runTask(msg.Kind, msg.Body),
		})
	}
	return true
}

func runTask(kind, body string) string {
	switch kind {
	case "plan":
		return "plan: split work into parallel worker checks"
	case "work":
		return "work: " + summarize(body)
	case "critic":
		return "critic: merged worker outputs look coherent"
	case "ping":
		return "pong"
	default:
		return "done: " + summarize(body)
	}
}

func summarize(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 96 {
		return s[:96]
	}
	return s
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
