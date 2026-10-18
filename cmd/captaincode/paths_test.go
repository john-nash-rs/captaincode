package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultDatabasePathIsPerWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, err := defaultDatabasePath("/Users/someone/code/api")
	if err != nil {
		t.Fatal(err)
	}
	b, err := defaultDatabasePath("/Users/someone/code/web")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("different workspaces share a database: %s", a)
	}
	// Deterministic: the same workspace must always map to the same file so a
	// run can be resumed without repeating --db.
	again, err := defaultDatabasePath("/Users/someone/code/api")
	if err != nil {
		t.Fatal(err)
	}
	if again != a {
		t.Fatalf("unstable path: %s then %s", a, again)
	}
	if name := filepath.Base(a); !strings.HasPrefix(name, "api-") {
		t.Errorf("filename should carry a readable prefix, got %q", name)
	}
}

// Two directories with the same name in different parents are the collision the
// hash exists to prevent.
func TestDefaultDatabasePathSeparatesSameNamedDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, _ := defaultDatabasePath("/one/thing")
	b, _ := defaultDatabasePath("/two/thing")
	if a == b {
		t.Fatalf("same-named workspaces collided: %s", a)
	}
}

func TestDefaultDatabasePathHandlesAwkwardNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"/", "/Users/x/we ird", "/Users/x/../../etc", "/Users/x/.hidden"} {
		got, err := defaultDatabasePath(name)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		base := filepath.Base(got)
		if strings.Contains(base, "..") || strings.HasPrefix(base, ".") {
			t.Errorf("%q produced an unsafe filename %q", name, base)
		}
		if !strings.HasSuffix(base, ".db") {
			t.Errorf("%q produced %q", name, base)
		}
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"MyProject":  "myproject",
		"my project": "my-project",
		"a//b":       "a-b",
		"..":         "",
		// Underscores are legal in a filename and stay legible; only
		// path separators and other punctuation are folded to "-".
		"___": "___",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPermuteArgs(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"."}, []string{"."}},
		{[]string{".", "--ide=false"}, []string{"--ide=false", "."}},
		{[]string{"--db", "/tmp/x.db", "."}, []string{"--db", "/tmp/x.db", "."}},
		{[]string{".", "--db", "/tmp/x.db"}, []string{"--db", "/tmp/x.db", "."}},
		{[]string{"--db=/tmp/y.db", "."}, []string{"--db=/tmp/y.db", "."}},
		{[]string{".", "-ide=false", "--stream"}, []string{"-ide=false", "--stream", "."}},
		// A boolean flag must not swallow the directory that follows it.
		{[]string{"--ide=false", "."}, []string{"--ide=false", "."}},
		{[]string{".", "--stream", "extra"}, []string{"--stream", ".", "extra"}},
	} {
		got := permuteArgs(c.in)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("permuteArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Everything after "--" is positional and must not be reordered or parsed.
func TestPermuteArgsStopsAtDoubleDash(t *testing.T) {
	got := permuteArgs([]string{"--stream", "--", "--ide=false", "."})
	want := []string{"--stream", "--ide=false", "."}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("permuteArgs = %q, want %q", got, want)
	}
}

func TestFlagName(t *testing.T) {
	for in, want := range map[string]string{
		"-tui": "tui", "--tui": "tui", "-db=/tmp/x": "db", "--ide=false": "ide", "-": "",
	} {
		if got := flagName(in); got != want {
			t.Errorf("flagName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The interface needs a terminal. A pipe or a CI log must produce the fix, not
// a Bubble Tea error about /dev/tty.
func TestNonTTYIsDetected(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("a regular file was reported as a terminal")
	}
}

func TestErrPermissionNamesThePath(t *testing.T) {
	wrapped := errPermission("/tmp/x/captaincode", os.ErrPermission)
	if !strings.Contains(wrapped.Error(), "/tmp/x/captaincode") {
		t.Errorf("error does not name the path: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "chown") {
		t.Errorf("permission error offers no fix: %v", wrapped)
	}
	other := errPermission("/tmp/x", os.ErrNotExist)
	if strings.Contains(other.Error(), "chown") {
		t.Errorf("non-permission error should not suggest chown: %v", other)
	}
}
