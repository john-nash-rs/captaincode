package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/john-nash-rs/captaincode/config"
	"github.com/john-nash-rs/captaincode/internal/testenv"
	"time"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/sandbox"
	"github.com/john-nash-rs/captaincode/toolbox"
)

// THE REPORT: /permissions writes on succeeded, the file was not created.
//
// The grant is stored in settings and read back through a callback the sandbox
// worker consults per call. This walks the real chain — settings, the worker's
// grants callback, the tool definitions the model is shown, and the refusal
// path — starting from a read-only session and granting mid-flight, which is
// what an operator does.
func TestLiveGrantReachesTheWorkerMidSession(t *testing.T) {
	home := t.TempDir()
	testenv.SetConfigHome(t, home)
	os.Unsetenv("CAPTAINCODE_CONFIG")

	configPath := filepath.Join(home, ".captaincode.json")
	// Start read-only, as a first run is.
	if err := os.WriteFile(configPath, []byte(`{"model":"a/b","allow_writes":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	settings, err := newSettings(configPath, "openrouter", "a/b", false, false)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	worker := sandbox.Worker{
		Executable: "/nonexistent",
		Registry:   toolbox.Registry{Root: workspace, MaxBytes: 4096},
		Grants:     settings.Grants,
	}

	if hasTool(worker.Definitions(), "create_file") {
		t.Fatal("create_file is offered before the grant")
	}

	// The operator grants it, exactly as /permissions does.
	if err := settings.StoreGrants(true, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "probe.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !hasTool(worker.Definitions(), "create_file") {
		t.Fatalf("create_file is still absent after the grant; the worker is reading a frozen value.\n"+
			"Registry flags: %+v", worker.Registry)
	}

	// And the call must not be refused by policy.
	result := worker.Execute(context.Background(), core.Call{
		ID: "c1", Name: "create_file", Arguments: []byte(`{"path":"made.txt"}`),
	})
	if result.Code == "denied" {
		t.Fatalf("the call was denied after the grant: %s", result.Text)
	}

	// The refusal a model would see must name the command, so it can tell the
	// user rather than going quiet.
	readonly := toolbox.Registry{Root: workspace, MaxBytes: 4096}
	refused := readonly.Execute(context.Background(), core.Call{
		ID: "c2", Name: "create_file", Arguments: []byte(`{"path":"x"}`),
	})
	if !strings.Contains(refused.Text, "/permissions writes on") {
		t.Fatalf("the refusal gives no command:\n%s", refused.Text)
	}
}

func hasTool(defs []core.Definition, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// The persisted file must be what the next run reads, or the grant appears to
// have been lost even though it was saved.
func TestGrantSurvivesARestart(t *testing.T) {
	home := t.TempDir()
	testenv.SetConfigHome(t, home)
	os.Unsetenv("CAPTAINCODE_CONFIG")

	configPath := filepath.Join(home, ".captaincode.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"a/b","allow_writes":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := newSettings(configPath, "openrouter", "a/b", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.StoreGrants(true, false); err != nil {
		t.Fatal(err)
	}

	// A new process.
	cfg, path, err := config.Resolve(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowWrites {
		t.Fatalf("the grant was not persisted to %s", path)
	}
	second, err := newSettings(path, "openrouter", cfg.Model, cfg.AllowWrites, cfg.AllowCommands)
	if err != nil {
		t.Fatal(err)
	}
	if writes, _ := second.Grants(); !writes {
		t.Fatal("a restarted session does not see the grant")
	}
	_ = time.Now
}
