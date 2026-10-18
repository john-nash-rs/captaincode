package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/john-nash-rs/captaincode/core"
)

// fakeProbe returns a canned answer so the connection states can be exercised
// without a network call.
type fakeProbe struct {
	results []Result
	called  bool
}

func (f *fakeProbe) Check(_ context.Context, models []string) []Result {
	f.called = true
	if f.results != nil {
		return f.results
	}
	out := make([]Result, 0, len(models))
	for _, m := range models {
		out = append(out, Result{Model: m})
	}
	return out
}

func joined(m *model) string { return strings.Join(m.lines, "\n") }

// The menu is the only way to reach a command without typing it, so arrow keys
// have to reach the same commands typing does.
func TestArrowKeysSelectAndEnterRuns(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "/openrouter")
	// Two commands share this prefix: api-key and model.
	m.syncMenu()
	if len(m.menu.rows) < 2 {
		t.Fatalf("expected several matches, got %v", m.menu.rows)
	}

	first := m.chosen()
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	second := m.chosen()
	if first == second {
		t.Fatal("down did not move the highlight")
	}

	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.chosen(); got != first {
		t.Fatalf("up returned to %q, want %q", got, first)
	}

	// Enter accepts the highlighted row rather than the typed prefix.
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	want := m.chosen()
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.queue) != 0 {
		t.Fatalf("a command was queued as a prompt: %v", m.queue)
	}
	if !strings.Contains(joined(m), "unknown option") && want != "/openrouter api-key" {
		t.Fatalf("expected %q to have run, transcript:\n%s", want, joined(m))
	}
}

// Arrow keys must not hijack prompts. A user editing a multi-step question with
// the arrow keys expects nothing to happen.
func TestArrowKeysDoNotAffectOrdinaryPrompts(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "a normal question")
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := string(m.input); got != "a normal question" {
		t.Fatalf("arrow keys changed the prompt to %q", got)
	}
}

func TestMenuClosesWhenInputStopsMatching(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "/")
	if len(m.menu.rows) == 0 {
		t.Fatal("no suggestions for a bare slash")
	}
	m.input = []rune("a normal question")
	m.syncMenu()
	if len(m.menu.rows) != 0 {
		t.Fatalf("menu survived ordinary text: %v", m.menu.rows)
	}
	if m.chosen() != "" {
		t.Fatal("a row is still chosen with an empty menu")
	}
}

// Typing a space means the operator has chosen a command and is filling in an
// argument, so the menu has to get out of the way.
func TestMenuStopsAtTheFirstArgument(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "/openrouter ")
	m.syncMenu()
	if len(m.menu.rows) != 0 {
		t.Fatalf("menu offered %v while typing an argument", m.menu.rows)
	}
}

// connectModel builds an interface with a probe and a known starting state.
func connectModel(s Settings, p Probe) *model {
	m := newCmdModel(s)
	m.info.Probe = p
	m.info.Models = []string{"openrouter/free"}
	m.info.Provider = "openrouter"
	// Report what the operator actually configured. Without this the footer
	// and the wizard both read as unconfigured whatever the fake holds.
	m.info.Model = s.(*fakeSettings).model
	return m
}

// The whole point: a new user runs one command and ends up connected, without
// hand-writing a JSON file first.
func TestConnectAsksForAKeyThenAModelThenVerifies(t *testing.T) {
	s := &fakeSettings{}
	p := &fakeProbe{}
	m := connectModel(s, p)

	// No credential yet, so the wizard must ask for one.
	m.runCommand("/connect")
	if !m.secret {
		t.Fatal("/connect did not ask for a key")
	}
	// Saving the key mid-wizard continues to the model rather than stopping
	// at a prompt that does not say what to do next.
	m.pendingModel = true
	typeText(m, "sk-or-v1-KEY")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.stored != "sk-or-v1-KEY" {
		t.Fatalf("key not stored: %q", s.stored)
	}
	if !strings.Contains(joined(m), "model") {
		t.Fatalf("wizard did not move on to the model step:\n%s", joined(m))
	}

	// A fresh session with the key stored but no model must ask for the model.
	s.model = ""
	m2 := connectModel(s, p)
	m2.runCommand("/connect")
	if m2.secret {
		t.Fatal("/connect asked for the key again")
	}
	if !strings.Contains(joined(m2), "model") {
		t.Fatalf("/connect did not ask for a model:\n%s", joined(m2))
	}
}

