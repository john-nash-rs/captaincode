package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/toolbox"
)

func TestUnavailableSandboxFailsClosed(t *testing.T) {
	worker := Worker{
		Executable:    "/bin/sh",
		SandboxBinary: "/definitely-missing-sandbox",
		Registry:      toolbox.Registry{Root: t.TempDir(), MaxBytes: 512},
	}
	result := worker.Execute(context.Background(), core.Call{Name: "read_file", Arguments: []byte(`{"path":"package.json"}`)})
	if result.OK || result.Code != "sandbox_unavailable" {
		t.Fatal(result)
	}
}

// THE BOUNDARY, MEASURED RATHER THAN DESCRIBED.
//
// Everything else in this package checks a decision. This one checks the
// result: a real program, started the way a tool call starts one, trying to
// read outside the workspace, to write inside it without permission, and to
// write outside it with permission. Each attempt reports what the operating
// system did.
//
// It is written to run on all three platforms on purpose. The macOS and Linux
// backends are command lines that this package builds and the test can read;
// the Windows backend is a container the process is born into, and nothing
// readable describes it. The only way to know the container is real is to make
// something try to escape it.
func TestOSReadWriteBoundaries(t *testing.T) {
	requireBackend(t)
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("set CAPTAINCODE_SANDBOX_TEST=1 for actual OS enforcement probes")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside canary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside canary"), 0600); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 512}}
	inside := filepath.Join(root, "inside.txt")
	newFile := filepath.Join(root, "new.txt")
	if runtime.GOOS == "linux" {
		// The Linux backend mounts the workspace at a fixed path.
		inside = "/workspace/inside.txt"
		newFile = "/workspace/new.txt"
	}
	workspaceACL := "(not captured)"
	probe := func(script string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command, err := worker.Command(ctx, shellRun(t, "probe", script, args...))
		if err != nil {
			return "", err
		}
		var output strings.Builder
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			return output.String(), err
		}
		workspaceACL = windowsACLForTest(t, root)
		err = command.Wait()
		return output.String(), err
	}
	// t.TempDir paths on the hosted Windows runner contain no spaces. Avoid
	// nested quotes here: cmd.exe's /c parser does not use the C-runtime quote
	// rules used by CreateProcess command-line composition.
	read := script{unix: `cat "$1"`, windows: `type %~1`}
	write := script{unix: `printf changed > "$1"`, windows: `echo changed>%~1`}

	if output, err := probe(read.forPlatform(), inside); err != nil || !strings.Contains(output, "inside canary") {
		t.Fatalf("allowed read failed: %s %v", output, err)
	}
	if output, err := probe(read.forPlatform(), outside); err == nil || strings.Contains(output, "outside canary") {
		t.Fatalf("outside read succeeded: %s", output)
	}
	output, err := probe(write.forPlatform(), newFile)
	// cmd.exe can report success even when its redirection was denied, so
	// verify the filesystem effect as well as the process exit status.
	if _, statErr := os.Stat(filepath.Join(root, "new.txt")); statErr == nil {
		t.Fatalf("read-only write created a file: %s (command error: %v)\nworkspace ACL while worker was running:\n%s", output, err, workspaceACL)
	}
	worker.Registry.AllowWrites = true
	if output, err := probe(write.forPlatform(), newFile); err != nil {
		t.Fatalf("allowed write failed: %s %v", output, err)
	}
	if output, err := probe(read.forPlatform(), newFile); err != nil || !strings.Contains(output, "changed") {
		t.Fatalf("the worker could not read its allowed write: %s %v", output, err)
	}
	if output, err := probe(write.forPlatform(), outside); err == nil {
		t.Fatalf("outside write succeeded: %s", output)
	}
}

// script is one probe in the two dialects the platforms' shells speak. Keeping
// both in one value is what stops the Windows half from drifting away from the
// assertion it is supposed to satisfy.
type script struct {
	unix    string
	windows string
}

func (s script) forPlatform() string {
	if runtime.GOOS == "windows" {
		return s.windows
	}
	return s.unix
}

