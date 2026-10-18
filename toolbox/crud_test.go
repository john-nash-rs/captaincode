package toolbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/john-nash-rs/captaincode/core"
)

func writableRegistry(t *testing.T) Registry {
	t.Helper()
	return Registry{Root: t.TempDir(), MaxBytes: 4096, AllowWrites: true}
}

func call(t *testing.T, name, args string) core.Result {
	t.Helper()
	return writableRegistry(t).Execute(context.Background(), core.Call{ID: "c", Name: name, Arguments: []byte(args)})
}

// A model told to create a file needs a tool that creates one. Asking it to
// write required knowing the previous hash or the word "absent", which is a
// detail of the harness rather than of the task; create_file does the obvious
// thing.
func TestCreateFile(t *testing.T) {
	r := writableRegistry(t)
	got := r.Execute(context.Background(), core.Call{Name: "create_file", Arguments: []byte(`{"path":"a.txt"}`)})
	if !got.OK {
		t.Fatalf("create failed: %s %s", got.Code, got.Text)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "a.txt")); err != nil {
		t.Fatalf("the file is not there: %v", err)
	}
}

// It must not silently truncate. That is the one behaviour that could lose
// someone's work.
func TestCreateFileRefusesToOverwrite(t *testing.T) {
	r := writableRegistry(t)
	if got := r.Execute(context.Background(), core.Call{Name: "create_file", Arguments: []byte(`{"path":"a.txt"}`)}); !got.OK {
		t.Fatal(got.Text)
	}
	got := r.Execute(context.Background(), core.Call{Name: "create_file", Arguments: []byte(`{"path":"a.txt"}`)})
	if got.OK || got.Code != "conflict" {
		t.Fatalf("a second create returned %q %q, want a conflict", got.Code, got.Text)
	}
}

func TestAppendFileCreatesAndAppends(t *testing.T) {
	r := writableRegistry(t)
	if got := r.Execute(context.Background(), core.Call{Name: "append_file", Arguments: []byte(`{"path":"log.txt","content":"one\n"}`)}); !got.OK {
		t.Fatal(got.Text)
	}
	if got := r.Execute(context.Background(), core.Call{Name: "append_file", Arguments: []byte(`{"path":"log.txt","content":"two\n"}`)}); !got.OK {
		t.Fatal(got.Text)
	}
	data, err := os.ReadFile(filepath.Join(r.Root, "log.txt"))
	if err != nil || string(data) != "one\ntwo\n" {
		t.Fatalf("contents = %q, err = %v", data, err)
	}
}

// Append must respect the edit limit rather than growing a file without bound.
func TestAppendFileRespectsTheOutputLimit(t *testing.T) {
	// A limit that fits one chunk but not two.
	r := Registry{Root: t.TempDir(), MaxBytes: 300, AllowWrites: true}
	chunk := `{"path":"big.txt","content":"` + strings.Repeat("x", 200) + `"}`

	if got := r.Execute(context.Background(), core.Call{Name: "append_file", Arguments: []byte(chunk)}); !got.OK {
		t.Fatalf("the first append was rejected: %s %s", got.Code, got.Text)
	}
	got := r.Execute(context.Background(), core.Call{Name: "append_file", Arguments: []byte(chunk)})
	if got.OK || got.Code != "conflict" {
		t.Fatalf("an over-limit append returned %q %q, want a conflict", got.Code, got.Text)
	}
	// A single chunk larger than the limit is refused outright.
	huge := `{"path":"big.txt","content":"` + strings.Repeat("x", 400) + `"}`
	if got := r.Execute(context.Background(), core.Call{Name: "append_file", Arguments: []byte(huge)}); got.OK {
		t.Fatal("an oversized single append was accepted")
	}
}

