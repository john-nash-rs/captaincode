// Package keyring resolves and stores the provider credential.
//
// The credential never becomes part of a conversation. It is read from the
// environment or from a private file at request time and handed straight to
// the HTTP transport, so it is never rendered, never logged, and never placed
// in a message that would be sent to a model.
package keyring

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvVar is checked first so an operator can override the stored file for
	// one shell session without editing anything.
	EnvVar = "OPENROUTER_API_KEY"
	// keyDir and keyFile live under the user config directory rather than the
	// project, so exposing a workspace never exposes the credential.
	keyDir  = "captaincode"
	keyFile = "openrouter.key"
	// maxKeyBytes bounds what may be stored, matching the transport's refusal
	// to accept an unbounded credential.
	maxKeyBytes = 512
)

// Path returns the file the credential is stored in. It is reported to the
// user so they can delete it, not read into any transcript.
func Path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return PathIn(base), nil
}

// PathIn is Path with the configuration directory named instead of looked up,
// so a caller that already knows where a process will look can compute the
// same location without changing its environment. The layout stays in one
// place: only this function knows the directory and file names.
func PathIn(configDir string) string {
	return filepath.Join(configDir, keyDir, keyFile)
}

// Resolve returns the credential, preferring the environment so a temporary
// override does not require touching the stored file. An empty string means
// no credential is configured; that is not an error, because the interface
// must still start and let the user supply one.
func Resolve() (string, error) {
	if key := strings.TrimSpace(os.Getenv(EnvVar)); key != "" {
		return key, nil
	}
	path, err := Path()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Store writes the credential to a private file. The directory is created
// 0700 and the file 0600 so other users on the machine cannot read it, and
// the file is written through a temporary name so a crash cannot leave a
// partially written credential behind.
func Store(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("refusing to store an empty credential")
	}
	if len(key) > maxKeyBytes {
		return fmt.Errorf("credential longer than %d bytes", maxKeyBytes)
	}
	if strings.ContainsAny(key, "\r\n\x00") {
		return errors.New("credential contains control characters")
	}
	path, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := private(dir, 0o700); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		os.Remove(temp)
		return err
	}
	return private(path, 0o600)
}

// Clear removes the stored credential. The environment override, if any, is
// unaffected because this package does not own it.
func Clear() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Masked renders a credential for display. It reveals at most the final four
// characters and never the prefix beyond a fixed hint, so a screenshot or a
// shared terminal cannot leak the key. An absent credential reports itself as
// absent rather than as an empty string that looks like a rendering failure.
func Masked(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "not set"
	}
	if len(key) <= 8 {
		return "set (too short to display)"
	}
	return "set ····" + key[len(key)-4:]
}
