package ui

import (
	"fmt"
	"strings"
	"testing"
)

// fakeHistory serves canned exchanges the way the store does: a skip counts
// back from the newest turn, and a page comes out oldest first with more saying
// whether a further page exists.
type fakeHistory struct {
	turns []Turn
	skips []int
}

func (f *fakeHistory) Older(skip, limit int) ([]Turn, bool, error) {
	f.skips = append(f.skips, skip)
	end := len(f.turns) - skip
	if end < 0 {
		end = 0
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	page := append([]Turn(nil), f.turns[start:end]...)
	return page, start > 0, nil
}

func makeTurns(count int) []Turn {
	turns := make([]Turn, 0, count)
	for i := 0; i < count; i++ {
		turns = append(turns, Turn{
			Question: fmt.Sprintf("question %02d", i),
			Answer:   fmt.Sprintf("answer %02d", i),
		})
	}
	return turns
}

// A conversation shorter than the window sits at the bottom with the composer,
// rather than at the top with a gap under it: output grows downward from the
// prompt the way a page of text does.
func TestShortTranscriptSitsAtTheBottom(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 20
	m.greeted = true
	m.addLine("hello")

	rows := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
	if strings.TrimSpace(rows[0]) != "" {
		t.Fatalf("a short transcript did not leave the top of the window blank:\n%s", m.View().Content)
	}
	if !strings.Contains(strings.Join(rows, "\n"), "hello") {
		t.Fatalf("the transcript is not on screen:\n%s", m.View().Content)
	}
}

// Scrolling is bounded by the transcript: it cannot go back past the first line
// or forward past the last.
func TestScrollStopsAtBothEnds(t *testing.T) {
	m := tallTranscript(60)
	m.scrollBy(1000)
	if m.scroll != m.scrollMax() {
		t.Fatalf("scroll = %d, want the limit %d", m.scroll, m.scrollMax())
	}
	m.scrollBy(-1000)
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want 0", m.scroll)
	}
}

// Output arriving while the operator is reading back must not move the page
// under them. The transcript grows at its end, so the position moves with it.
func TestNewOutputDoesNotMoveAScrolledView(t *testing.T) {
	m := tallTranscript(60)
	m.scrollTo(5)
	before := m.View().Content

	m.addLine("a brand new line")

	if after := m.View().Content; after != before {
		t.Fatalf("new output moved a scrolled view:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A page of older exchanges is asked for by skipping the ones already on
// screen, and it is put in above them without the text on screen jumping.
func TestPagingPrependsOlderTurns(t *testing.T) {
	store := &fakeHistory{turns: makeTurns(30)}
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 20
	m.info.History = store
	m.more = true
	m.greeted = true

	// The first request is for the newest page.
	m.olderPage(m.loadOlder()().(historyPage))
	if m.turns != 20 || !m.more {
		t.Fatalf("after the first page: turns=%d more=%v, want 20 and true", m.turns, m.more)
	}

	// Scroll back so the view is not at the end. Reaching the top asks for the
	// next page itself, which is the command that comes back.
	cmd := m.scrollTo(m.scrollMax())
	if cmd == nil {
		t.Fatal("reaching the top did not ask for the next page")
	}
	before := m.View().Content
	m.olderPage(cmd().(historyPage))
	if m.turns != 30 {
		t.Fatalf("after the second page turns=%d, want 30", m.turns)
	}
	if m.more {
		t.Fatal("more reported past the start of the conversation")
	}
	if after := m.View().Content; after != before {
		t.Fatalf("prepending a page moved the text on screen:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if len(store.skips) != 2 || store.skips[0] != 0 || store.skips[1] != 20 {
		t.Fatalf("pages asked for at skips %v, want [0 20]", store.skips)
	}

	text := strings.Join(m.lines, "\n")
	if !strings.Contains(text, "question 00") || !strings.Contains(text, "question 29") {
		t.Fatalf("the conversation is not whole:\n%s", text)
	}
}

// The reserve is about a screen minus two rows, so a page has arrived before
// the top of the window is reached and scrolling never waits on the store.
func TestPrefetchKeepsAReserveBelowTheLoad(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 80, 24
	m.greeted = true

	if got, want := m.prefetchRows(), m.viewportRows()-2; got != want {
		t.Fatalf("reserve = %d, want %d", got, want)
	}
	if m.prefetchRows() < 1 {
		t.Fatal("the reserve is empty on a short terminal")
	}
}

// Reaching the top of what is loaded asks for the next page; scrolling at the
// end of the conversation does not.
func TestReachingTheTopRequestsAnotherPage(t *testing.T) {
	store := &fakeHistory{turns: makeTurns(60)}
	m := tallTranscript(60)
	m.info.History = store
	m.more = true

	m.scrollTo(0)
	if len(store.skips) != 0 {
		t.Fatalf("scrolling at the end asked for a page: skips %v", store.skips)
	}
	cmd := m.scrollTo(m.scrollMax())
	if cmd == nil {
		t.Fatal("reaching the top did not ask for a page")
	}
	cmd()
	if len(store.skips) != 1 {
		t.Fatalf("reaching the top asked for %d pages, want 1", len(store.skips))
	}
}

// A store that cannot be read is reported once and then left alone, so one
// failure does not become a request on every wheel notch.
func TestAFailedPageIsReportedOnce(t *testing.T) {
	m := tallTranscript(60)
	m.info.History = failingHistory{}
	m.more = true

	cmd := m.scrollTo(m.scrollMax())
	if cmd == nil {
		t.Fatal("reaching the top did not ask for a page")
	}
	m.olderPage(cmd().(historyPage))
	if m.more {
		t.Fatal("a failed page left paging open")
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "could not load earlier turns") {
		t.Fatalf("the failure is not in the transcript:\n%s", strings.Join(m.lines, "\n"))
	}
	if cmd := m.wantOlder(); cmd != nil {
		t.Fatal("a failed store was asked again")
	}
}

type failingHistory struct{}

func (failingHistory) Older(int, int) ([]Turn, bool, error) {
	return nil, false, fmt.Errorf("no such conversation")
}
