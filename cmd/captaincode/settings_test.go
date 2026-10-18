package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/john-nash-rs/captaincode/config"
)

// A model chosen with /openrouter has to survive the process. This is the
// behaviour that was missing when no configuration file was in play: the
// interface accepted a model and then dropped it, so the same mistake had to be
// repeated in every new session.
func TestStoreModelPersistsToTheResolvedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	os.Unsetenv("CAPTAINCODE_CONFIG")

	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	// No configuration exists yet, which is how this bug was reached.
	cfg, path, err := config.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "" {
		t.Fatalf("precondition: model = %q, want empty", cfg.Model)
	}

	s, err := newSettings(path, "openrouter", cfg.Model, cfg.AllowWrites, cfg.AllowCommands)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StoreModel("openrouter/free"); err != nil {
		t.Fatal(err)
	}

	// Resolve again, as a fresh run would.
	after, _, err := config.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if after.Model != "openrouter/free" {
		t.Fatalf("model after = %q, want openrouter/free", after.Model)
	}
}

// Writing a model must not reset limits that were already configured.
func TestStoreModelPreservesOtherSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	os.Unsetenv("CAPTAINCODE_CONFIG")

	path := filepath.Join(home, config.Name)
	if err := os.WriteFile(path, []byte(`{"model":"old","max_turns":5,"allow_writes":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := newSettings(path, "openrouter", cfg.Model, cfg.AllowWrites, cfg.AllowCommands)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StoreModel("new/model"); err != nil {
		t.Fatal(err)
	}

	after, _, err := config.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if after.MaxTurns != 5 || !after.AllowWrites {
		t.Fatalf("limits lost: %+v", after)
	}
	if after.Model != "new/model" {
		t.Fatalf("model = %q, want new/model", after.Model)
	}
}
