package ui

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/provider"
	"github.com/john-nash-rs/captaincode/toolbox"
)

// The report: the model said it had only read, list and stat tools, and the
// operator had no way to tell that a command would fix it.
//
// This drives the real refusal through the real model, so the message the
// operator ends up reading is the one under test.
func TestLiveModelRelaysThePermissionCommand(t *testing.T) {
	key := os.Getenv("CAPTAINCODE_EVAL_KEY")
	if file := os.Getenv("CAPTAINCODE_EVAL_KEY_FILE"); key == "" && file != "" {
		if data, err := os.ReadFile(file); err == nil {
			if m := regexp.MustCompile(`(?i)api[-_]?key\s*=\s*"?([^"\s]+)"?`).FindSubmatch(data); m != nil {
				key = string(m[1])
			}
		}
	}
	model := os.Getenv("CAPTAINCODE_EVAL_MODEL")
	if key == "" || model == "" {
		t.Skip("set CAPTAINCODE_EVAL_KEY and CAPTAINCODE_EVAL_MODEL")
	}

	// Read-only, exactly as the user had it.
	root := t.TempDir()
	registry := toolbox.Registry{Root: root, MaxBytes: 4096, AllowWrites: false}

	adapter := provider.New(key, model, "")
	adapter.ModelFunc = func() string { return model }

	history := []core.Message{{Role: "system", Content: core.SystemPrompt}}
	history = append(history, core.Message{Role: "user",
		Content: "Create a file called hello.py containing print('hi'). Do it now."})

	for turn := range 3 {
		reply, err := adapter.Complete(context.Background(), history, registry.Definitions())
		if err != nil {
			t.Fatalf("turn %d: %v", turn+1, err)
		}
		if len(reply.Calls) == 0 {
			t.Logf("turn %d answered without calling a tool:\n%s", turn+1, reply.Content)
			// The behaviour this exists for. Left to itself the model looked at
			// a read-only tool list, concluded it had no write capability at
			// all, and told the user to run a shell command by hand.
			//
			// Offering a manual alternative alongside the ask is fine and
			// useful. What is not fine is the ask being absent, or the model
			// claiming the work was done.
			lower := strings.ToLower(reply.Content)
			for _, forbidden := range []string{
				"i have no write", "there is no write", "no write or execute capability",
			} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("the model claimed the capability does not exist (%q):\n%s", forbidden, reply.Content)
				}
			}
			if !strings.Contains(lower, "enable") && !strings.Contains(lower, "grant") {
				t.Errorf("the model did not ask to be given the permission:\n%s", reply.Content)
			}
			if strings.Contains(lower, "created") || strings.Contains(lower, "i've written") {
				t.Errorf("the model claimed to have created a file with no tools:\n%s", reply.Content)
			}
			// It should recognise the capability is gated rather than absent.
			if !strings.Contains(lower, "enable") && !strings.Contains(lower, "grant") {
				t.Errorf("the model did not say the capability can be enabled:\n%s", reply.Content)
			}
			return
		}
		call := reply.Calls[0]
		result := registry.Execute(context.Background(), call)
		history = append(history, reply, core.Message{Role: "tool", CallID: call.ID, Content: result.Text})

		if result.Code == "denied" {
			t.Logf("refusal the model receives:\n%s", result.Text)
			// The refusal must carry the remedy, or the model cannot help.
			if !strings.Contains(result.Text, "/permissions writes on") {
				t.Fatalf("the refusal gives the model nothing to relay:\n%s", result.Text)
			}
			final, err := adapter.Complete(context.Background(), history, registry.Definitions())
			if err != nil {
				t.Fatalf("final turn: %v", err)
			}
			t.Logf("what the user is told:\n%s", final.Content)
			if !strings.Contains(final.Content, "/permissions") {
				t.Errorf("the model did not relay the command:\n%s", final.Content)
			}
			return
		}
	}
}