func TestConnectChecksTheConnectionOnceConfigured(t *testing.T) {
	s := &fakeSettings{model: "openrouter/free", stored: "sk-or-v1-KEY"}
	p := &fakeProbe{}
	m := connectModel(s, p)

	cmd := m.runCommand("/connect")
	if cmd == nil {
		t.Fatal("no check was started")
	}
	if _, ok := cmd().(checked); !ok {
		t.Fatalf("command did not produce a check message: %T", cmd())
	}

	m.Update(checked([]Result{{Model: "openrouter/free"}}))
	if !m.verified {
		t.Error("a successful probe did not mark the connection verified")
	}
	if !strings.Contains(m.footer(80), "connected") {
		t.Errorf("footer does not report the connection:\n%s", m.footer(80))
	}
}

// A rejected key and a missing model are different problems. Reporting both as
// "failed" is what made setting this up confusing.
func TestConnectionFailuresAreDistinguished(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"rejected key", errors.New("the API key was rejected"), "check for a typo"},
		{"no such model", errors.New("no such model"), "openrouter/free"},
		{"unreachable", errors.New("could not reach the provider"), "check the network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSettings{model: "openrouter/free", stored: "sk-or-v1-KEY"}
			m := connectModel(s, &fakeProbe{results: []Result{{Model: "openrouter/free", Err: tc.err}}})
			m.runCommand("/connect")
			m.report([]Result{{Model: "openrouter/free", Err: tc.err}})
			if m.verified {
				t.Error("a failed probe reported as verified")
			}
			if !strings.Contains(joined(m), tc.want) {
				t.Errorf("transcript lacks the fix %q:\n%s", tc.want, joined(m))
			}
			if !strings.Contains(m.footer(80), "not connected") {
				t.Errorf("footer does not say it is not connected:\n%s", m.footer(80))
			}
		})
	}
}

// "Configured" and "working" are different claims, and the footer must not
// present the first as the second.
func TestFooterDoesNotClaimAConnectionItHasNotMade(t *testing.T) {
	s := &fakeSettings{model: "openrouter/free", stored: "sk-or-v1-KEY"}
	m := connectModel(s, &fakeProbe{})
	footer := m.footer(80)
	if strings.Contains(footer, "connected") {
		t.Fatalf("footer claims a connection before any check:\n%s", footer)
	}
	if !strings.Contains(footer, "unverified") {
		t.Fatalf("footer does not say the connection is unverified:\n%s", footer)
	}
}

// A configured machine must never be told it is offline.
//
// This exact line — "fixture · offline, no model needed · ready · read-only"
// — was read by a user who had a key and a model set up and reasonably expected
// them to be in force. The footer was right about the provider it was told
// about and the provider default was wrong, so the fix belongs here as much as
// in the resolver: whatever the provider is, the footer must name it honestly.
func TestFooterNamesTheProviderActuallyInForce(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model string
		want, absent          string
	}{
		{
			name: "hosted", provider: "openrouter", model: "z-ai/glm-5.3",
			want: "z-ai/glm-5.3", absent: "offline",
		},
		{
			// Honest, and the reason the default was wrong when it appeared
			// on a configured machine.
			name: "offline", provider: "fixture", model: "z-ai/glm-5.3",
			want: "offline, no model needed", absent: "z-ai/glm-5.3",
		},
		{
			name: "hosted with no model", provider: "openrouter", model: "",
			want: "no model configured", absent: "offline",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newCmdModel(&fakeSettings{})
			m.info.Provider = tc.provider
			m.info.Model = tc.model
			footer := m.footer(80)
			if !strings.Contains(footer, tc.want) {
				t.Errorf("footer does not say %q:\n%s", tc.want, footer)
			}
			if strings.Contains(footer, tc.absent) {
				t.Errorf("footer wrongly says %q:\n%s", tc.absent, footer)
			}
			if !strings.Contains(footer, tc.provider) {
				t.Errorf("footer does not name the provider %q:\n%s", tc.provider, footer)
			}
		})
	}
}

