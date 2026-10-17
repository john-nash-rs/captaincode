package ui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"example.com/agentbook/core"
	"testing"
)

func TestTypingQueuesWhileRunIsActive(t *testing.T) {
	m := &model{running: true, input: []rune("second question")}
	_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command != nil || len(m.queue) != 1 || len(m.input) != 0 {
		t.Fatal("new request was not queued")
	}
}
func TestOldProgressCannotReplaceCurrentRun(t *testing.T) {
	m := &model{generation: 2}
	m.Update(progress{1, core.Event{Kind: "old"}})
	if len(m.lines) != 0 {
		t.Fatal("stale event accepted")
	}
}
func TestEscapeCancelsUnderlyingContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &model{running: true, cancel: cancel}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if ctx.Err() == nil {
		t.Fatal("UI cancelled only rendering")
	}
}
