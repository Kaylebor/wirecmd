//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWirecmdSignalProcess(t *testing.T) {
	if os.Getenv("GO_WIRECMD_SIGNAL_PROCESS") != "1" {
		return
	}
	os.Args = []string{"wirecmd", "--direct", "--config", os.Getenv("GO_WIRECMD_SIGNAL_CONFIG"), "blocking", "block"}
	main()
}

func TestBlockingMCPProcess(t *testing.T) {
	if os.Getenv("GO_WIRECMD_BLOCKING_SERVER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("WIRECMD_CHILD_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "blocking", Version: "dev"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "block"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func TestSIGTERMClosesDirectStdioChild(t *testing.T) {
	directory := t.TempDir()
	pidPath := filepath.Join(directory, "child.pid")
	configPath := filepath.Join(directory, "wirecmd.kdl")
	config := `wirecmd {
    mcp "blocking" {
        scope "workspace"
        stdio ` + strconv.Quote(os.Args[0]) + ` {
            arg "-test.run=TestBlockingMCPProcess"
            arg "--"
            env GO_WIRECMD_BLOCKING_SERVER="1"
            env WIRECMD_CHILD_PID_FILE=` + strconv.Quote(pidPath) + `
        }
    }
}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=TestWirecmdSignalProcess", "--")
	command.Env = append(os.Environ(),
		"GO_WIRECMD_SIGNAL_PROCESS=1",
		"GO_WIRECMD_SIGNAL_CONFIG="+configPath,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})

	childPID := waitForChildPID(t, pidPath)
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-time.After(7 * time.Second):
		t.Fatal("Wirecmd did not exit after SIGTERM")
	case <-done:
	}
	if err := syscall.Kill(childPID, 0); err != syscall.ESRCH {
		t.Fatalf("stdio child process %d survived SIGTERM cleanup: %v", childPID, err)
	}
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(string(content))
			if parseErr != nil {
				t.Fatalf("parse child PID: %v", parseErr)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for stdio child to start")
	return 0
}
