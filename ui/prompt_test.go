package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The caret has to advance as the operator types, or the editor looks frozen.
func TestCursorAdvancesWithInput(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 24
	m.lines = []string{banner(theme{}, 80)}

	start := m.View().Cursor.X
	typeText(m, "hello")
	after := m.View().Cursor.X
	if after != start+len("hello") {
		t.Errorf("cursor X = %d, want %d after typing %q", after, start+len("hello"), "hello")
	}
	// Backspacing must move it back.
	for i := 0; i < 5; i++ {
		m.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	if got := m.View().Cursor.X; got != start {
		t.Errorf("cursor X = %d after erasing, want %d", got, start)
	}
}

// The caret must never escape the border, however long the input gets.
func TestCursorStaysInsideTheBox(t *testing.T) {
	for _, w := range []int{24, 40, 80} {
		m := newCmdModel(&fakeSettings{})
		m.width, m.height = w, 20
		m.lines = []string{banner(theme{}, w)}
		typeText(m, strings.Repeat("x", 500))
		x := m.View().Cursor.X
		if x < promptWidth || x > w {
			t.Errorf("w=%d cursor X = %d, outside [%d,%d]", w, x, promptWidth, w)
		}
	}
}

// While a credential is typed the caret sits after the fixed mask.
func TestCursorDuringCredentialEntry(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 24
	m.secret = true
	m.input = []rune("sk-or-v1-secret")
	out := m.View().Content
	if strings.Contains(out, "secret") {
		t.Fatal("credential text rendered")
	}
	if got, want := m.View().Cursor.X, promptWidth+maskWidth; got != want {
		t.Errorf("cursor X = %d, want %d", got, want)
	}
}

func TestSuggestionsAppearForCommandInput(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 24
	m.lines = []string{banner(theme{}, 80)}

	if len(m.suggestions()) != 0 {
		t.Error("suggestions shown before typing")
	}
	typeText(m, "/openrouter")
	rows := m.suggestions()
	if len(rows) == 0 {
		t.Fatal("no suggestions for a complete command")
	}
	// The frame holds the composer, so it must fit the terminal with the list
	// shown but never grow past it.
	frame := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
	if len(frame) > m.height {
		t.Errorf("frame has %d rows, want at most %d with suggestions", len(frame), m.height)
	}
	for i, r := range frame {
		if lipgloss.Width(r) > m.width {
			t.Errorf("row %d overflows: %d", i, lipgloss.Width(r))
		}
	}
}

func TestNoSuggestionsForOrdinaryPrompts(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 24
	typeText(m, "what is the project version")
	if rows := m.suggestions(); len(rows) != 0 {
		t.Errorf("ordinary prompt showed %d suggestions", len(rows))
	}
	frame := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
	if len(frame) > m.height {
		t.Errorf("frame has %d rows, want at most %d", len(frame), m.height)
	}
}

// Tab finishes to the best match, which is the first row of the suggestion
// list the operator is already looking at.
func TestTabCompletesBestMatch(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "/openrout")
	m.key(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := string(m.input); got != "/openrouter" {
		t.Errorf("Tab produced %q, want /openrouter", got)
	}
}

func TestTabLeavesUnknownInputAlone(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	for _, in := range []string{"/zzz", "hello"} {
		m.input = []rune(in)
		m.key(tea.KeyPressMsg{Code: tea.KeyTab})
		if got := string(m.input); got != in {
			t.Errorf("Tab changed %q to %q", in, got)
		}
	}
	// A bare slash takes the first suggestion, which is what the list shows.
	// Asserted against the list rather than a literal, because the first entry
	// changes as commands are added and this is about the rule, not the order.
	m.input = []rune("/")
	m.key(tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := string(m.input), commandList[0]; got != want {
		t.Errorf("Tab on %q produced %q, want %q", "/", got, want)
	}
}

func TestCompletionIsPure(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/openrouter", "/openrouter"},
		{"/openrouter api", "/openrouter api-key"},
		{"/openrouter m", "/openrouter model"},
		{"/openrouter p", "/openrouter path"},
		{"/h", "/help"},
		{"/zzz", "/zzz"},
		{"hello", "hello"},
		{"", ""},
	} {
		if got := completion(c.in); got != c.want {
			t.Errorf("completion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The cursor row must land on the prompt even with a status line and
// suggestions present, which is the layout that previously drifted.
func TestCursorRowTracksLayout(t *testing.T) {
	for _, running := range []bool{false, true} {
		m := newCmdModel(&fakeSettings{})
		m.width, m.height = 80, 30
		m.running = running
		typeText(m, "/openr")
		v := m.View()
		rows := strings.Split(strings.TrimRight(v.Content, "\n"), "\n")
		if len(rows) > 30 {
			t.Fatalf("running=%v frame has %d rows", running, len(rows))
		}
		if v.Cursor.Y < 0 || v.Cursor.Y >= len(rows) {
			t.Fatalf("running=%v cursor row %d outside a %d row frame", running, v.Cursor.Y, len(rows))
		}
		row := rows[v.Cursor.Y]
		if !strings.Contains(row, "›") {
			t.Errorf("running=%v cursor row %d is not the prompt: %q", running, v.Cursor.Y, row)
		}
		if v.Cursor.X < 1 {
			t.Errorf("running=%v cursor X = %d", running, v.Cursor.X)
		}
	}
}

// A short terminal must drop suggestions rather than overflow or hide the box.
func TestSuggestionsDropOnShortTerminal(t *testing.T) {
	for _, h := range []int{6, 7, 8} {
		m := newCmdModel(&fakeSettings{})
		m.width, m.height = 60, h
		m.lines = []string{banner(theme{}, 60)}
		typeText(m, "/openr")
		rows := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
		if len(rows) > h {
			t.Errorf("h=%d frame has %d rows", h, len(rows))
		}
		v := m.View()
		if v.Cursor.Y < 0 || v.Cursor.Y >= h {
			t.Errorf("h=%d cursor row %d out of frame", h, v.Cursor.Y)
		}
	}
}
