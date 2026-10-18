package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// scrollModel builds a model with a transcript long enough to scroll.
func scrollModel(answers int) *model {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 20
	for i := range answers {
		m.addLine("answer " + strings.Repeat("x", 40) + " " + string(rune('A'+i)))
	}
	return m
}

// The transcript was a fixed window with no way back, so an answer that had
// scrolled past was unreachable. That is not the same as not keeping it.
func TestTranscriptScrollsBack(t *testing.T) {
	m := scrollModel(40)
	bottom := m.View().Content
	if strings.Contains(bottom, "answer x") != strings.Contains(bottom, "answer x") {
		t.Fatal("sanity")
	}
	if !strings.Contains(bottom, "answer x") {
		t.Fatalf("the live view does not show the newest line:\n%s", bottom)
	}

	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.scroll != 3 {
		t.Fatalf("scroll = %d, want 3", m.scroll)
	}
	if m.View().Content == bottom {
		t.Error("scrolling did not change the view")
	}
}

// Scrolling must not run past the start of the record.
func TestScrollStopsAtTheStart(t *testing.T) {
	m := scrollModel(10)
	for range 100 {
		m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if m.scroll > len(m.lines) {
		t.Fatalf("scroll = %d, past the %d retained lines", m.scroll, len(m.lines))
	}
	if m.scroll != len(m.lines) {
		t.Errorf("scroll = %d, want the whole transcript", m.scroll)
	}
}

// And it must come back.
func TestScrollReturnsToTheLiveView(t *testing.T) {
	m := scrollModel(20)
	for range 5 {
		m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	for range 10 {
		m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want 0", m.scroll)
	}
}

// New output is when the newest thing matters, so the view returns to the end
// on its own. Scrolling back to re-read something is not undone by that.
func TestNewOutputSnapsBackToTheLiveEnd(t *testing.T) {
	m := scrollModel(30)
	for range 5 {
		m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if m.scroll == 0 {
		t.Fatal("precondition: not scrolled")
	}
	m.Update(finished{generation: m.generation, text: "fresh answer", advice: ""})
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want the live view after new output", m.scroll)
	}
	if !strings.Contains(m.View().Content, "fresh answer") {
		t.Error("the new answer is not on screen")
	}
}

// The window must stay the size it is. Overflowing the terminal would push the
// prompt off the bottom, which is the one thing the layout exists to prevent.
func TestScrolledViewStillFitsTheTerminal(t *testing.T) {
	m := scrollModel(40)
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyUp}, {Code: tea.KeyUp}, {Code: tea.KeyPgUp}, {Code: tea.KeyUp},
	} {
		m.key(key)
	}
	frame := m.View().Content
	if got := strings.Count(frame, "\n") + 1; got > m.height {
		t.Fatalf("the frame is %d rows for a %d row terminal", got, m.height)
	}
	if !strings.Contains(frame, "›") {
		t.Error("the prompt was pushed off screen while scrolled")
	}
}

// Page keys move by a screenful rather than a line.
func TestPageKeysScrollFurther(t *testing.T) {
	m := scrollModel(60)
	m.key(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.scroll != 10 {
		t.Fatalf("pgup scrolled %d lines, want 10", m.scroll)
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.scroll != 0 {
		t.Fatalf("pgdown left scroll at %d, want 0", m.scroll)
	}
}

func TestHomeAndEndJumpToTheExtremes(t *testing.T) {
	m := scrollModel(30)
	m.key(tea.KeyPressMsg{Code: tea.KeyHome})
	if m.scroll != len(m.lines) {
		t.Fatalf("home gave scroll = %d, want %d", m.scroll, len(m.lines))
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.scroll != 0 {
		t.Fatalf("end gave scroll = %d, want 0", m.scroll)
	}
}

// While scrolled back, the status line says so. A spinner describing a run
// that has scrolled out of view is worse than no spinner.
func TestScrolledStatusLineSaysSo(t *testing.T) {
	m := scrollModel(30)
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	line := m.statusLine()
	if !strings.Contains(line, "scrolled back") {
		t.Fatalf("status line = %q, want it to report the scroll", line)
	}
	if !strings.Contains(line, "end returns") {
		t.Fatalf("status line = %q, want it to say how to come back", line)
	}
}

// The arrows belong to the picker while it is open. Scrolling instead would
// make a model impossible to choose.
func TestArrowsDoNotScrollWhileThePickerIsOpen(t *testing.T) {
	m := scrollModel(20)
	m.picking = true
	m.found = catalogue()
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want the picker to keep the arrows", m.scroll)
	}
}

// And to the command menu.
func TestArrowsDoNotScrollWhileTheMenuIsOpen(t *testing.T) {
	m := scrollModel(20)
	m.input = []rune("/")
	m.syncMenu()
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want the menu to keep the arrows", m.scroll)
	}
}

// While a credential is being typed, the arrows must not scroll either: the
// field is the only thing that responds to keys in that mode.
func TestArrowsDoNotScrollDuringCredentialEntry(t *testing.T) {
	m := scrollModel(20)
	m.secret = true
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want credential entry to keep the arrows", m.scroll)
	}
}

// A run that fails must print the advice beneath the error, and name the
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
