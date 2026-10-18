package toolbox

import (
	"context"
	"strings"
	"testing"

	"github.com/john-nash-rs/captaincode/core"
)

// A refusal the model cannot relay leaves the operator stuck. It used to say
// "tool is disabled by current policy", which conveys no remedy at all.
func TestRefusalCarriesTheRemedy(t *testing.T) {
	readonly := Registry{Root: t.TempDir(), MaxBytes: 512}
	got := readonly.Execute(context.Background(), core.Call{Name: "create_file", Arguments: []byte(`{"path":"a"}`)})
	if got.Code != "denied" {
		t.Fatalf("code = %q, want denied", got.Code)
	}
	if !strings.Contains(got.Text, "/permissions writes on") {
		t.Fatalf("the refusal gives no command:\n%s", got.Text)
	}
	// It must also say it applies now, so the operator does not go looking for
	// a restart.
	if !strings.Contains(got.Text, "immediately") {
		t.Errorf("the refusal does not say it takes effect at once:\n%s", got.Text)
	}
}

func TestCommandRefusalNamesTheRightCommand(t *testing.T) {
	readonly := Registry{Root: t.TempDir(), MaxBytes: 512}
	got := readonly.Execute(context.Background(), core.Call{Name: "run_command", Arguments: []byte(`{"program":"/bin/ls","args":[]}`)})
	if got.Code != "denied" {
		t.Fatalf("code = %q, want denied", got.Code)
	}
	if !strings.Contains(got.Text, "/permissions commands on") {
		t.Fatalf("the refusal names the wrong command:\n%s", got.Text)
	}
}

// Every write tool must refuse the same way, not just the one that happens to
// be used most.
func TestEveryWriteToolRefusesWithARemedy(t *testing.T) {
	readonly := Registry{Root: t.TempDir(), MaxBytes: 512}
	for _, name := range []string{"write_file", "append_file", "create_file", "make_directory", "move_path", "delete_path"} {
		got := readonly.Execute(context.Background(), core.Call{Name: name, Arguments: []byte(`{}`)})
		if got.Code != "denied" {
			t.Errorf("%s returned %q, want denied", name, got.Code)
			continue
		}
		if !strings.Contains(got.Text, "/permissions writes on") {
			t.Errorf("%s refusal gives no command: %s", name, got.Text)
		}
	}
}

// The system prompt has to name the capability, or the model looks at a
// read-only tool list and concludes the capability does not exist at all rather
// than that it is switchable.
func TestSystemPromptNamesTheGatedCapability(t *testing.T) {
	lower := strings.ToLower(core.SystemPrompt)
	if !strings.Contains(lower, "grant") {
		t.Errorf("the system prompt never mentions granting:\n%s", core.SystemPrompt)
	}
	if !strings.Contains(lower, "shell") {
		t.Errorf("the system prompt does not rule out shell workarounds:\n%s", core.SystemPrompt)
	}
	// It must NOT assert the capabilities are off. An earlier version did, and
	// the model then refused to write even with writing granted, because it
	// believed the prompt over its own tool list.
	for _, claim := range []string{"are disabled until", "is disabled in this", "you cannot write"} {
		if strings.Contains(lower, claim) {
			t.Errorf("the system prompt asserts %q, which is false when grants are on:\n%s", claim, core.SystemPrompt)
		}
	}
	// It must defer to the tool list as the source of truth.
	if !strings.Contains(lower, "tool list") && !strings.Contains(lower, "your available tools") {
		t.Errorf("the system prompt does not defer to the tool list:\n%s", core.SystemPrompt)
	}
}