// secrets that live inside a workspace must stay unreadable even when writes
// are on, because turning writes on is not a decision to hand over a key.
func TestTheCageRefusesHomeSecrets(t *testing.T) {
	requireBackend(t)
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("set CAPTAINCODE_SANDBOX_TEST=1 for actual OS enforcement probes")
	}
	root := t.TempDir()
	secret := filepath.Join(root, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(secret), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("secret canary"), 0600); err != nil {
		t.Fatal(err)
	}
	// A file the worker is allowed to read, for the same reasons as the
	// positive control below: without it this test passes just as happily
	// when the worker cannot run at all, which is the failure it exists to
	// catch a version of.
	if err := os.WriteFile(filepath.Join(root, "readable.txt"), []byte("public canary"), 0600); err != nil {
		t.Fatal(err)
	}
	// Writes on, which is the mode that has to widen the workspace and is
	// therefore the mode where the deny is load-bearing.
	worker := Worker{
		Executable: shellFor(t),
		Registry:   toolbox.Registry{Root: root, MaxBytes: 512, AllowWrites: true},
	}
	visible := secret
	control := filepath.Join(root, "readable.txt")
	if runtime.GOOS == "linux" {
		visible = "/workspace/.ssh/id_rsa"
		control = "/workspace/readable.txt"
	}
	probe := script{unix: `cat "$1"`, windows: `type %~1`}.forPlatform()
	secretACL := "(not captured)"
	read := func(path string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command, err := worker.Command(ctx, shellRun(t, "probe", probe, path))
		if err != nil {
			return "", err
		}
		var output strings.Builder
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			return output.String(), err
		}
		secretACL = windowsACLForTest(t, secret)
		err = command.Wait()
		return output.String(), err
	}
	// The control first. A read that is supposed to work proves the worker
	// can start, can reach the workspace, and can use the shell; only then
	// does a read that fails mean the deny is what stopped it.
	if output, err := read(control); err != nil || !strings.Contains(output, "public canary") {
		t.Fatalf("the worker could not read a file it is allowed to read, so this test cannot tell a denial from a broken worker:\n%s%v", output, err)
	}
	if output, _ := read(visible); strings.Contains(output, "secret canary") {
		t.Fatalf("the worker read a credential inside the workspace:\n%s\nsecret ACL while worker was running:\n%s", output, secretACL)
	}
}

func windowsACLForTest(t *testing.T, path string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "(not Windows)"
	}
	output, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		return string(output) + "\nicacls error: " + err.Error()
	}
	quotedPath := strings.ReplaceAll(path, "'", "''")
	command := "$acl = Get-Acl -LiteralPath '" + quotedPath + "'; $acl.Sddl"
	sddl, sddlErr := exec.Command("powershell.exe", "-NoProfile", "-Command", command).CombinedOutput()
	if sddlErr != nil {
		return string(output) + "\nSDDL error: " + sddlErr.Error() + "\n" + string(sddl)
	}
	return string(output) + "\nSDDL: " + strings.TrimSpace(string(sddl))
}

// A read-only worker must not be able to write, and the workspace it was
// pointed at must be exactly as it was found.
func TestReadOnlyLeavesTheWorkspaceAlone(t *testing.T) {
	requireBackend(t)
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("set CAPTAINCODE_SANDBOX_TEST=1 for actual OS enforcement probes")
	}
	root := t.TempDir()
	target := filepath.Join(root, "new.txt")
	// What the worker is told to write. On Linux the backend mounts the
	// workspace at a fixed path, so the host path is not what the worker
	// sees; naming it would fail for a reason that has nothing to do with
	// writes being off.
	guest := target
	if runtime.GOOS == "linux" {
		guest = "/workspace/new.txt"
	}
	write := script{unix: `printf changed > "$1"`, windows: `echo changed>%~1`}.forPlatform()
	attempt := func(writes bool) error {
		worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 512, AllowWrites: writes}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command, err := worker.Command(ctx, shellRun(t, "probe", write, guest))
		if err != nil {
			return err
		}
		_, err = command.CombinedOutput()
		return err
	}

	if err := attempt(false); err == nil {
		t.Fatal("a read-only worker wrote a file without error")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("a read-only worker created a file")
	}
	// The positive control. Without it this test would pass on a machine
	// where the worker cannot run at all, because a command that never
	// starts also never creates the file. Turning writes on and watching the
	// same shell succeed is what makes the refusal above mean "the sandbox
	// stopped it" rather than "nothing happened".
	if err := attempt(true); err != nil {
		t.Fatalf("writes were allowed but the same write still failed:\n%v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("writes were allowed but no file appeared: %v", err)
	}
}
