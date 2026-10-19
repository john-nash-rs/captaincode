package ui

import (
	"fmt"
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

// A line is recorded once and drawn once.
//
// There is no queue between the record and the screen any more: the window
// reads from the record, so a line cannot be printed twice or dropped between
// being produced and being shown.
func TestALineIsRecordedAndDrawnOnce(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.greeted = true
	m.addLine("answer")

	if len(m.lines) != 1 || m.lines[0] != "answer" {
		t.Fatalf("record = %q, want one answer", m.lines)
	}
	if got := strings.Count(m.View().Content, "answer"); got != 1 {
		t.Fatalf("the answer is drawn %d times, want 1:\n%s", got, m.View().Content)
	}
}

// Output produced before the first size message is kept.
//
// The splash is what waits for the width, because it is sized to the terminal.
// Nothing else does: a line recorded before the first frame is drawn from the
// same record as everything else, so there is no window in which it can be
// overwritten and lost.
func TestOutputBeforeTheFirstSizeIsKept(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.addLine("early output")
	m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.greeted {
		t.Fatal("the first size message did not greet")
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "early output") {
		t.Fatal("output recorded before the first size message was lost")
	}
	if len(m.splash) == 0 {
		t.Fatal("the splash was never laid out")
	}
	if !strings.Contains(m.View().Content, "early output") {
		t.Fatalf("the kept output is not on screen:\n%s", m.View().Content)
	}
}

// A click is reported in screen coordinates and the frame is drawn directly
// under the transcript, so a click's row is the screen row. The frame starts
// where the transcript ends, and a choice starts at its own offset inside it.
func TestClickRunsTheCommandUnderTheCursor(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 24
	m.greeted = true
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
	cmd := view.OnMouse(tea.MouseClickMsg{Button: tea.MouseLeft, Y: index})
	if cmd == nil {
		t.Fatalf("a click on %q did nothing", command)
	}
	chosen, ok := cmd().(picked)
	if !ok || chosen.command != command {
		t.Fatalf("the click produced %#v, want %q", cmd(), command)
	}

	// One row higher is the editor, so it must select nothing.
	if cmd := view.OnMouse(tea.MouseClickMsg{Button: tea.MouseLeft, Y: index - 1}); cmd != nil {
		t.Fatal("a click above the first suggestion selected something")
	}
}

// A wheel notch moves the transcript, and moving it back returns to the end.
func TestWheelScrollsTheTranscript(t *testing.T) {
	m := tallTranscript(60)
	view := m.View()

	view.OnMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.scroll != wheelRows {
		t.Fatalf("scroll after one notch up = %d, want %d", m.scroll, wheelRows)
	}
	view.OnMouse(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.scroll != 0 {
		t.Fatalf("scroll after one notch down = %d, want 0", m.scroll)
	}
}

// The arrows move the transcript when no list is open, because the transcript
// is the only thing on screen that scrolls.
func TestArrowKeysScrollTheTranscript(t *testing.T) {
	m := tallTranscript(60)
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.scroll != 1 {
		t.Fatalf("up left the transcript at %d, want 1", m.scroll)
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.scroll != 0 {
		t.Fatalf("down left the transcript at %d, want 0", m.scroll)
	}
}

// A run that fails must show the remedy beneath the error, and name the command
// that applies.
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

// tallTranscript is a model with more lines than fit a 20-row terminal, so it
// has somewhere to scroll to.
func tallTranscript(lines int) *model {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 20
	m.greeted = true
	for i := 0; i < lines; i++ {
		m.addLine(fmt.Sprintf("line %02d", i))
	}
	return m
}

type errWithAdvice struct{}

func (errWithAdvice) Error() string { return "no-such/model is not a valid model ID" }
func (errWithAdvice) Advice() string {
	return "That model does not exist. Pick another with /models."
}

type plainErr struct{}

func (plainErr) Error() string { return "something broke" }
