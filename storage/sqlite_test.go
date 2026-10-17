package storage

import (
	"context"
	"github.com/john-nash-rs/captaincode/core"
	"path/filepath"
	"testing"
)

func TestReopenAndExclusiveOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(path); err == nil {
		other.Close()
		t.Fatal("two owners admitted")
	}
	run := core.NewRun("durable", "version?")
	if err := store.Save(context.Background(), run, "created"); err != nil {
		t.Fatal(err)
	}
	if err := store.Bind(context.Background(), "/workspace-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Bind(context.Background(), "/workspace-b"); err == nil {
		t.Fatal("workspace changed")
	}
	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recovered, err := store.Load(context.Background(), "durable")
	if err != nil || recovered.Messages[1].Content != "version?" {
		t.Fatal(recovered, err)
	}
}

func TestUnknownSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if reopened, err := Open(path); err == nil {
		reopened.Close()
		t.Fatal("unknown schema accepted")
	}
}

func TestSnapshotAndEventRollbackTogether(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := core.NewRun("atomic", "before")
	if err := store.Save(context.Background(), run, "before"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.Exec(`CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); err != nil {
		t.Fatal(err)
	}
	run.Messages[1].Content = "after"
	if err := store.Save(context.Background(), run, "after"); err == nil {
		t.Fatal("injection missed")
	}
	loaded, err := store.Load(context.Background(), run.ID)
	if err != nil || loaded.Messages[1].Content != "before" {
		t.Fatal("snapshot escaped rollback", err)
	}
}
