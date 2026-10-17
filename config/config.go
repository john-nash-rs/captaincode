package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
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
