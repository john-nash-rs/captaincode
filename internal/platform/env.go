// Package platform holds the per-OS environment a child process needs, in one
// place, because two unrelated callers need it: the sandbox worker that runs
// every tool call, and the shell command the model asks for by name. Keeping
// it in one place is what stops a Windows fix from reaching one and not the
// other, which is how a tool comes to be refused by the sandbox when the real
// fault is a missing variable.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// ShellEnv returns the environment entries a child process on this platform
// needs before it can start a shell or load a system library. It returns nil
// on Unix, where neither is needed: the loader finds the dynamic linker and
// the shell by fixed path, and PATH is set by the caller for its own reasons.
//
// On Windows the whole set is required. A process handed an environment with
// no SystemRoot cannot locate cmd.exe and several system DLLs fail to load, so
// the symptom that reaches the operator is "the sandbox failed" rather than
// the missing variable that caused it.
func ShellEnv() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = os.Getenv("WINDIR")
	}
	if root == "" {
		// A machine with Windows somewhere other than C:\ is legal. This is
		// the last resort only, and it is the value the OS itself falls back
		// to when the variables are absent.
		root = `C:\Windows`
	}
	system32 := filepath.Join(root, "System32")
	env := []string{
		"SystemRoot=" + root,
		"WINDIR=" + root,
		"ComSpec=" + filepath.Join(system32, "cmd.exe"),
		"PATH=" + system32 + string(os.PathListSeparator) + root,
	}
	// Left out when unset rather than set to nothing: an empty PATHEXT makes
	// the standard library's LookPath refuse every extension it would have
	// tried, so a tool named `git` stops resolving.
	if ext := os.Getenv("PATHEXT"); ext != "" {
		env = append(env, "PATHEXT="+ext)
	}
	return env
}
