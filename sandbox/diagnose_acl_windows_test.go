//go:build windows

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/john-nash-rs/captaincode/toolbox"
)

func TestWindowsACLDiagnosis(t *testing.T) {
	if os.Getenv("CAPTAINCODE_DIAGNOSE") != "1" {
		t.Skip("diagnostic only")
	}
	root := t.TempDir()
	secret := filepath.Join(root, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(secret), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("secret canary"), 0600); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 4096, AllowWrites: true}}
	for name, script := range map[string]string{
		"token":       "whoami /all",
		"root-acl":    "icacls " + root,
		"secret-acl":  "icacls " + filepath.Dir(secret),
		"secret-read": "type " + secret,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		command, err := worker.Command(ctx, []string{"/c", script})
		if err != nil {
			cancel()
			t.Logf("%s command: %v", name, err)
			continue
		}
		output, err := command.CombinedOutput()
		cancel()
		t.Logf("%s err=%v output:\n%s", name, err, output)
	}
}
