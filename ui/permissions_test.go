package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The scenario the user hit: they asked the model to save a file and it
// refused, correctly, because writes were off. There was then no route to
// change that from inside the interface.
func TestPermissionsShowsAndChangesGrants(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)

	m.runCommand("/permissions")
	text := joined(m)
	if !strings.Contains(text, "writes    off") {
		t.Fatalf("permissions are not shown:\n%s", text)
	}
	if !strings.Contains(text, "/permissions writes on") {
		t.Fatalf("no way to change them is offered:\n%s", text)
	}

	m.runCommand("/permissions writes on")
	if !s.writes {
		t.Fatal("writes were not granted")
	}
	if !strings.Contains(joined(m), "writes on") {
		t.Fatalf("the change was not confirmed:\n%s", joined(m))
	}
	// It has to say it applies now, because the previous behaviour required a
	// restart and that is the thing being fixed.
	if !strings.Contains(joined(m), "no restart") {
		t.Fatalf("the restart requirement is not stated:\n%s", joined(m))
	}
}

// Turning a grant on must reach the footer immediately, or the interface still
// reads read-only while the model can write.
func TestFooterReflectsAGrantImmediately(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	if !strings.Contains(m.footer(80), "read-only") {
		t.Fatal("precondition: footer should start read-only")
	}
	m.runCommand("/permissions writes on")
	if strings.Contains(m.footer(80), "read-only") {
		t.Fatalf("the footer still says read-only:\n%s", m.footer(80))
	}
	if !strings.Contains(m.footer(80), "writes") {
		t.Fatalf("the footer does not show the grant:\n%s", m.footer(80))
	}
}

func TestPermissionsTogglesBothGrants(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	m.runCommand("/permissions all on")
	if !s.writes || !s.commands {
		t.Fatalf("all on gave writes=%v commands=%v", s.writes, s.commands)
	}
	m.runCommand("/permissions all off")
	if s.writes || s.commands {
		t.Fatalf("all off gave writes=%v commands=%v", s.writes, s.commands)
	}
}

// Nonsense must be rejected rather than guessed at.
func TestPermissionsRejectsNonsense(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	for _, bad := range []string{"/permissions writes maybe", "/permissions wrenches on", "/permissions"} {
		m.lines = nil
		m.runCommand(bad)
		if bad == "/permissions" {
			continue // the bare form is a report, not a change
		}
		if !strings.Contains(joined(m), "usage:") {
			t.Errorf("%q was not rejected: %s", bad, joined(m))
		}
	}
	if s.writes || s.commands {
		t.Fatal("a rejected command still changed a grant")
	}
}

// The command must be reachable from the menu, or it does not exist.
func TestPermissionsIsReachableFromTheMenu(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "/perm")
	m.syncMenu()
	found := false
	for _, row := range m.menu.rows {
		if strings.HasPrefix(row, "/permissions") {
			found = true
		}
	}
	if !found {
		t.Fatalf("/permissions is not offered; the menu shows %v", m.menu.rows)
	}
	if _, cmd := func() (tea.Model, tea.Cmd) {
		typeText(m, "/permissions")
		return m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	}(); cmd != nil {
		// Reporting the permissions returns no command, which is fine, but it
		// must not panic or leave the picker open.
		if m.picking {
			t.Fatal("the picker opened for /permissions")
		}
	}
}
