package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectUnknownAndInvalidConfiguration(t *testing.T) {
	for _, input := range []string{`{"max_turns":0}`, `{"allow_everything":true}`, `{} {}`, `{"tool_timeout_seconds":-1}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(input), 0600)
		if _, err := Load(path); err == nil {
			t.Fatal("accepted", input)
		}
	}
}

// isolate points HOME at a scratch directory so these tests never read or write
// the configuration of whoever is running them.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CAPTAINCODE_CONFIG", "")
	return home
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// There is one configuration file. Confirming it is a fixed, predictable
// location is what lets the README state it in a sentence instead of a table.
func TestResolveUsesTheHomeFile(t *testing.T) {
	home := isolate(t)
	write(t, filepath.Join(home, Name), `{"model":"openrouter/free","max_turns":3}`)

	cfg, path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "openrouter/free" || cfg.MaxTurns != 3 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if want := filepath.Join(home, Name); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

// A project-local file must not be read. It is the entry that made a checkout
// able to change behaviour on someone else's machine, and it shadowed the file
// that actually held the model.
func TestResolveIgnoresAProjectLocalFile(t *testing.T) {
	home := isolate(t)
	write(t, filepath.Join(home, Name), `{"model":"from-home"}`)

	root := t.TempDir()
	write(t, filepath.Join(root, Name), `{"model":"from-project"}`)

	cfg, path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "from-home" {
		t.Fatalf("model = %q, want from-home; a project file must not apply", cfg.Model)
	}
	if path != filepath.Join(home, Name) {
		t.Fatalf("path = %q", path)
	}
}

// A file named with --config is honoured. This is the escape hatch that replaces
// per-project configuration, so it has to keep working.
func TestResolveExplicitFileWins(t *testing.T) {
	home := isolate(t)
	write(t, filepath.Join(home, Name), `{"model":"from-home"}`)

	explicit := filepath.Join(t.TempDir(), "deploy.json")
	write(t, explicit, `{"model":"from-explicit"}`)

	cfg, path, err := Resolve(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "from-explicit" || path != explicit {
		t.Fatalf("got %q from %q", cfg.Model, path)
	}
}

// Silently running on defaults after being pointed at a specific file would
// hide a typo in the path.
func TestResolveExplicitFileMustExist(t *testing.T) {
	isolate(t)
	if _, _, err := Resolve(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("accepted a --config path that does not exist")
	}
}

// Running with no configuration at all is ordinary, so it must not fail. It
// still has to report a path, because that is where /openrouter model writes.
func TestResolveWithoutAnyFileIsNotAnError(t *testing.T) {
	home := isolate(t)
	cfg, path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Fatalf("cfg = %+v, want the defaults", cfg)
	}
	if path != filepath.Join(home, Name) {
		t.Fatalf("path = %q, want %q", path, filepath.Join(home, Name))
	}
}

// A file that exists but does not parse is a mistake, not an absence. Falling
// through would run on defaults and leave the broken file in place.
func TestResolveReportsAnUnreadableFile(t *testing.T) {
	home := isolate(t)
	write(t, filepath.Join(home, Name), `{"allow_everything":true}`)

	if _, _, err := Resolve(""); err == nil {
		t.Fatal("ignored a configuration that does not parse")
	}
}
