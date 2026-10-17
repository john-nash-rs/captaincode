package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectUnknownAndInvalidConfiguration(t *testing.T) {
	for _, input := range []string{`{"max_turns":0}`, `{"allow_everything":true}`, `{} {}`, `{"tool_timeout_seconds":-1}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(input), 0600)
		if _, err := Load(path); err == nil {
			t.Fatal("accepted", input)
		}
	}
}
