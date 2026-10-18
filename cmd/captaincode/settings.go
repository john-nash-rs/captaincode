// Package main wires the harness together.
//
// It also implements the settings surface the terminal interface uses to show
// and change connection state. Keeping that here means the interface package
// contains no filesystem or credential handling at all.
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/john-nash-rs/captaincode/config"
	"github.com/john-nash-rs/captaincode/keyring"
)

// settings implements ui.Settings over the configuration file and the
// credential store.
//
// The configuration path is remembered so that /openrouter model can rewrite
// the same file the operator supplied. The credential lives in its own private
// file and is never written into the configuration, so sharing or backing up a
// configuration file cannot leak it.
//
// It is also the live source of the model identifier: the provider reads it
// per request, so a change made in the interface applies without a restart.
type settings struct {
	path     string
	provider string
	model    string
	writes   bool
	commands bool
}

func newSettings(configPath, provider, model string, writes, commands bool) (*settings, error) {
	if _, err := keyring.Path(); err != nil {
		return nil, err
	}
	return &settings{path: configPath, provider: provider, model: model, writes: writes, commands: commands}, nil
}

// Model returns the model identifier currently in force.
func (s *settings) Model() string { return s.model }

// Status reports connection state. The credential is returned already masked;
// the raw value never leaves the keyring package.
func (s *settings) Status() (provider, model, credential string) {
	if s.provider != "" {
		provider = s.provider
	}
	if s.model != "" {
		model = s.model
	}
	key, err := keyring.Resolve()
	if err != nil {
		return provider, model, "unreadable"
	}
	return provider, model, keyring.Masked(key)
}

// Grants reports the current permissions.
//
// They are held in memory as well as in the file, because the sandbox consults
// them per tool call and a grant given from the interface must apply at once.
func (s *settings) Grants() (bool, bool) { return s.writes, s.commands }

// StoreGrants persists a permission change and applies it immediately.
func (s *settings) StoreGrants(writes, commands bool) error {
	current, err := config.Load(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current.AllowWrites = writes
	current.AllowCommands = commands
	if err := writeConfig(s.path, current); err != nil {
		return err
	}
	s.writes, s.commands = writes, commands
	return nil
}

func (s *settings) StoreCredential(key string) error { return keyring.Store(key) }

func (s *settings) ClearCredential() error { return keyring.Clear() }

// StoreModel persists the model identifier to the configuration in force,
// preserving every other setting. When no configuration file existed the path
// is still the default one, so a model chosen here is picked up by the next run
// instead of being forgotten when the interface closes.
func (s *settings) StoreModel(model string) error {
	s.model = model
	if s.path == "" {
		return nil
	}
	current, err := config.Load(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current.Model = model
	return writeConfig(s.path, current)
}

// CredentialPath reports where the credential is stored so the operator can
// find or delete it.
func (s *settings) CredentialPath() string {
	path, err := keyring.Path()
	if err != nil {
		return "unavailable"
	}
	return path
}

// writeConfig saves a configuration with owner-only permissions, because a
// configuration file may name a private endpoint or model.
func writeConfig(path string, cfg config.Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