func TestMakeDirectoryAndMove(t *testing.T) {
	r := writableRegistry(t)
	if got := r.Execute(context.Background(), core.Call{Name: "make_directory", Arguments: []byte(`{"path":"src/nested"}`)}); !got.OK {
		t.Fatal(got.Text)
	}
	if err := os.WriteFile(filepath.Join(r.Root, "src", "nested", "f.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(context.Background(), core.Call{Name: "move_path", Arguments: []byte(`{"from":"src/nested/f.txt","to":"moved.txt"}`)})
	if !got.OK {
		t.Fatalf("move failed: %s %s", got.Code, got.Text)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "moved.txt")); err != nil {
		t.Fatalf("the moved file is not there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "src", "nested", "f.txt")); !os.IsNotExist(err) {
		t.Fatal("the original is still there")
	}
}

func TestDeleteFile(t *testing.T) {
	r := writableRegistry(t)
	if got := r.Execute(context.Background(), core.Call{Name: "create_file", Arguments: []byte(`{"path":"gone.txt"}`)}); !got.OK {
		t.Fatal(got.Text)
	}
	if got := r.Execute(context.Background(), core.Call{Name: "delete_path", Arguments: []byte(`{"path":"gone.txt"}`)}); !got.OK {
		t.Fatalf("delete failed: %s %s", got.Code, got.Text)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("the file is still there")
	}
	if got := r.Execute(context.Background(), core.Call{Name: "delete_path", Arguments: []byte(`{"path":"gone.txt"}`)}); got.OK {
		t.Fatal("deleting nothing reported success")
	}
}

// Recursively emptying a directory is a much larger action than the call asked
// for, so a non-empty one is refused.
func TestDeleteRefusesANonEmptyDirectory(t *testing.T) {
	r := writableRegistry(t)
	if got := r.Execute(context.Background(), core.Call{Name: "make_directory", Arguments: []byte(`{"path":"d"}`)}); !got.OK {
		t.Fatal(got.Text)
	}
	os.WriteFile(filepath.Join(r.Root, "d", "keep.txt"), []byte("x"), 0600)
	got := r.Execute(context.Background(), core.Call{Name: "delete_path", Arguments: []byte(`{"path":"d"}`)})
	if got.OK || got.Code != "conflict" {
		t.Fatalf("deleting a non-empty directory returned %q %q", got.Code, got.Text)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "d", "keep.txt")); err != nil {
		t.Fatalf("the contents were removed anyway: %v", err)
	}
	// An empty one is fine.
	os.Remove(filepath.Join(r.Root, "d", "keep.txt"))
	if got := r.Execute(context.Background(), core.Call{Name: "delete_path", Arguments: []byte(`{"path":"d"}`)}); !got.OK {
		t.Fatalf("an empty directory was not removed: %s %s", got.Code, got.Text)
	}
}

// The workspace root is outside the tools' remit and no observation here could
// recover from removing it.
func TestMutatingToolsRefuseTheWorkspaceRoot(t *testing.T) {
	r := writableRegistry(t)
	for _, c := range []core.Call{
		{Name: "delete_path", Arguments: []byte(`{"path":"."}`)},
		{Name: "move_path", Arguments: []byte(`{"from":".","to":"x"}`)},
		{Name: "make_directory", Arguments: []byte(`{"path":"."}`)},
		{Name: "create_file", Arguments: []byte(`{"path":"."}`)},
	} {
		got := r.Execute(context.Background(), c)
		if got.OK {
			t.Errorf("%s accepted the workspace root", c.Name)
		}
	}
	if _, err := os.Stat(r.Root); err != nil {
		t.Fatalf("the workspace root is gone: %v", err)
	}
}

// Every path tool must refuse to leave the workspace, however it is spelled.
func TestPathToolsRefuseToEscape(t *testing.T) {
	r := writableRegistry(t)
	for _, c := range []core.Call{
		{Name: "create_file", Arguments: []byte(`{"path":"../escape.txt"}`)},
		{Name: "delete_path", Arguments: []byte(`{"path":"../outside"}`)},
		{Name: "move_path", Arguments: []byte(`{"from":"a.txt","to":"../escape.txt"}`)},
		{Name: "make_directory", Arguments: []byte(`{"path":"../escapedir"}`)},
		{Name: "append_file", Arguments: []byte(`{"path":"/etc/passwd","content":"x"}`)},
	} {
		got := r.Execute(context.Background(), c)
		if got.OK {
			t.Errorf("%s escaped the workspace: %s", c.Name, got.Text)
		}
	}
}

// A write tool must not exist at all without the grant, or the model will
// offer to use one and be refused.
func TestWriteToolsAreAbsentWithoutTheGrant(t *testing.T) {
	readonly := Registry{Root: t.TempDir(), MaxBytes: 512}
	names := map[string]bool{}
	for _, d := range readonly.Definitions() {
		names[d.Name] = true
	}
	for _, absent := range []string{"write_file", "append_file", "create_file", "make_directory", "move_path", "delete_path"} {
		if names[absent] {
			t.Errorf("%s is offered without the grant", absent)
		}
	}
	for _, present := range []string{"read_file", "list_files", "search_files", "stat_file"} {
		if !names[present] {
			t.Errorf("%s is missing from the read-only set", present)
		}
	}
}

// Even if a model asks for one it cannot see, the call must be refused.
func TestWriteToolsAreRefusedWithoutTheGrant(t *testing.T) {
	readonly := Registry{Root: t.TempDir(), MaxBytes: 512}
	for _, c := range []core.Call{
		{Name: "create_file", Arguments: []byte(`{"path":"a.txt"}`)},
		{Name: "append_file", Arguments: []byte(`{"path":"a.txt","content":"x"}`)},
		{Name: "move_path", Arguments: []byte(`{"from":"a","to":"b"}`)},
		{Name: "delete_path", Arguments: []byte(`{"path":"a"}`)},
		{Name: "make_directory", Arguments: []byte(`{"path":"d"}`)},
	} {
		got := readonly.Execute(context.Background(), c)
		if got.Code != "denied" {
			t.Errorf("%s returned %q, want denied", c.Name, got.Code)
		}
	}
}

func TestStatFileReportsAbsenceAndKind(t *testing.T) {
	r := writableRegistry(t)
	if got := r.Execute(context.Background(), core.Call{Name: "stat_file", Arguments: []byte(`{"path":"nope.txt"}`)}); got.Text != "absent" {
		t.Fatalf("absent reported as %q", got.Text)
	}
	os.WriteFile(filepath.Join(r.Root, "there.txt"), []byte("hello"), 0600)
	got := r.Execute(context.Background(), core.Call{Name: "stat_file", Arguments: []byte(`{"path":"there.txt"}`)})
	if !strings.Contains(got.Text, "file") || !strings.Contains(got.Text, "5") {
		t.Fatalf("stat reported %q", got.Text)
	}
}

// stat is read-only and must be available without any grant.
func TestStatIsAvailableReadOnly(t *testing.T) {
	readonly := Registry{Root: t.TempDir(), MaxBytes: 512}
	got := readonly.Execute(context.Background(), core.Call{Name: "stat_file", Arguments: []byte(`{"path":"x"}`)})
	if got.Code == "denied" {
		t.Fatal("stat_file needs a grant it should not")
	}
}

func TestNeedsWriteCoversEveryMutatingTool(t *testing.T) {
	for _, name := range []string{"write_file", "append_file", "create_file", "make_directory", "move_path", "delete_path"} {
		if !needsWrite(name) {
			t.Errorf("%s is not classified as a write", name)
		}
	}
	for _, name := range []string{"read_file", "list_files", "search_files", "stat_file", "run_command"} {
		if needsWrite(name) {
			t.Errorf("%s is wrongly classified as a write", name)
		}
	}
}
