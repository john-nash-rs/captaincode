package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func visibleText(frame string) string {
	var b strings.Builder
	for _, line := range strings.Split(frame, "\n") {
		if strings.TrimSpace(line) != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// The report: a code request produced an empty screen.
func TestLongReplyIsNotBlank(t *testing.T) {
	for _, height := range []int{10, 16, 24, 40, 80} {
		m := longReplyModel(height)
		text := visibleText(m.View().Content)
		if strings.TrimSpace(text) == "" {
			t.Fatalf("height %d rendered a blank transcript", height)
		}
		// Which words survive depends on the height, because trimming keeps
		// the newest rows. Asserting on content only makes sense once the
		// whole answer fits, so that is the only case checked here.
		if height >= 40 {
			if !strings.Contains(text, "calculator") {
				t.Errorf("height %d dropped the question:\n%s", height, text)
			}
			if !strings.Contains(text, "def add") {
				t.Errorf("height %d dropped the start of the answer:\n%s", height, text)
			}
		}
	}
}

// A reply taller than the window must not push the prompt off the bottom. The
// frame is exactly the terminal height, with the editor on the last rows.
func TestFrameNeverOverflowsTheTerminal(t *testing.T) {
	for _, height := range []int{8, 12, 24, 40, 80} {
		m := longReplyModel(height)
		frame := m.View().Content
		if got := strings.Count(frame, "\n") + 1; got > height {
			t.Errorf("height %d produced a frame of %d rows", height, got)
		}
		if !strings.Contains(frame, "ask about this project") && !strings.Contains(frame, "›") {
			t.Errorf("height %d lost the prompt:\n%s", height, frame)
		}
	}
}

// The newest content is what is being read, so trimming keeps the end.
func TestTrimmingKeepsTheNewestRows(t *testing.T) {
	trimmed := clipRows("a\nb\nc\nd\ne", 80, 2)
	if trimmed != "d\ne" {
		t.Fatalf("clipRows kept %q, want the last two rows", trimmed)
	}
	// A budget of zero yields nothing rather than panicking on a slice.
	if got := clipRows("a\nb", 80, 0); got != "" {
		t.Fatalf("clipRows with no budget returned %q", got)
	}
	// Text that already fits is untouched.
	if got := clipRows("a\nb", 80, 5); got != "a\nb" {
		t.Fatalf("clipRows altered text that fits: %q", got)
	}
}

// A single line taller than the window must still produce something. This is
// the case that blanked the screen: the window search found nothing that fit
// and returned an empty slice.
func TestASingleHugeLineStillRenders(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 12
	m.addLine(strings.Repeat("line of code\n", 60))
	if strings.TrimSpace(visibleText(m.View().Content)) == "" {
		t.Fatal("one very tall line rendered blank")
	}
	if got := strings.Count(m.View().Content, "\n") + 1; got > 12 {
		t.Fatalf("frame is %d rows for a 12 row terminal", got)
	}
}

// Scrolling back through a long reply must show the older part of it, not
// repeat the newest rows.
func TestScrollingRevealsTheStartOfALongReply(t *testing.T) {
	m := longReplyModel(24)
	bottom := visibleText(m.View().Content)
	if strings.Contains(bottom, "def add") {
		t.Skip("the whole answer already fits; nothing to scroll")
	}
	for range 12 {
		m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	top := visibleText(m.View().Content)
	if top == bottom {
		t.Fatal("scrolling did not change the transcript")
	}
}

// A tiny terminal must still produce a usable frame rather than an empty one.
func TestTinyTerminalStillRenders(t *testing.T) {
	m := longReplyModel(6)
	frame := m.View().Content
	if strings.TrimSpace(frame) == "" {
		t.Fatal("a six row terminal rendered nothing")
	}
}
