package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A spinner tick must not touch the transcript.
//
// The tick used to append an empty line and then drop the last one to "keep the
// phase line current". addLine ignores empty strings, so the append did nothing
// and the drop removed a real line every 90 ms for as long as a run was active:
// roughly eleven lines a second, newest first, for the whole run.
func TestTickDoesNotEatTranscript(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.lines = []string{"a", "b", "c", "d", "e"}
	m.running = true

	m.update(tick{})
	m.update(tick{})
	m.update(tick{})

	if got := strings.Join(m.lines, ""); got != "abcde" {
		t.Fatalf("three ticks changed the transcript to %q", got)
	}
}

// A line is committed exactly once: recorded, queued, and then cleared from the
// queue when it is printed. Printing it twice would show an answer twice, and
// printing it never is how output was lost.
func TestALineIsCommittedOnce(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.greeted = true
	m.addLine("answer")

	if m.flush() == nil {
		t.Fatal("a new line was never printed")
	}
	if len(m.pending) != 0 {
		t.Fatalf("%d lines left queued after printing", len(m.pending))
	}
	if m.flush() != nil {
		t.Fatal("an empty queue was printed again")
	}
	if len(m.lines) != 1 || m.lines[0] != "answer" {
		t.Fatalf("record = %q, want one answer", m.lines)
	}
}

// Nothing is printed before the first size message.
//
// Printing first is what made committed output vanish: the renderer had no
// frame to insert above yet, so the lines were overwritten as soon as it drew.
// The splash is what waits for that moment, because it is sized to the width.
func TestNothingPrintsBeforeTheFirstSize(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.addLine("early output")
	if cmd := m.flush(); cmd != nil {
		t.Fatal("printed before the first size message")
	}
	m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.greeted {
		t.Fatal("the first size message did not greet")
	}
	// The held line is kept, not dropped, and the splash is queued with it.
	if !strings.Contains(strings.Join(m.lines, "\n"), "early output") {
		t.Fatal("output queued before the first size message was lost")
	}
	if got, want := len(m.lines), 1+len(splash(m.theme, m.width)); got != want {
		t.Fatalf("after the first size message the record has %d lines, want %d", got, want)
	}
}

// The committed row count is what places the frame under a click, so a wrapped
// line has to count as the rows it actually occupies.
func TestCommittedRowsCountWrapping(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.greeted = true
	m.width = 24
	m.addLine("1234567890") // fits
	if m.flush() == nil {
		t.Fatal("nothing printed")
	}
	if m.printedRows != 1 {
		t.Fatalf("a fitting line counted as %d rows, want 1", m.printedRows)
	}
	m.addLine(strings.Repeat("x", 60)) // wraps to three rows at width 24
	m.flush()
	if m.printedRows != 4 {
		t.Fatalf("printedRows = %d, want 4", m.printedRows)
	}
}

// A click is reported in screen coordinates while the frame floats under the
// committed output, so the handler has to offset by where the frame starts. A
// click one row too high belongs to the output, not to the menu.
func TestClickRunsTheCommandUnderTheCursor(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 24
	m.greeted = true
	m.printedRows = 3 // the splash, as if it had already been printed
	typeText(m, "/")

	view := m.View()
	if view.OnMouse == nil {
		t.Fatal("an open menu has no click handler")
	}
	rows := strings.Split(strings.TrimRight(view.Content, "\n"), "\n")
	index := -1
	for i, row := range rows {
		if strings.Contains(row, m.menu.rows[0]) {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("the first suggestion is not in the frame:\n%s", view.Content)
	}

	command := m.menu.rows[0]
	cmd := view.OnMouse(tea.MouseClickMsg{Button: tea.MouseLeft, Y: m.printedRows + index})
	if cmd == nil {
		t.Fatalf("a click on %q did nothing", command)
	}
	chosen, ok := cmd().(picked)
	if !ok || chosen.command != command {
		t.Fatalf("the click produced %#v, want %q", cmd(), command)
	}

	// One row higher is still inside the editor, so it must select nothing.
	if cmd := view.OnMouse(tea.MouseClickMsg{Button: tea.MouseLeft, Y: m.printedRows + index - 1}); cmd != nil {
		t.Fatal("a click above the first suggestion selected something")
	}
}

// A run that fails must print the remedy beneath the error, and name the
// command that applies.
func TestRunFailureRendersAdvice(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.Update(finished{
		generation: m.generation,
		err:        errWithAdvice{},
		advice:     "That model does not exist. Pick another with /models.",
	})
	text := strings.Join(m.lines, "\n")
	if !strings.Contains(text, "does not exist") {
		t.Errorf("the remedy is not shown:\n%s", text)
	}
	if !strings.Contains(text, "run /models") {
		t.Errorf("the remedy does not name the command:\n%s", text)
	}
}

// A failure with no known remedy must not invent one.
func TestRunFailureWithoutAdviceShowsOnlyTheError(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.Update(finished{generation: m.generation, err: plainErr{}, advice: ""})
	text := strings.Join(m.lines, "\n")
	if !strings.Contains(text, "something broke") {
		t.Errorf("the error is missing:\n%s", text)
	}
	if strings.Contains(text, "run /models") {
		t.Errorf("an advice-free failure invented a remedy:\n%s", text)
	}
}

type errWithAdvice struct{}

func (errWithAdvice) Error() string { return "no-such/model is not a valid model ID" }
func (errWithAdvice) Advice() string {
	return "That model does not exist. Pick another with /models."
}

type plainErr struct{}

func (plainErr) Error() string { return "something broke" }
