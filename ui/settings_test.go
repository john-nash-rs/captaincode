package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/john-nash-rs/captaincode/core"
)

// fakeSettings records what the interface asked for and returns a masked
// credential, exactly as the real implementation promises to.
type fakeSettings struct {
	stored    string
	cleared   bool
	model     string
	statusErr string
}

func (f *fakeSettings) Status() (string, string, string) {
	cred := "not set"
	if f.stored != "" {
		cred = "set ····" + f.stored[len(f.stored)-4:]
	}
	return "openrouter", f.model, cred
}
func (f *fakeSettings) StoreCredential(k string) error { f.stored = k; return nil }
func (f *fakeSettings) ClearCredential() error         { f.cleared = true; f.stored = ""; return nil }
func (f *fakeSettings) StoreModel(m string) error      { f.model = m; return nil }
func (f *fakeSettings) CredentialPath() string         { return "/tmp/keyring/openrouter.key" }

func newCmdModel(s Settings) *model {
	return &model{info: Info{Provider: "openrouter", Settings: s}, theme: newTheme(false), width: 80, height: 20}
}

func typeText(m *model, text string) {
	for _, r := range text {
		m.key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// The central guarantee: a credential typed at the interface must never be
// reachable from anything that could be sent to a model. The transcript is
// exactly the content the engine sees, so it is asserted here.
func TestCredentialNeverEntersTheTranscript(t *testing.T) {
	const secret = "sk-or-v1-SUPERSECRETVALUE9999"
	s := &fakeSettings{}
	m := newCmdModel(s)

	typeText(m, "/openrouter api-key")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeText(m, secret)

	// Before it is even stored, the transcript must not contain it.
	if strings.Contains(strings.Join(m.lines, "\n"), secret) {
		t.Fatal("credential entered the transcript while being typed")
	}
	if strings.Contains(m.View().Content, secret) {
		t.Fatal("credential was rendered while being typed")
	}

	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.stored != secret {
		t.Fatalf("credential was not stored: %q", s.stored)
	}
	joined := strings.Join(m.lines, "\n")
	if strings.Contains(joined, secret) {
		t.Fatalf("credential entered the transcript after saving:\n%s", joined)
	}
	if strings.Contains(m.View().Content, secret) {
		t.Fatal("credential was rendered after saving")
	}
}

func TestSlashCommandIsNotDispatchedAsAPrompt(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "/help")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.queue) != 0 {
		t.Fatalf("command was queued as a prompt: %v", m.queue)
	}
	if m.generation != 0 {
		t.Fatal("command started a run")
	}
}

func TestOrdinaryPromptsStillDispatchAndQueue(t *testing.T) {
	// Idle: the prompt is dispatched immediately, so it leaves the queue and a
	// run begins.
	var asked string
	dispatched := newCmdModel(&fakeSettings{})
	dispatched.runner = func(_ context.Context, q string, _ func(core.Event)) (string, error) {
		asked = q
		return "ok", nil
	}
	typeText(dispatched, "what is the version?")
	_, cmd := dispatched.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no command was returned to start the run")
	}
	// The command is what actually invokes the runner; the runtime executes it.
	if _, ok := cmd().(finished); !ok {
		t.Fatalf("command did not produce a completion message: %T", cmd())
	}
	if asked != "what is the version?" {
		t.Fatalf("idle prompt was not dispatched: %q", asked)
	}
	if dispatched.generation != 1 {
		t.Errorf("generation = %d, want 1", dispatched.generation)
	}

	// Busy: the prompt waits in the queue for the active run to finish.
	busy := newCmdModel(&fakeSettings{})
	busy.running = true
	typeText(busy, "second question")
	busy.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(busy.queue) != 1 || busy.queue[0] != "second question" {
		t.Fatalf("busy prompt was not queued: %v", busy.queue)
	}
	if busy.generation != 0 {
		t.Error("queueing a prompt started a second run")
	}
}

func TestEscapeLeavesCredentialModeWithoutCancelling(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.running = true
	cancelled := false
	m.cancel = func() { cancelled = true }

	typeText(m, "/openrouter api-key")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter}) // enter secret mode
	typeText(m, "sk-or-v1-partial")
	m.key(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.secret {
		t.Fatal("still in credential mode after escape")
	}
	if cancelled {
		t.Fatal("escape cancelled the running request instead of leaving credential mode")
	}
	if len(m.input) != 0 {
		t.Fatal("partial credential was not discarded")
	}
}

func TestEmptyCredentialIsRejected(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	typeText(m, "/openrouter api-key")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.stored != "" {
		t.Fatal("empty credential was stored")
	}
}

func TestModelCommandUpdatesStatus(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	typeText(m, "/openrouter model thinkingmachines/inkling:free")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.model != "thinkingmachines/inkling:free" {
		t.Fatalf("model = %q", s.model)
	}
	if m.info.Model != s.model {
		t.Errorf("footer model not refreshed: %q", m.info.Model)
	}
}

func TestStatusNeverPrintsRawCredential(t *testing.T) {
	s := &fakeSettings{stored: "sk-or-v1-SUPERSECRETVALUE9999"}
	m := newCmdModel(s)
	typeText(m, "/openrouter")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	joined := strings.Join(m.lines, "\n")
	if strings.Contains(joined, "SUPERSECRET") {
		t.Fatalf("status leaked the credential:\n%s", joined)
	}
	if !strings.Contains(joined, "9999") {
		t.Errorf("status should show the last four characters:\n%s", joined)
	}
}

func TestUnknownCommandsAreRejected(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	for _, cmd := range []string{"/nope", "/openrouter bogus", "/openrouter model"} {
		before := len(m.queue)
		typeText(m, cmd)
		m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
		if len(m.queue) != before {
			t.Errorf("%q was queued as a prompt", cmd)
		}
	}
}
