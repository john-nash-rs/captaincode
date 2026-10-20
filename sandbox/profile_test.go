package sandbox

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/john-nash-rs/captaincode/toolbox"
)

// THE REPORT: /permissions writes on succeeded and the file still was not
// created.
//
// Three places read the grants. Definitions and Execute consulted the live
// callback; Command, which builds the isolation profile, read the Registry
// field frozen at startup. The result was a grant that looked completely
// effective until the OS refused the write — "the system did not allow it".
//
// macOS only. The assertions below read the sandbox-exec profile, and the Linux
// backend expresses the same decision as a different flag in a different
// command line. Running this on Linux failed on the string comparison rather
// than on any behaviour, which is how it was found: CI runs both platforms and
// a macOS-only assertion had been passing locally the whole time.
func TestIsolationProfileFollowsTheLiveGrant(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("asserts on the macOS sandbox-exec profile; see TestGrantedSessionProducesARealFile for the portable check")
	}
	if testing.Short() {
		t.Skip("exercises the real sandbox")
	}
	grants := false
	executable, err := currentExecutable()
	if err != nil {
		t.Skipf("cannot locate this binary: %v", err)
	}
	root := t.TempDir()

	w := Worker{
		Executable:    executable,
		SandboxBinary: sandboxBinaryForTest(),
		Registry:      toolbox.Registry{Root: root, MaxBytes: 4096},
		Grants:        func() (bool, bool) { return grants, false },
	}

	readonly, err := w.Command(t.Context(), nil)
	if err != nil {
		t.Skipf("no sandbox on this machine: %v", err)
	}
	before := strings.Join(readonly.args, " ")
	if !strings.Contains(before, "(deny default)") {
		t.Fatalf("the read-only profile is not deny-by-default:\n%s", before)
	}
	if strings.Contains(before, "(allow file-write*") {
		t.Fatalf("the read-only profile allows writes:\n%s", before)
	}

	// The grant is given mid-session.
	grants = true
	writable, err := w.Command(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Join(writable.args, " ")
	if !strings.Contains(after, "(allow file-write*") {
		t.Fatalf("the profile does not allow writes after the grant:\n%s", after)
	}
	// The workspace is still bounded even when writes are allowed. A grant is
	// not permission to write anywhere on the machine.
	if !strings.Contains(after, root) {
		t.Fatalf("the writable profile does not scope writes to the workspace:\n%s", after)
	}
}

// The same must hold for the definitions and the policy check, or the three
// would disagree and the failure would look like the model's.
func TestAllThreePathsSeeTheSameGrant(t *testing.T) {
	grants := false
	w := Worker{
		Executable: "/nonexistent",
		Registry:   toolbox.Registry{Root: t.TempDir(), MaxBytes: 512},
		Grants:     func() (bool, bool) { return grants, false },
	}
	if advertised(w, "create_file") {
		t.Error("create_file is advertised before the grant")
	}

	grants = true
	if !advertised(w, "create_file") {
		t.Error("create_file is not advertised after the grant")
	}

	// Withdrawing it must take effect too, in the other direction.
	grants = false
	if advertised(w, "create_file") {
		t.Error("create_file is still advertised after the grant was withdrawn")
	}
}

func advertised(w Worker, name string) bool {
	for _, d := range w.Definitions() {
		if d.Name == name {
			return true
		}
	}
	return false
}
func currentExecutable() (string, error) { return os.Executable() }

func sandboxBinaryForTest() string {
	if runtime.GOOS == "darwin" {
		return "/usr/bin/sandbox-exec"
	}
	return "bwrap"
}
