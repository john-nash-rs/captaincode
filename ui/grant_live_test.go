package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/sandbox"
	"github.com/john-nash-rs/captaincode/toolbox"
)

// The reported failure, end to end: a read-only session is granted writes from
// the interface, and a real tool call through the real sandboxed worker must
// then succeed.
//
// This is the test that was missing. Every earlier check stopped at the policy
// gate or the tool list, and the layer that actually refused the write — the
// isolation profile — was never exercised, so a grant could look entirely
// effective and still be refused by the OS.
// buildRealBinary compiles the actual command.
//
// The sandboxed worker is this same binary re-executed with the "worker"
// argument, so a test binary is useless here: it has no such subcommand and the
// child exits without answering.
func buildRealBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("CAPTAINCODE_TEST_BINARY"); binary != "" {
		return binary
	}
	binary := filepath.Join(t.TempDir(), "captaincode")
	cmd := exec.Command("go", "build", "-o", binary, "github.com/john-nash-rs/captaincode/cmd/captaincode")
	wd, _ := os.Getwd()
	cmd.Dir = filepath.Dir(wd) // the module root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build the command under test: %v\n%s", err, out)
	}
	return binary
}

func TestGrantMidSessionProducesARealFile(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the real sandbox")
	}
	executable := buildRealBinary(t)
	workspace := t.TempDir()

	grants := false
	worker := sandbox.Worker{
		Executable: executable,
		Registry:   toolbox.Registry{Root: workspace, MaxBytes: 4096},
		Grants:     func() (bool, bool) { return grants, false },
	}

	// Read-only: the call is refused, and the refusal carries the command.
	before := worker.Execute(context.Background(), core.Call{
		ID: "a", Name: "create_file", Arguments: []byte(`{"path":"early.txt"}`),
	})
	if before.Code != "denied" {
		t.Fatalf("precondition: expected denied, got %q", before.Code)
	}
	if !strings.Contains(before.Text, "/permissions writes on") {
		t.Fatalf("the refusal gives no command:\n%s", before.Text)
	}

	// The operator grants it, as /permissions does.
	grants = true

	after := worker.Execute(context.Background(), core.Call{
		ID: "b", Name: "create_file", Arguments: []byte(`{"path":"late.txt"}`),
	})
	// Success carries OK, not a Code; Code is set only on failure.
	if !after.OK {
		t.Fatalf("the call still failed after the grant: %s %s", after.Code, after.Text)
	}
	if _, err := os.Stat(filepath.Join(workspace, "late.txt")); err != nil {
		t.Fatalf("the file does not exist, so the sandbox refused the write: %v", err)
	}
}

// And writing content through the sandbox, not just creating an empty file.
func TestGrantedSessionWritesContentThroughTheSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the real sandbox")
	}
	executable := buildRealBinary(t)
	workspace := t.TempDir()
	worker := sandbox.Worker{
		Executable: executable,
		Registry:   toolbox.Registry{Root: workspace, MaxBytes: 4096},
		Grants:     func() (bool, bool) { return true, false },
	}

	result := worker.Execute(context.Background(), core.Call{
		ID: "c", Name: "write_file",
		Arguments: []byte(`{"path":"content.txt","content":"hello","expected_sha256":"absent"}`),
	})
	if !result.OK {
		t.Fatalf("writing content failed: %s %s", result.Code, result.Text)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "content.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("contents = %q, err = %v", data, err)
	}
}

// A grant is not permission to write outside the project. The profile must
// stay scoped even with writes allowed.
func TestGrantedSessionCannotEscapeTheWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the real sandbox")
	}
	executable := buildRealBinary(t)
	workspace := t.TempDir()
	worker := sandbox.Worker{
		Executable: executable,
		Registry:   toolbox.Registry{Root: workspace, MaxBytes: 4096},
		Grants:     func() (bool, bool) { return true, true },
	}
	for _, escape := range []string{"../escaped.txt", "/tmp/escaped.txt", "sub/../../escaped.txt"} {
		got := worker.Execute(context.Background(), core.Call{
			ID: "d", Name: "create_file",
			Arguments: []byte(`{"path":"` + escape + `"}`),
		})
		if got.OK {
			t.Errorf("%q was created despite the grant", escape)
		}
	}
}

var _ = tea.KeyEnter
var _ = context.Background
