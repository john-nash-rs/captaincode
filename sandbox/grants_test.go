package sandbox

import (
	"context"
	"testing"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/toolbox"
)

func writeCall() core.Call {
	return core.Call{ID: "c1", Name: "write_file",
		Arguments: []byte(`{"path":"a.txt","content":"x","expected_sha256":"absent"}`)}
}

func hasDefinition(defs []core.Definition, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// Grants used to be captured in the Registry at startup, so a user who turned
// writes on from the interface still had a model that could not write until
// they quit and started again.
func TestGrantsApplyWithoutRestart(t *testing.T) {
	writes := false
	w := Worker{
		Executable: "/nonexistent",
		Registry:   toolbox.Registry{Root: t.TempDir(), MaxBytes: 512},
		Grants:     func() (bool, bool) { return writes, false },
	}

	if hasDefinition(w.Definitions(), "write_file") {
		t.Fatal("write_file is offered before the grant is given")
	}
	writes = true
	if !hasDefinition(w.Definitions(), "write_file") {
		t.Fatal("write_file is still absent after the grant, without a restart")
	}
}

// Turning a grant on must also lift the execution refusal, not just advertise
// the tool. Advertising a tool that is then denied wastes the turn.
func TestExecutionFollowsTheLiveGrant(t *testing.T) {
	writes := false
	w := Worker{
		Executable: "/nonexistent",
		Registry:   toolbox.Registry{Root: t.TempDir(), MaxBytes: 512},
		Grants:     func() (bool, bool) { return writes, false },
	}
	denied := w.Execute(context.Background(), writeCall())
	if denied.Code != "denied" {
		t.Fatalf("write before the grant gave %q, want denied", denied.Code)
	}
	// The executable does not exist, so reaching that failure proves the grant
	// was honoured rather than the call being refused by policy.
	writes = true
	after := w.Execute(context.Background(), writeCall())
	if after.Code == "denied" {
		t.Fatal("write was still denied after the grant")
	}
}

// A worker with no Grants callback keeps its Registry flags, so the existing
// construction sites stay valid.
func TestRegistryFlagsRemainTheDefault(t *testing.T) {
	w := Worker{
		Executable: "/nonexistent",
		Registry:   toolbox.Registry{Root: t.TempDir(), MaxBytes: 512, AllowWrites: true},
	}
	if !hasDefinition(w.Definitions(), "write_file") {
		t.Fatal("the Registry flags are no longer honoured when Grants is unset")
	}
}
