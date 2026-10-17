package sandbox

import (
	"context"
	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/toolbox"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUnavailableSandboxFailsClosed(t *testing.T) {
	worker := Worker{Executable: "/bin/sh", SandboxBinary: "/definitely-missing-sandbox", Registry: toolbox.Registry{Root: t.TempDir(), MaxBytes: 512}}
	result := worker.Execute(context.Background(), core.Call{Name: "read_file", Arguments: []byte(`{"path":"package.json"}`)})
	if result.OK || result.Code != "sandbox_unavailable" {
		t.Fatal(result)
	}
}
func TestOSReadWriteBoundaries(t *testing.T) {
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("set CAPTAINCODE_SANDBOX_TEST=1 for actual OS enforcement probes")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("outside canary"), 0600)
	os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside canary"), 0600)
	worker := Worker{Executable: "/bin/sh", Registry: toolbox.Registry{Root: root, MaxBytes: 512}}
	inside := filepath.Join(root, "inside.txt")
	newFile := filepath.Join(root, "new.txt")
	if runtime.GOOS == "linux" {
		inside = "/workspace/inside.txt"
		newFile = "/workspace/new.txt"
	}
	probe := func(script, path string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command, err := worker.Command(ctx, []string{"-c", script, "probe", path})
		if err != nil {
			return "", err
		}
		output, err := command.CombinedOutput()
		return string(output), err
	}
	if output, err := probe(`cat "$1"`, inside); err != nil || !strings.Contains(output, "inside canary") {
		t.Fatalf("allowed read failed: %s %v", output, err)
	}
	if output, err := probe(`cat "$1"`, outside); err == nil || strings.Contains(output, "outside canary") {
		t.Fatal("outside read succeeded", output)
	}
	if _, err := probe(`printf changed > "$1"`, newFile); err == nil {
		t.Fatal("read-only write succeeded")
	}
	worker.Registry.AllowWrites = true
	if output, err := probe(`printf changed > "$1"`, newFile); err != nil {
		t.Fatal("allowed write failed", output, err)
	}
	if _, err := probe(`printf changed > "$1"`, outside); err == nil {
		t.Fatal("outside write succeeded")
	}
}