func TestStatusRunsACheckRatherThanOnlyReadingTheFile(t *testing.T) {
	s := &fakeSettings{model: "openrouter/free", stored: "sk-or-v1-KEY"}
	p := &fakeProbe{}
	m := connectModel(s, p)

	cmd := m.runCommand("/status")
	if cmd == nil {
		t.Fatal("/status did not check the connection")
	}
	// The returned command is what performs the check, so it has to be run
	// for the probe to be reached at all.
	msg := cmd()
	if _, ok := msg.(checked); !ok {
		t.Fatalf("command produced %T, want a check result", msg)
	}
	m.Update(msg)
	if !p.called {
		t.Error("the probe was never called")
	}
	if !strings.Contains(joined(m), "reachable") {
		t.Errorf("status did not report reachability:\n%s", joined(m))
	}
}

// /status has to be useful when nothing is configured, which is exactly when a
// new user runs it.
func TestStatusExplainsWhatIsMissing(t *testing.T) {
	m := connectModel(&fakeSettings{}, &fakeProbe{})
	m.runCommand("/status")
	if !strings.Contains(joined(m), "/connect") {
		t.Fatalf("status does not point at the fix:\n%s", joined(m))
	}
}

func TestStatusNeverPrintsARawCredential(t *testing.T) {
	const secret = "sk-or-v1-SUPERSECRET9999"
	m := connectModel(&fakeSettings{model: "openrouter/free", stored: secret}, &fakeProbe{})
	m.runCommand("/status")
	if strings.Contains(joined(m), "SUPERSECRET") {
		t.Fatalf("status leaked the credential:\n%s", joined(m))
	}
}

// The probe must never be able to reach the transcript through an error value.
func TestProbeErrorsNeverCarryACredential(t *testing.T) {
	m := connectModel(&fakeSettings{model: "m", stored: "sk-or-v1-KEY"}, &fakeProbe{})
	m.report([]Result{{Model: "m", Err: errors.New("boom")}})
	if strings.Contains(joined(m), "sk-or-v1") {
		t.Fatalf("transcript contains credential material:\n%s", joined(m))
	}
}

// Every command must be reachable from the menu, or the menu is a trap. The
// menu stays open on an exact match on purpose: enter then runs the same row
// either way, so there is one behaviour rather than two.
//
// Only the verbs are offered. Once a space is typed the operator has chosen a
// command and is filling in an argument, and showing a command list again
// would be misleading rather than helpful.
func TestEveryCommandIsReachableFromTheMenu(t *testing.T) {
	for _, want := range commandList {
		if strings.Contains(want, " ") {
			continue
		}
		m := newCmdModel(&fakeSettings{})
		typeText(m, want)
		m.syncMenu()
		found := false
		for _, row := range m.menu.rows {
			if row == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is not offered by the menu; it offers %v", want, m.menu.rows)
		}
	}
}

func TestConnectIsOfferedFirstBecauseItIsTheEntryPoint(t *testing.T) {
	if commandList[0] != "/connect" {
		t.Fatalf("first suggestion is %q, want /connect", commandList[0])
	}
}

// The runner must stay untouched by all of this: connection checks are not
// runs, and must not consume the generation counter.
func TestChecksDoNotStartRuns(t *testing.T) {
	m := connectModel(&fakeSettings{model: "openrouter/free", stored: "sk-or-v1-KEY"}, &fakeProbe{})
	m.runner = func(_ context.Context, _ string, _ func(core.Event)) (string, error) {
		t.Error("a connectivity check invoked the runner")
		return "", nil
	}
	m.runCommand("/status")
	m.report([]Result{{Model: "openrouter/free"}})
	if m.generation != 0 {
		t.Fatalf("generation = %d, want 0", m.generation)
	}
}
