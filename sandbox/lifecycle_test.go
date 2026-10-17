package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.com/agentbook/toolbox"
)

func TestOSNetworkAndEnvironmentBoundary(t *testing.T) {
	if os.Getenv("AGENTBOOK_SANDBOX_TEST") != "1" {
		t.Skip("OS probes require explicit opt-in")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("network-canary")) }))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	worker := Worker{Executable: "/usr/bin/curl", Registry: toolbox.Registry{Root: t.TempDir(), MaxBytes: 512}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command, err := worker.Command(ctx, []string{"--silent", "--max-time", "1", server.URL})
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.CombinedOutput()
	if err == nil || strings.Contains(string(output), "network-canary") {
		t.Fatal("worker reached local network", string(output))
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "OPENROUTER_API_KEY=") {
			t.Fatal("model key inherited by worker")
		}
	}
}

func TestOSCancellationStopsHeartbeat(t *testing.T) {
	if os.Getenv("AGENTBOOK_SANDBOX_TEST") != "1" {
		t.Skip("OS probes require explicit opt-in")
	}
	root := t.TempDir()
	heartbeat := filepath.Join(root, "heartbeat")
	guest := heartbeat
	if runtime.GOOS == "linux" {
		guest = "/workspace/heartbeat"
	}
	worker := Worker{Executable: "/bin/sh", Registry: toolbox.Registry{Root: root, MaxBytes: 512, AllowWrites: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command, err := worker.Command(ctx, []string{"-c", `while true; do printf x >> "$1"; /bin/sleep 0.03; done`, "heartbeat", guest})
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(heartbeat)
		if len(data) > 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("worker never started heartbeat")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not terminate")
	}
	before, _ := os.ReadFile(heartbeat)
	time.Sleep(100 * time.Millisecond)
	after, _ := os.ReadFile(heartbeat)
	if len(before) != len(after) {
		t.Fatal("work continued after cancellation")
	}
}
