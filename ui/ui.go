package ui

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"example.com/agentbook/core"
)

type Runner func(context.Context, string, func(core.Event)) (string, error)
type progress struct {
	generation int
	event      core.Event
}
type finished struct {
	generation int
	text       string
	err        error
}
type model struct {
	runner            Runner
	send              func(tea.Msg)
	input             []rune
	queue             []string
	lines             []string
	running, quitting bool
	cancel            context.CancelFunc
	generation        int
}

func Run(runner Runner) error {
	m := &model{runner: runner, lines: []string{"Agentbook — Enter submits; Esc cancels; Ctrl+C exits."}}
	program := tea.NewProgram(m)
	m.send = program.Send
	_, err := program.Run()
	return err
}
func (m *model) Init() tea.Cmd { return nil }
func (m *model) start() tea.Cmd {
	if m.running || len(m.queue) == 0 {
		return nil
	}
	question := m.queue[0]
	m.queue = m.queue[1:]
	m.running = true
	m.generation++
	generation := m.generation
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.addLine("You: " + safe(question))
	return func() tea.Msg {
		text, err := m.runner(ctx, question, func(event core.Event) { m.send(progress{generation, event}) })
		cancel()
		return finished{generation, text, err}
	}
}
func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch item := message.(type) {
	case tea.KeyPressMsg:
		switch item.String() {
		case "ctrl+c":
			if m.running {
				m.quitting = true
				m.queue = nil
				m.cancel()
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.cancel != nil {
				m.cancel()
			}
		case "enter":
			if len(m.input) > 0 && len(m.queue) < 10 && !m.quitting {
				m.queue = append(m.queue, string(m.input))
				m.input = nil
				return m, m.start()
			}
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		default:
			if len(m.input) < 2000 {
				for _, character := range item.Key().Text {
					if !unicode.IsControl(character) && len(m.input) < 2000 {
						m.input = append(m.input, character)
					}
				}
			}
		}
	case progress:
		if item.generation == m.generation {
			m.addLine("• " + safe(item.event.Kind))
		}
	case finished:
		if item.generation != m.generation {
			return m, nil
		}
		m.running = false
		m.cancel = nil
		if item.err != nil {
			m.addLine("Stopped: " + safe(item.err.Error()))
		} else {
			m.addLine("Agent: " + safe(item.text))
		}
		if m.quitting {
			return m, tea.Quit
		}
		return m, m.start()
	}
	return m, nil
}
func (m *model) View() tea.View {
	lines := m.lines
	if len(lines) > 14 {
		lines = lines[len(lines)-14:]
	}
	return tea.NewView(strings.Join(lines, "\n") + fmt.Sprintf("\n\nRunning: %t  Queued: %d\n> %s\n", m.running, len(m.queue), string(m.input)))
}
func safe(text string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return -1
		}
		return character
	}, text)
}

func (m *model) addLine(line string) {
	m.lines = append(m.lines, line)
	if len(m.lines) > 200 {
		m.lines = append([]string(nil), m.lines[len(m.lines)-200:]...)
	}
}
