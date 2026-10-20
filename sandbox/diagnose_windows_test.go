package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/john-nash-rs/captaincode/toolbox"
)

// TestWindowsContainerDiagnosis exists to print what a container can actually
// see, because the boundary failures on Windows report only an exit code and a
// shell that says nothing. It is opt-in so it never runs as part of the suite;
// it asserts nothing and its output is the point.
func TestWindowsContainerDiagnosis(t *testing.T) {
	if os.Getenv("CAPTAINCODE_DIAGNOSE") != "1" {
		t.Skip("set CAPTAINCODE_DIAGNOSE=1 to print the container's view")
	}
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside canary"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := func(label string, args []string) {
		worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 4096}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command, err := worker.Command(ctx, args)
		if err != nil {
			t.Logf("[%s] Command error: %v", label, err)
			return
		}
		output, err := command.CombinedOutput()
		t.Logf("[%s] err=%v\n%s", label, err, output)
	}
	inside := filepath.Join(root, "inside.txt")
	// Does the shell run at all?
	probe("echo", []string{"/c", "echo hello"})
	// Does it know where it is, and can it list the workspace?
	probe("cwd+dir", []string{"/c", `cd & dir`})
	// Can it read a file by absolute path, with the error spelled out?
	probe("type", []string{"/c", `type "` + inside + `"`})
	// Can it write at all, not at all?
	probe("write", []string{"/c", `echo x>"` + filepath.Join(root, "new.txt") + `" & echo wrote`})
	// Does a read of something outside the workspace fail differently?
	probe("outside", []string{"/c", `type "` + os.Getenv("COMSPEC") + `"`})
}
