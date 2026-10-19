package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Turn is one exchange the interface can show: the prompt and the answer it
// drew.
type Turn struct {
	Question string
	Answer   string
}

// History serves the exchanges that came before the window on screen.
//
// The interface holds the newest part of a conversation and asks for what
// precedes it as the operator scrolls back. That is what lets a long
// conversation stay readable without every answer being held in memory at
// once, and it is why the transcript is drawn by the program rather than left
// to the terminal's own scrollback, which cannot be paged.
type History interface {
	// Older returns up to limit turns ending skip turns before the newest,
	// oldest first, and whether a further page exists.
	Older(skip, limit int) ([]Turn, bool, error)
}

// historyPage is the result of one paging request.
type historyPage struct {
	turns []Turn
	more  bool
	err   error
}

// pageSize is how many exchanges a request asks for. It is larger than a screen
// so that one request usually fills the window and the common case never shows
// a load.
const pageSize = 20

// wheelRows is how far one notch of the mouse wheel moves the transcript.
const wheelRows = 3

// The smallest terminal the interface draws into. Below this the frame would
// not fit and the borders would wrap, which reads as corruption.
const (
	minWidth  = 24
	minHeight = 6
)

// onMouse handles the wheel and clicks.
//
// viewRows is passed rather than read here because it is the geometry of the
// frame being drawn: the frame starts where the transcript ends, and a choice's
// row is that plus its offset inside the frame.
func (m *model) onMouse(viewRows int) func(tea.MouseMsg) tea.Cmd {
	return func(message tea.MouseMsg) tea.Cmd {
		switch item := message.(type) {
		case tea.MouseWheelMsg:
			switch item.Button {
			case tea.MouseWheelUp:
				return m.scrollBy(wheelRows)
			case tea.MouseWheelDown:
				return m.scrollBy(-wheelRows)
			}
			return nil

		case tea.MouseClickMsg:
			if item.Button != tea.MouseLeft {
				return nil
			}
			index := item.Y - (viewRows + m.chooserOffset())
			if index < 0 {
				return nil
			}
			if m.picking {
				// The drawn rows are a window onto the filtered list, so the
				// screen offset has to be added back before indexing.
				start := 0
				if m.pick >= maxModelRows {
					start = m.pick - maxModelRows + 1
				}
				target := start + index
				if target >= len(m.found) {
					return nil
				}
				return func() tea.Msg { return modelPicked{index: target} }
			}
			if len(m.menu.rows) > 0 && index < len(m.menu.rows) {
				command := m.menu.rows[index]
				return func() tea.Msg { return picked{command} }
			}
			return nil
		}
		return nil
	}
}

// layout is the geometry of one frame: the clamped size, the rows left for the
// transcript, and the rendered frame beneath it.
func (m *model) layout() (width, height, viewRows int, bottom string) {
	width = m.width
	if width < minWidth {
		width = minWidth
	}
	height = m.height
	if height < minHeight {
		height = minHeight
	}
	bottom, blockRows := m.bottomBlock(width, height)
	viewRows = height - blockRows
	if viewRows < 1 {
		viewRows = 1
	}
	return width, height, viewRows, bottom
}

// viewportRows is the number of transcript rows the window holds.
func (m *model) viewportRows() int {
	_, _, viewRows, _ := m.layout()
	return viewRows
}

// rows is the whole transcript wrapped to the terminal width, oldest first.
//
// It is rebuilt only when the width or the content changes: a frame is drawn on
// every keystroke, and re-wrapping a long conversation each time would make
// typing cost more the longer the session runs.
func (m *model) rows() []string {
	width := m.width
	if width < minWidth {
		width = minWidth
	}
	if m.renderedRevision == m.revision && m.renderedWidth == width {
		return m.rendered
	}
	lines := make([]string, 0, len(m.splash)+len(m.lines))
	lines = append(lines, m.splash...)
	lines = append(lines, m.lines...)
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, strings.Split(clip(line, width), "\n")...)
	}
	m.rendered, m.renderedWidth, m.renderedRevision = rows, width, m.revision
	return rows
}

