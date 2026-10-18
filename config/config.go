package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type Config struct {
	MaxTurns           int    `json:"max_turns"`
	MaxToolCalls       int    `json:"max_tool_calls"`
	ContextBytes       int    `json:"context_bytes"`
	OutputBytes        int    `json:"output_bytes"`
	ToolTimeoutSeconds int    `json:"tool_timeout_seconds"`
	RunTimeoutSeconds  int    `json:"run_timeout_seconds"`
	Model              string `json:"model"`
	SandboxBinary      string `json:"sandbox_binary"`
	AllowWrites        bool   `json:"allow_writes"`
	AllowCommands      bool   `json:"allow_commands"`
}

func Default() Config {
	return Config{MaxTurns: 12, MaxToolCalls: 24, ContextBytes: 65536, OutputBytes: 8192, ToolTimeoutSeconds: 15, RunTimeoutSeconds: 180}
}

// Name is the one configuration file CaptainCode reads.
const Name = ".captaincode.json"

// Path returns the configuration file, whether or not it exists.
//
// There is exactly one, in the home directory. An earlier version searched an
// environment variable, the project, the home directory, and the user config
// directory in that order, and every user then had to work out which of the
// four was theirs and why the one they were editing was being ignored. The
// project-local entry was the worst of them: it silently shadowed the file
// holding an API key, and a checkout that happened to include one would
// change behaviour on someone else's machine.
//
// Per-project behaviour is still available, by naming the file:
// --config ./deploy.json. That is explicit, visible in the command, and
// reported by doctor.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, Name), nil
}

// Resolve loads the configuration in force and reports the file it came from.
//
// The returned path is where a setting change belongs. It is never empty: with
// no --config flag it is the single home-directory file, so /openrouter model
// writes a file the next run will find again.
//
// A file named with --config must exist, because asking for a specific file and
// silently getting defaults instead hides a typo. The home-directory file need
// not exist.
func Resolve(explicit string) (Config, string, error) {
	if explicit != "" {
		cfg, err := Load(explicit)
		return cfg, explicit, err
	}
	path, err := Path()
	if err != nil {
		return Default(), "", err
	}
	cfg, err := Load(path)
	switch {
	case err == nil:
		return cfg, path, nil
	case errors.Is(err, os.ErrNotExist):
		// Having no configuration at all is ordinary. The path is still
		// returned so a setting chosen later has somewhere to be written.
		return Default(), path, nil
	default:
		// The file exists but does not parse. Reporting it beats falling
		// back to defaults, which would leave the broken file in place and
		// let every later run quietly ignore it.
		return cfg, path, err
	}
}

func Load(path string) (Config, error) {
	config := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return config, err
		}
		if len(data) > 65536 {
			return config, errors.New("configuration too large")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return config, err
		}
		if decoder.Decode(new(any)) != io.EOF {
			return config, errors.New("trailing configuration data")
		}
	}
	return config, config.Validate()
}

func (c Config) Validate() error {
	if c.MaxTurns < 1 || c.MaxTurns > 100 || c.MaxToolCalls < 1 || c.MaxToolCalls > 200 || c.ContextBytes < 1024 || c.ContextBytes > 1000000 || c.OutputBytes < 128 || c.OutputBytes > 32768 || c.ToolTimeoutSeconds < 1 || c.ToolTimeoutSeconds > 120 || c.RunTimeoutSeconds < 1 || c.RunTimeoutSeconds > 1800 {
		return errors.New("configuration limit outside supported range")
	}
	return nil
}
