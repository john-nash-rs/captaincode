package toolbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"example.com/agentbook/core"
	"os"
	"path/filepath"
	"testing"
)

func TestValidationPathsAndPolicy(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"version":"2.5.0"}`), 0600)
	registry := Registry{Root: root, MaxBytes: 512}
	for _, args := range []string{`{"path":42}`, `{"path":"../secret"}`, `{"path":"package.json","extra":true}`, `{"path":".env"}`} {
		result := registry.Execute(context.Background(), core.Call{Name: "read_file", Arguments: json.RawMessage(args)})
		if result.OK || result.Code != "invalid_arguments" {
			t.Fatal(args, result)
		}
	}
	result := registry.Execute(context.Background(), core.Call{Name: "write_file", Arguments: json.RawMessage(`{}`)})
	if result.Code != "denied" {
		t.Fatal(result)
	}
	result = registry.Execute(context.Background(), core.Call{Name: "read_file", Arguments: json.RawMessage(`{"path":"missing"}`)})
	if result.Code != "not_found" {
		t.Fatal(result)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "alias"))
	result = registry.Execute(context.Background(), core.Call{Name: "read_file", Arguments: json.RawMessage(`{"path":"alias"}`)})
	if result.OK {
		t.Fatal("symlink escaped rooted file API")
	}
}
func TestEditDetectsStaleContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	os.WriteFile(path, []byte("original"), 0600)
	registry := Registry{Root: root, MaxBytes: 512, AllowWrites: true}
	hash := sha256.Sum256([]byte("original"))
	args, _ := json.Marshal(map[string]string{"path": "file.txt", "content": "new", "expected_sha256": hex.EncodeToString(hash[:])})
	call := core.Call{Name: "write_file", Arguments: args}
	if result := registry.Execute(context.Background(), call); !result.OK {
		t.Fatal(result)
	}
	if result := registry.Execute(context.Background(), call); result.Code != "conflict" {
		t.Fatal("stale edit accepted", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatal("wrong file contents")
	}
}
