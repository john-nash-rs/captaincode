package ui

import (
	"strings"
	"testing"
)

// A short answer rendered while a code request left the screen blank. Two
// causes, both here: a line taller than the transcript window returned an empty
// window, and the frame was never trimmed to the height the prompt needed.
const longReply = "Here's a calculator:\n```python\n" +
	"def add(a, b):\n    return a + b\n\n" +
	"def sub(a, b):\n    return a - b\n\n" +
	"def mul(a, b):\n    return a * b\n\n" +
	"def div(a, b):\n    return a / b\n\n" +
	"while True:\n    print('1 add 2 sub 3 mul 4 div 5 quit')\n" +
	"    choice = input('> ')\n    a = float(input('first: '))\n" +
	"    b = float(input('second: '))\n" +
	"    if choice == '1': print(add(a, b))\n" +
	"    elif choice == '5': break\n```"

func longReplyModel(height int) *model {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, height
	m.addLine("give me python code for calculator")
	m.addLine(longReply)
	return m
}

// The report: a code request produced an empty screen. The answer is longer
// than the window, so it cannot all be on screen at once; what must hold is
// that the record keeps it whole, and that the window always shows the newest
// of it with the prompt under it, at every terminal height.
func TestLongReplyIsNotBlank(t *testing.T) {
	for _, height := range []int{10, 16, 24, 40, 80} {
		m := longReplyModel(height)
		if !strings.Contains(strings.Join(m.lines, "\n"), "def add") {
			t.Errorf("height %d lost the start of the answer", height)
		}
		frame := m.View().Content
		if strings.TrimSpace(frame) == "" {
			t.Fatalf("height %d rendered nothing", height)
		}
		if !strings.Contains(frame, "›") {
			t.Errorf("height %d lost the prompt:\n%s", height, frame)
		}
	}
}

// The frame holds the transcript and the composer, and must never be taller
// than the terminal: the renderer discards the top rows of an oversized frame,
// which would take the editor with it.
func TestFrameNeverOverflowsTheTerminal(t *testing.T) {
	for _, height := range []int{8, 12, 24, 40, 80} {
		m := longReplyModel(height)
		frame := m.View().Content
		if got := strings.Count(strings.TrimRight(frame, "\n"), "\n") + 1; got > height {
			t.Errorf("height %d produced a frame of %d rows", height, got)
		}
		if !strings.Contains(frame, "›") {
			t.Errorf("height %d lost the prompt:\n%s", height, frame)
		}
	}
}

// A single line taller than the terminal must still be recorded, and must not
// push the frame past the terminal. This is the case that blanked the screen:
// the window search found nothing that fit and returned an empty slice.
func TestASingleHugeLineStillRenders(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 12
	m.addLine(strings.Repeat("line of code\n", 60))
	if strings.TrimSpace(strings.Join(m.lines, "\n")) == "" {
		t.Fatal("one very tall line recorded nothing")
	}
	frame := m.View().Content
	if strings.TrimSpace(frame) == "" {
		t.Fatal("one very tall line rendered nothing")
	}
	if got := strings.Count(strings.TrimRight(frame, "\n"), "\n") + 1; got > 12 {
		t.Fatalf("frame is %d rows for a 12 row terminal", got)
	}
}

// A tiny terminal must still produce a usable frame rather than an empty one.
func TestTinyTerminalStillRenders(t *testing.T) {
	m := longReplyModel(6)
	if strings.TrimSpace(m.View().Content) == "" {
		t.Fatal("a six row terminal rendered nothing")
	}
}
