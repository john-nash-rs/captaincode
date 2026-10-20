package keyring

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/john-nash-rs/captaincode/internal/testenv"
)

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testenv.SetConfigHome(t, home)
	t.Setenv(EnvVar, "")
	return home
}

// hasMode reports whether a path has the mode the test asked for.
//
// On Windows os.Stat synthesizes 0666 for a normal file and 0755 for a normal
// directory: the mode returned there is a compatibility view built from the
// read-only attribute and carries no permission meaning at all. Asserting 0600
// would therefore be testing a fiction on that platform, so the real check is
// the ACL, which TestKeyFileACLGivesOnlyTheCurrentUser owns.
func hasMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != want {
		t.Errorf("mode of %s = %o, want %o", path, perm, want)
	}
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
	hasMode(t, path, 0o600)
	hasMode(t, filepath.Dir(path), 0o700)
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
