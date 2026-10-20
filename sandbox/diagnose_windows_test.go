package sandbox

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/john-nash-rs/captaincode/toolbox"
)

// TestWindowsContainerDiagnosis exists to find out why a container cannot run
// even a command that touches no files. It is opt-in so it never runs as part
// of the suite; it asserts nothing and its output is the point.
//
// It runs the same child twice: once inside the container and once with the
// container attribute left off, everything else identical. If the second run
// works, the launch path, the pipes, the environment and the job are all fine
// and the container is what fails; if both fail, the fault is before the
// container is ever involved.
func TestWindowsContainerDiagnosis(t *testing.T) {
	if os.Getenv("CAPTAINCODE_DIAGNOSE") != "1" {
		t.Skip("set CAPTAINCODE_DIAGNOSE=1 to print the container's view")
	}
	root := t.TempDir()
	worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 4096}}
	run := func(label string, args []string) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command, err := worker.Command(ctx, args)
		if err != nil {
			t.Logf("[%s] Command error: %v", label, err)
			return
		}
		start := time.Now()
		output, err := command.CombinedOutput()
		t.Logf("[%s] err=%v elapsed=%s output=%q", label, err, time.Since(start), output)
	}
	t.Logf("=== inside the container ===")
	run("container:echo", []string{"/c", "echo hello"})
	run("container:exit0", []string{"/c", "exit 0"})

	diagnoseWithoutContainer = true
	defer func() { diagnoseWithoutContainer = false }()
	t.Logf("=== container attribute left off ===")
	run("plain:echo", []string{"/c", "echo hello"})
	run("plain:exit0", []string{"/c", "exit 0"})
}