// lineHeight is how many wrapped rows one line occupies at the current width.
func (m *model) lineHeight(line string) int {
	width := m.width
	if width < minWidth {
		width = minWidth
	}
	if line == "" {
		return 1
	}
	return strings.Count(clip(line, width), "\n") + 1
}

// scrollMax is the furthest the transcript can be scrolled back before its
// first line sits at the top of the window.
func (m *model) scrollMax() int {
	limit := len(m.rows()) - m.viewportRows()
	if limit < 0 {
		limit = 0
	}
	return limit
}

// clampScroll keeps the scroll position inside the transcript. A resize or a
// new answer both change how far back there is to go, so the bound is applied
// wherever the position is read rather than once where it is set.
func (m *model) clampScroll() {
	if limit := m.scrollMax(); m.scroll > limit {
		m.scroll = limit
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// scrollBy moves the transcript. Positive moves back in time, which is what an
// up arrow or a wheel notch away from the operator means.
func (m *model) scrollBy(delta int) tea.Cmd {
	m.scroll += delta
	m.clampScroll()
	return m.wantOlder()
}

// scrollTo moves the transcript to an exact position.
func (m *model) scrollTo(position int) tea.Cmd {
	m.scroll = position
	m.clampScroll()
	return m.wantOlder()
}

// prefetchRows is how much of the conversation beyond the window is kept ready.
//
// The operator must never catch up with the edge of what is loaded: reaching
// the top and waiting for a database read would make scrolling stutter exactly
// while it is being used. The reserve is about one screen, so a page has
// arrived before the window's top is reached, with two rows left so the frame
// still fits.
func (m *model) prefetchRows() int {
	rows := m.viewportRows() - 2
	if rows < 1 {
		rows = 1
	}
	return rows
}

// wantOlder requests another page when the window is close to the start of what
// is loaded. It returns nil when there is nothing to fetch, so callers can
// return it directly.
func (m *model) wantOlder() tea.Cmd {
	if m.info.History == nil || !m.more || m.paging {
		return nil
	}
	above := len(m.rows()) - m.scroll - m.viewportRows()
	if above < 0 {
		above = 0
	}
	if above > m.prefetchRows() {
		return nil
	}
	return m.loadOlder()
}

// loadOlder asks the store for the page just before the window.
//
// The skip is taken now and travels with the request. A turn that completes
// while the read is in flight lands at the end of the window, which moves the
// window's start by the same amount the store's end moved, so the page still
// joins it without a gap or an overlap.
func (m *model) loadOlder() tea.Cmd {
	if m.info.History == nil || !m.more || m.paging {
		return nil
	}
	m.paging = true
	skip := m.turns
	history := m.info.History
	return func() tea.Msg {
		turns, more, err := history.Older(skip, pageSize)
		return historyPage{turns: turns, more: more, err: err}
	}
}

// prependTurns puts older exchanges above the transcript.
//
// It goes under the splash rather than above it, so the banner stays the first
// thing in the conversation. The scroll position is left alone: it is the
// distance from the end of the transcript, and a page is added at the start, so
// every row keeps exactly the distance it had and the text on screen does not
// move.
func (m *model) prependTurns(turns []Turn) {
	if len(turns) == 0 {
		return
	}
	block := make([]string, 0, len(turns)*3)
	for _, turn := range turns {
		block = append(block, m.theme.user.Render(turn.Question))
		if turn.Answer != "" {
			block = append(block, m.theme.assistant.Render(turn.Answer))
		}
		block = append(block, "")
	}
	m.lines = append(block, m.lines...)
	m.revision++
	m.turns += len(turns)
}

// olderPage folds a completed request into the transcript.
func (m *model) olderPage(page historyPage) {
	m.paging = false
	if page.err != nil {
		// Stop asking. A store that cannot be read will not start being
		// readable on the next wheel notch, and retrying on every scroll would
		// turn one failure into a storm of them. The failure is recorded in the
		// transcript so it is not discovered only by noticing that scrolling
		// stops.
		m.more = false
		m.addLine(m.theme.failure.Render("could not load earlier turns: " + page.err.Error()))
		return
	}
	m.prependTurns(page.turns)
	m.more = page.more
}
