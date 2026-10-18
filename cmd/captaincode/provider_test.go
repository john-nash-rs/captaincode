package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/john-nash-rs/captaincode/config"
	"github.com/john-nash-rs/captaincode/keyring"
)

// A machine that has run /connect has both a model and a credential. Choosing
// the offline provider for it reported no model and no write tools, which is
// what prompted this.
func TestProviderDefaultsToHostedOnceConfigured(t *testing.T) {
	if got := resolveProvider("", "z-ai/glm-5.3", "sk-or-v1-KEY", nil); got != "openrouter" {
		t.Fatalf("provider = %q, want openrouter for a configured machine", got)
	}
}

// A machine with nothing set up must still work, offline. That is the one case
// where the fixture is what was wanted.
func TestProviderFallsBackToFixtureWhenUnconfigured(t *testing.T) {
	for _, tc := range []struct {
		name, model, credential string
		err                     error
	}{
		{"nothing", "", "", nil},
		{"model but no key", "a/b", "", nil},
		{"key but no model", "", "sk-or-v1-KEY", nil},
		{"unreadable keyring", "a/b", "", os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveProvider("", tc.model, tc.credential, tc.err); got != "fixture" {
				t.Fatalf("provider = %q, want fixture", got)
			}
		})
	}
}

// An explicit flag always wins, including asking for the fixture on a machine
// that is configured.
func TestExplicitProviderFlagWins(t *testing.T) {
	if got := resolveProvider("fixture", "a/b", "sk-or-v1-KEY", nil); got != "fixture" {
		t.Fatalf("provider = %q, want the explicit fixture", got)
	}
	if got := resolveProvider("openrouter", "", "", nil); got != "openrouter" {
		t.Fatalf("provider = %q, want the explicit openrouter", got)
	}
}

// resolveStoredCredential reads the credential the way the provider does.
func resolveStoredCredential(context.Context) (string, error) { return keyring.Resolve() }

// The real end to end: no --provider flag, a real credential on disk, and a
// settings file naming a model. The run must use the hosted provider, or the
// operator's configuration is ignored without any warning.
func TestRunWithoutProviderFlagUsesTheConfiguredMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("OPENROUTER_API_KEY", "")

	// Exactly what /connect leaves behind.
	if err := os.WriteFile(filepath.Join(home, config.Name),
		[]byte(`{"model":"z-ai/glm-5.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "Library", "Application Support", "captaincode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openrouter.key"), []byte("sk-or-v1-KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, path, err := config.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model == "" {
		t.Fatal("the configured model was not read")
	}
	credential, credentialErr := resolveStoredCredential(context.Background())
	if got := resolveProvider("", cfg.Model, credential, credentialErr); got != "openrouter" {
		t.Fatalf("provider = %q, want openrouter with a model at %s", got, path)
	}
}

// A machine with no settings at all must not attempt a hosted provider, which
// would fail on a missing model instead of working offline.
func TestRunWithoutProviderFlagOnAFreshMachineIsOffline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("OPENROUTER_API_KEY", "")

	cfg, _, err := config.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	credential, credentialErr := resolveStoredCredential(context.Background())
	if got := resolveProvider("", cfg.Model, credential, credentialErr); got != "fixture" {
		t.Fatalf("provider = %q, want fixture on a fresh machine", got)
	}
}
