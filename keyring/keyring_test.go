package keyring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(EnvVar, "")
	return home
}

func TestStoreCreatesPrivateFile(t *testing.T) {
	home := isolate(t)
	if err := Store("sk-or-v1-secret-value"); err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, home) {
		t.Fatalf("keyfile %q is outside the home directory %q", path, home)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("keyfile mode = %o, want 600", perm)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("keyfile directory mode = %o, want 700", perm)
	}
}

func TestRoundTrip(t *testing.T) {
	isolate(t)
	if err := Store("sk-or-v1-abc123"); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-or-v1-abc123" {
		t.Errorf("Resolve = %q", got)
	}
}

func TestEnvironmentOverridesFile(t *testing.T) {
	isolate(t)
	if err := Store("sk-or-v1-from-file"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, "sk-or-v1-from-env")
	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-or-v1-from-env" {
		t.Errorf("environment did not win: %q", got)
	}
}

func TestResolveWithoutCredentialIsNotAnError(t *testing.T) {
	isolate(t)
	got, err := Resolve()
	if err != nil {
		t.Fatalf("absent credential returned an error: %v", err)
	}
	if got != "" {
		t.Errorf("Resolve = %q, want empty", got)
	}
}

// A masked credential must never contain the secret. This is the property the
// terminal interface depends on when it prints status.
func TestMaskedNeverLeaksTheKey(t *testing.T) {
	const secret = "sk-or-v1-SUPERSECRETVALUE1234"
	got := Masked(secret)
	if strings.Contains(got, "SUPERSECRET") {
		t.Fatalf("Masked leaked the key: %q", got)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("Masked leaked the whole key: %q", got)
	}
	if !strings.HasSuffix(got, "1234") {
		t.Errorf("Masked = %q, want it to end in the last four characters", got)
	}
	if got := Masked(""); got != "not set" {
		t.Errorf("Masked(empty) = %q", got)
	}
}

func TestStoreRejectsUnusableInput(t *testing.T) {
	isolate(t)
	for name, key := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"newline":    "sk-or\nsecond-line",
		"carriage":   "sk-or\rreturn",
		"null":       "sk-or\x00null",
		"too long":   strings.Repeat("a", maxKeyBytes+1),
	} {
		if err := Store(key); err == nil {
			t.Errorf("Store accepted %s input", name)
		}
	}
}

func TestClearIsIdempotent(t *testing.T) {
	isolate(t)
	if err := Store("sk-or-v1-x"); err != nil {
		t.Fatal(err)
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if err := Clear(); err != nil {
		t.Fatalf("second Clear failed: %v", err)
	}
	got, _ := Resolve()
	if got != "" {
		t.Errorf("Resolve after Clear = %q", got)
	}
}
