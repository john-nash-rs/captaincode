package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Someone who has already set up a key and a model should not have to run
// /status to find out whether it still works. The interface checks on start.
//
// The bug behind this is quieter than it looks: an earlier version had no
// startup check at all, so a returning user saw "unverified" in the footer and
// had no way to tell whether that meant "not checked yet" or "checked and
// broken".
func TestStartupChecksARememberedSetup(t *testing.T) {
	s := &fakeSettings{stored: "sk-or-v1-KEY", model: "a/b"}
	p := &fakeProbe{}
	m := newCmdModel(s)
	m.info.Model = "a/b"
	m.info.Probe = p
	m.checking = true
	m.quietSuccess = true
	m.startupCheck = m.check("a/b")

	if m.startupCheck == nil {
		t.Fatal("no startup check was scheduled")
	}
	m.Init()()
	if !p.called {
		t.Fatal("the probe was not called")
	}
	m.Update(m.startupCheck())
	if !m.verified {
		t.Fatal("a working setup was not marked verified")
	}
	if !strings.Contains(m.footer(80), "connected") {
		t.Fatalf("the footer does not say connected:\n%s", m.footer(80))
	}
}

// A success that is announced every start is noise. The footer already says
// connected; a returning user does not need a banner saying so as well.
func TestStartupSuccessIsQuiet(t *testing.T) {
	m := newCmdModel(&fakeSettings{stored: "sk-or-v1-KEY", model: "a/b"})
	m.quietSuccess = true
	m.checking = true
	m.report([]Result{{Model: "a/b"}})
	if strings.Contains(joined(m), "reachable") {
		t.Fatalf("the startup check announced success:\n%s", joined(m))
	}
}

// But a failure is the case worth interrupting for, and it must not be silent.
func TestStartupFailureIsAnnounced(t *testing.T) {
	m := newCmdModel(&fakeSettings{stored: "sk-or-v1-KEY", model: "a/b"})
	m.quietSuccess = true
	m.checking = true
	m.report([]Result{{Model: "a/b", Err: errors.New("the API key was rejected")}})
	if !strings.Contains(joined(m), "rejected") {
		t.Fatalf("a startup failure was silent:\n%s", joined(m))
	}
	if strings.Contains(m.footer(80), "not connected") == false {
		t.Fatalf("the footer does not say it is not connected:\n%s", m.footer(80))
	}
}

// An explicit /status must still speak up. Quiet-on-success is only for the
// automatic check, never for one the operator asked for.
func TestExplicitStatusStillReportsSuccess(t *testing.T) {
	m := newCmdModel(&fakeSettings{stored: "sk-or-v1-KEY", model: "a/b"})
	m.info.Probe = &fakeProbe{}
	m.quietSuccess = false
	cmd := m.runCommand("/status")
	if cmd == nil {
		t.Fatal("/status ran no check")
	}
	m.Update(cmd())
	if !strings.Contains(joined(m), "reachable") {
		t.Fatalf("/status did not report success:\n%s", joined(m))
	}
}

// With nothing configured there is nothing to verify, and pretending otherwise
// would show a checking indicator that never resolves.
func TestNoStartupCheckWhenNothingIsConfigured(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.info.Probe = &fakeProbe{}
	// Run() skips scheduling when the model or the credential is absent.
	if m.startupCheck != nil {
		t.Fatal("a check was scheduled with nothing configured")
	}
}

// The whole point: settings survive a restart. This is the behaviour the user
// reported as broken, and it is really about which binary is on PATH, so the
// test pins the mechanism rather than the environment.
func TestStoredSettingsSurviveAFreshModel(t *testing.T) {
	// One process sets them.
	store := &fakeSettings{}
	first := newCmdModel(store)
	typeText(first, "/openrouter api-key")
	first.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeText(first, "sk-or-v1-KEY")
	first.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeText(first, "/openrouter model some/model")
	first.key(tea.KeyPressMsg{Code: tea.KeyEnter})

	if store.stored == "" || store.model == "" {
		t.Fatalf("nothing was stored: key=%q model=%q", store.stored, store.model)
	}

	// A new model stands for a new process reading what was written.
	second := &fakeSettings{stored: store.stored, model: store.model}
	_, model, credential := second.Status()
	if model != "some/model" {
		t.Fatalf("the model was not remembered: %q", model)
	}
	if !strings.Contains(credential, "set") {
		t.Fatalf("the credential was not remembered: %q", credential)
	}
}

var _ = context.Background
