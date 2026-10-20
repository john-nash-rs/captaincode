// Package testenv holds the platform details that tests need to get right on
// every operating system CaptainCode supports.
//
// It exists because a test that only redirects HOME passes on Linux and macOS
// and quietly reads the real configuration directory on Windows, where
// os.UserHomeDir reads USERPROFILE and os.UserConfigDir reads APPDATA. Such a
// test either pollutes the profile of whoever ran it or fails on a runner, and
// in both cases it is not testing what it claims to test.
package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// SetConfigHome points every platform's idea of "this user's files" at home, so
// a test never reads or writes the configuration of whoever is running it.
//
// It redirects both of the lookups the code actually uses:
//
//	os.UserHomeDir   -> HOME on Unix, USERPROFILE on Windows, and that is where
//	                    config.Resolve finds ~/.captaincode.json
//	os.UserConfigDir -> XDG_CONFIG_HOME then HOME on Unix, APPDATA on Windows,
//	                    and that is where keyring.Path and defaultDatabasePath
//	                    find their files
//
// XDG_CONFIG_HOME is pointed at home/xdg rather than home/.config on purpose:
// several tests exist to prove that a project-local or XDG configuration file
// is *not* the one in force, and pointing it at the same directory the config
// file uses would make that proof vacuous.
//
// CAPTAINCODE_CONFIG is left alone. Tests that need it cleared already clear
// it, and hiding that from the reader of the test would make the test lie.
func SetConfigHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
}

// ConfigDir is what os.UserConfigDir resolves to for a process started with
// ConfigEnv(home), computed rather than inherited.
//
// It exists because a test that needs to place a file where a child will look
// for it cannot ask the child, and must not redirect its own HOME to find out:
// the test process is usually mid-build, and turning HOME into a temporary
// directory turns the Go module cache into one too.
func ConfigDir(home string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming") // %APPDATA%
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	default:
		// XDG_CONFIG_HOME as ConfigEnv sets it; HOME is the fallback the
		// standard library uses only when it is unset.
		return filepath.Join(home, "xdg")
	}
}

// ConfigEnv is SetConfigHome in the form a child process needs: an environment
// is a replacement, not a merge, so anything not listed is absent downstream.
//
// The Windows entries carry SystemRoot and ComSpec because a child handed an
// environment without them cannot find cmd.exe or load system DLLs, and a
// harness that cannot start a shell would report that as a sandbox failure.
func ConfigEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "xdg"),
		"APPDATA=" + filepath.Join(home, "AppData", "Roaming"),
	}
	if runtime.GOOS == "windows" {
		env = append(env,
			"SystemRoot="+systemRoot(),
			"WINDIR="+systemRoot(),
			"ComSpec="+filepath.Join(systemRoot(), "System32", "cmd.exe"),
		)
	}
	return env
}

// systemRoot returns the Windows system directory's root, read from the
// environment rather than assumed, because a machine with Windows on D: is
// legal and hardcoding C:\ would not work there.
func systemRoot() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return root
	}
	if root := os.Getenv("WINDIR"); root != "" {
		return root
	}
	return `C:\Windows`
}
