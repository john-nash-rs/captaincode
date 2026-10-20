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

	"github.com/john-nash-rs/captaincode/toolbox"
)

// requireBackend skips a test on a platform with no isolation backend.
//
// The tests below probe real OS enforcement. Without a backend there is
// nothing to enforce, and they would do no more than assert that the refusal
// names a path that does not exist on this platform. Skipping is the honest
// report; running would be a green or red that means nothing.
func requireBackend(t *testing.T) {
	t.Helper()
	if !HasBackend() {
		t.Skipf("no sandbox backend on %s", runtime.GOOS)
	}
}

// shellFor is the program a probe runs to exercise a boundary.
//
// Every boundary below is enforced by the OS on a process, not by a shell, so
// the shell is only what writes the bytes. Using the platform's own means the
// probes run on Windows too, which is the point: a boundary asserted only on
// the platforms it was written on is a boundary nobody has checked.
func shellFor(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = os.Getenv("WINDIR")
		}
		if root == "" {
			root = `C:\Windows`
		}
		return filepath.Join(root, "System32", "cmd.exe")
	}
	return "/bin/sh"
}

// shellRun is the argv that runs one script string under shellFor.
func shellRun(t *testing.T, program, script string, args ...string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return append([]string{"/c", script}, args...)
	}
	return append([]string{"-c", script, program}, args...)
}

func TestOSNetworkAndEnvironmentBoundary(t *testing.T) {
	requireBackend(t)
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("OS probes require explicit opt-in")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("network-canary")) }))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	// The probe is a program whose only job is to reach a socket that is
	// certainly listening. curl does it on Unix; powershell does it on
	// Windows, and both are no-ops if the network is genuinely gone, which is
	// what the assertion reads.
	probe, args := networkProbe(t, server.URL)
	worker := Worker{Executable: probe, Registry: toolbox.Registry{Root: t.TempDir(), MaxBytes: 512}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command, err := worker.Command(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.CombinedOutput()
	if err == nil && strings.Contains(string(output), "network-canary") {
		t.Fatal("worker reached local network", string(output))
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "OPENROUTER_API_KEY=") {
			t.Fatal("model key inherited by worker")
		}
	}
}

// networkProbe returns a program and its arguments that would print the
// canary if the network were reachable.
func networkProbe(t *testing.T, url string) (string, []string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "/usr/bin/curl", []string{"--silent", "--max-time", "5", url}
	}
	// PowerShell rather than curl: a curl.exe on PATH on Windows is an
	// unrelated program (the Git-for-Windows one is a shim), and this needs
	// to be the interpreter whose absence or failure is unambiguous.
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	powershell := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	script := "(New-Object System.Net.WebClient).DownloadString('" + url + "')"
	return powershell, []string{"-NoProfile", "-NonInteractive", "-Command", script}
}

func TestOSCancellationStopsHeartbeat(t *testing.T) {
	requireBackend(t)
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("OS probes require explicit opt-in")
	}
	root := t.TempDir()
	heartbeat := filepath.Join(root, "heartbeat")
	guest := heartbeat
	if runtime.GOOS == "linux" {
		guest = "/workspace/heartbeat"
	}
	// A loop that writes until it is stopped. On Windows this is cmd's own
	// loop rather than a shell's, so the boundary being tested is the
	// cancellation and not the presence of sh.
	var script string
	if runtime.GOOS == "windows" {
		script = `for /l %i in (1,1,1000000) do @(echo x>>"%1" & ping -n 1 127.0.0.1 >nul)`
	} else {
		script = `while true; do printf x >> "$1"; sleep 0.03; done`
	}
	worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 512, AllowWrites: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arguments := shellRun(t, "heartbeat", script, guest)
	command, err := worker.Command(ctx, arguments)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
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
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not terminate")
	}
	before, _ := os.ReadFile(heartbeat)
	time.Sleep(200 * time.Millisecond)
	after, _ := os.ReadFile(heartbeat)
	if len(before) != len(after) {
		t.Fatal("work continued after cancellation")
	}
}
