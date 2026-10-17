package evals

import (
	"context"
	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/storage"
	"github.com/john-nash-rs/captaincode/toolbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOfflineTaskCases(t *testing.T) {
	for _, scenario := range []struct {
		name, content, want string
		exists              bool
	}{
		{"version", `{"version":"2.5.0"}`, "2.5.0", true},
		{"missing", "", "not_found", false},
		{"malformed", `{"version":`, "did not contain", true},
		{"wrong_type", `{"version":42}`, "did not contain", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			workspace := t.TempDir()
			if scenario.exists {
				os.WriteFile(filepath.Join(workspace, "package.json"), []byte(scenario.content), 0600)
			}
			store, err := storage.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			tools := toolbox.Registry{Root: workspace, MaxBytes: 512}
			engine := core.Engine{Model: core.Fixture{}, Tools: tools, Store: store, MaxTurns: 3, MaxToolCalls: 2, MaxContextBytes: 4096, ToolTimeout: time.Second}
			run := core.NewRun(scenario.name, "Find the project version without changing files")
			if err := engine.Run(context.Background(), &run); err != nil {
				t.Fatal(err)
			}
			if run.Phase != "completed" || run.ToolCalls != 1 || !strings.Contains(run.Messages[len(run.Messages)-1].Content, scenario.want) {
				t.Fatal(run)
			}
			data, readErr := os.ReadFile(filepath.Join(workspace, "package.json"))
			if scenario.exists && (readErr != nil || string(data) != scenario.content) {
				t.Fatal("read-only task changed fixture")
			}
		})
	}
}

func TestRecoveryUsesCurrentWritePermission(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "new.txt")
	store, err := storage.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	call := core.Call{ID: "planned-write", Name: "write_file", Arguments: []byte(`{"path":"new.txt","content":"changed","expected_sha256":"absent"}`)}
	run := core.NewRun("revoked", "create file")
	run.Phase = "tools"
	run.Messages = append(run.Messages, core.Message{Role: "assistant", Calls: []core.Call{call}})
	run.Pending = []core.Pending{{Call: call, State: "pending"}}
	if err := store.Save(context.Background(), run, "tools_planned"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	engine := core.Engine{Model: core.Fixture{}, Tools: toolbox.Registry{Root: workspace, MaxBytes: 512, AllowWrites: false}, Store: store, MaxTurns: 3, MaxToolCalls: 3, MaxContextBytes: 4096, ToolTimeout: time.Second}
	if err := engine.Recover(&loaded); err != nil {
		t.Fatal(err)
	}
	if err := engine.Run(context.Background(), &loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("revoked write executed")
	}
	if !strings.Contains(loaded.Messages[3].Content, `"code":"denied"`) {
		t.Fatal("missing policy observation")
	}
}
