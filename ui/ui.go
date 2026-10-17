// Package ui renders the harness in a terminal.
//
// The interface is deliberately read-mostly. It shows coarse progress and the
// final answer, exactly as described in LIMITATIONS.md: there is no live token
// rendering, no approval dialog, no conversation steering, and no full
// scrollback. Restyling must not silently imply capabilities that do not exist.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/john-nash-rs/captaincode/core"
)

// Runner executes one question and reports progress through the callback.
type Runner func(context.Context, string, func(core.Event)) (string, error)

// Info is the harness state the interface reports but does not own.
//
// The caller already resolved the provider, model, workspace, and permission
// grants before starting the UI. Passing them explicitly keeps the dependency
// visible: the footer states what is actually connected rather than guessing
// from process arguments. The fields are display-only and grant nothing.
type Info struct {
	Provider      string
	Model         string
	Workspace     string
	AllowWrites   bool
	AllowCommands bool
}

const (
	inputLimit = 2000 // maximum runes of buffered input
	queueLimit = 10   // maximum queued prompts
	lineLimit  = 500  // retained transcript lines
)

type progress struct {
	generation int
	event      core.Event
}

type finished struct {
	generation int
	text       string
	err        error
}

// tick drives the spinner and the elapsed-time readout. It is separate from
// progress events so that a silent run still shows that something is alive.
type tick time.Time

type model struct {
	runner            Runner
	send              func(tea.Msg)
	input             []rune
	queue             []string
	lines             []string
	running, quitting bool
	cancel            context.CancelFunc
	generation        int

	// Presentation state. None of it affects harness behavior.
	info          Info
	theme         theme
	showArt       bool
	width, height int
	phase         string
	started       time.Time
	frame         int
	forceQuit     bool // set once the terminal closes so Exit cannot hang
}

// theme holds the styles used to render. A zero theme paints plain text, which
// is the safe default: tests construct a model directly and never set one, and
// a machine with no color support must not receive escape codes.
//
// lipgloss emits ANSI unconditionally, so the decision to color at all is made
// once here rather than per style. NO_COLOR, TERM=dumb, and a non-TTY stderr
// all disable it.
type theme struct {
	user, assistant, muted, failure, phase, prompt, brand lipgloss.Style
	border                                                lipgloss.Border
	borderColor                                           color.Color
}

// newTheme returns a color palette, or plain styles when color is disabled.
// Border is always drawn: it is structure, not decoration.
func newTheme(enabled bool) theme {
	if !enabled {
		var plain lipgloss.Style
		return theme{
			user: plain, assistant: plain, muted: plain, failure: plain,
			phase: plain, prompt: plain, brand: plain,
			border: lipgloss.RoundedBorder(),
		}
	}
	return theme{
		user:        lipgloss.NewStyle().Foreground(color.RGBA{R: 0x7d, G: 0xa6, B: 0xff, A: 0xff}),
		assistant:   lipgloss.NewStyle().Foreground(color.RGBA{R: 0xd8, G: 0xde, B: 0xe9, A: 0xff}),
		muted:       lipgloss.NewStyle().Foreground(color.RGBA{R: 0x6b, G: 0x72, B: 0x80, A: 0xff}),
		failure:     lipgloss.NewStyle().Foreground(color.RGBA{R: 0xf2, G: 0x6d, B: 0x78, A: 0xff}),
		phase:       lipgloss.NewStyle().Foreground(color.RGBA{R: 0xa6, G: 0xe3, B: 0xa1, A: 0xff}),
		prompt:      lipgloss.NewStyle().Foreground(color.RGBA{R: 0x7d, G: 0xa6, B: 0xff, A: 0xff}).Bold(true),
		brand:       lipgloss.NewStyle().Foreground(color.RGBA{R: 0xcb, G: 0xb6, B: 0xff, A: 0xff}).Bold(true),
		border:      lipgloss.RoundedBorder(),
		borderColor: color.RGBA{R: 0x3a, G: 0x40, B: 0x4a, A: 0xff},
	}
}

func Run(runner Runner, info Info) error {
	paint := newTheme(colorEnabled())
	m := &model{
		runner:  runner,
		info:    info,
		theme:   paint,
		showArt: true,
		lines:   []string{banner(paint, 80)},
		width:   80,
		height:  24,
	}
	program := tea.NewProgram(m)
	m.send = program.Send
	_, err := program.Run()
	if m.forceQuit && (errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, tea.ErrProgramPanic)) {
		return nil
	}
	return err
}

func (m *model) Init() tea.Cmd {
	return nil
}

// start dispatches the next queued prompt. It is the only place that calls the
// runner, so every run has exactly one cancel context and one generation.
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
	m.started = time.Now()
	m.phase = "starting"
	m.showArt = false
	m.addLine(m.theme.user.Render(question))
	return func() tea.Msg {
		text, err := m.runner(ctx, question, func(event core.Event) { m.send(progress{generation, event}) })
		cancel()
		return finished{generation, text, err}
	}
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch item := message.(type) {
	case tea.WindowSizeMsg:
		if item.Width > 0 {
			m.width, m.height = item.Width, item.Height
		}
		return m, nil

	case tick:
		if m.running {
			m.frame++
			m.addLine("") // keep the phase line current between events
			m.lines = m.lines[:len(m.lines)-1]
		}
		return m, m.spinner()

	case tea.KeyPressMsg:
		return m.key(item)

	case progress:
		// A stale event belongs to a run that has already finished. Rendering
		// it would misreport what the current run did.
		if item.generation != m.generation {
			return m, nil
		}
		m.note(item.event)
		return m, nil

	case finished:
		if item.generation != m.generation {
			return m, nil
		}
		m.running = false
		m.cancel = nil
		if item.err != nil {
			m.addLine(m.theme.failure.Render("Error: " + item.err.Error()))
		} else {
			m.addLine(m.theme.assistant.Render(item.text))
		}
		if m.quitting {
			return m, tea.Quit
		}
		return m, m.start()
	}
	return m, nil
}

// key handles input. Queueing, cancellation, and the two input caps are the
// behaviors covered by ui_test.go and must keep their exact semantics.
func (m *model) key(item tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch item.String() {
	case "ctrl+c":
		if m.running {
			m.quitting = true
			m.queue = nil
			m.cancel()
			return m, nil
		}
		return m, tea.Quit

	case "ctrl+d":
		if len(m.input) == 0 {
			return m, tea.Quit
		}

	case "esc":
		if m.cancel != nil {
			m.cancel()
		}

	case "enter":
		// While a run is active the prompt is buffered, not dispatched.
		if len(m.input) > 0 && len(m.queue) < queueLimit && !m.quitting {
			m.queue = append(m.queue, string(m.input))
			m.input = nil
			return m, m.start()
		}

	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}

	default:
		for _, character := range item.Key().Text {
			if unicode.IsControl(character) || len(m.input) >= inputLimit {
				continue
			}
			m.input = append(m.input, character)
		}
	}
	return m, nil
}

// note records one engine event. Events are collapsed into a compact phase
// line rather than a growing list of dots.
func (m *model) note(event core.Event) {
	m.phase = phase(event)
}

func (m *model) spinner() tea.Cmd {
	if !m.running {
		return nil
	}
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return tick(time.Now()) })
}

// View composes the frame.
//
// The renderer sizes an inline frame to the height of its content, so a short
// frame floats near the top of the terminal. To keep the input pinned to the
// bottom row we always emit exactly height lines: the transcript at the top, a
// run of blank filler, then the status and prompt blocks at the bottom.
func (m *model) View() tea.View {
	width := m.width
	if width < 24 {
		width = 24
	}
	height := m.height
	if height < 6 {
		height = 6
	}

	// Build the bottom block first; its height is fixed.
	var bottom strings.Builder
	if m.running {
		bottom.WriteString(m.statusLine())
		bottom.WriteByte('\n')
	}
	bottom.WriteString(m.prompt(width))
	bottomRows := strings.Count(bottom.String(), "\n") + 1

	// The splash occupies transcript rows while it is shown, so it has to come
	// out of the budget before the conversation is laid out.
	var head []string
	if m.showArt {
		head = splash(m.theme, width)
	}
	budget := height - bottomRows - 1 - len(head)
	if budget < 1 {
		budget = 1
	}
	visible := m.tail(budget, width)

	var b strings.Builder
	rows := 0
	for _, line := range append(head, visible...) {
		wrapped := clip(line, width)
		b.WriteString(wrapped)
		b.WriteByte('\n')
		rows += strings.Count(wrapped, "\n") + 1
	}
	// Blank filler pushes the prompt to the final row.
	for ; rows < height-bottomRows; rows++ {
		b.WriteByte('\n')
	}
	b.WriteString(bottom.String())

	view := tea.NewView(b.String())
	// Position the real terminal cursor inside the editor so native editing and
	// copy/paste keep working. X is the text column inside the border; Y is the
	// prompt's row inside the box, which sits above the footer and hint.
	view.Cursor = &tea.Cursor{
		Position: tea.Position{
			X: promptWidth,
			Y: height - bottomRows + boxContentRow + offsetOfPrompt(m.running),
		},
		Shape: tea.CursorBar,
	}
	return view
}

// boxContentRow is the row inside the bordered box that holds the prompt text:
// one row for the top border, then the first content row.
const boxContentRow = 1

// offsetOfPrompt returns the row of the prompt line within the bottom block.
// The status line, when present, occupies row zero.
func offsetOfPrompt(running bool) int {
	if running {
		return 1
	}
	return 0
}

// tail returns the last lines that fit in budget rendered rows after wrapping,
// dropping the oldest lines first.
func (m *model) tail(budget, width int) []string {
	var chosen []string
	rows := 0
	for i := len(m.lines) - 1; i >= 0; i-- {
		w := strings.Count(clip(m.lines[i], width), "\n") + 1
		if rows+w > budget {
			break
		}
		chosen = append([]string{m.lines[i]}, chosen...)
		rows += w
	}
	return chosen
}

func (m *model) statusLine() string {
	elapsed := ""
	if !m.started.IsZero() {
		elapsed = " " + formatDuration(time.Since(m.started))
	}
	queued := ""
	if n := len(m.queue); n > 0 {
		queued = fmt.Sprintf(" · %d queued", n)
	}
	phase := m.phase
	if phase == "" {
		phase = "working"
	}
	return spinnerFrames[m.frame%len(spinnerFrames)] + " " + m.theme.phase.Render(phase+elapsed+queued)
}

// inputBox draws the bordered editor that holds the prompt. The border is
// structural, not decorative: it shows exactly how much room the editor has.
func (m *model) inputBox(width int) string {
	body := m.theme.prompt.Render("›") + " "
	if len(m.input) == 0 && !m.running {
		body += m.theme.muted.Render("ask about this project…")
	} else {
		body += safe(string(m.input))
	}
	// lipgloss Width() is the outer width, borders included. The text column is
	// that minus the border and the padding on both sides.
	inner := width
	if inner < boxChrome+8 {
		inner = boxChrome + 8
	}
	box := lipgloss.NewStyle().
		Border(m.theme.border).
		PaddingLeft(1).
		PaddingRight(1).
		Width(inner)
	if m.theme.borderColor != nil {
		box = box.BorderForeground(m.theme.borderColor)
	}
	return box.Render(body)
}

// footer names the connected provider, the model, and the grants in force.
// It reports state the caller resolved; it grants nothing itself.
func (m *model) footer(width int) string {
	provider := m.info.Provider
	if provider == "" {
		provider = "unknown"
	}
	model := m.info.Model
	if model == "" {
		model = "no model configured"
	}
	mode := "read-only"
	switch {
	case m.info.AllowWrites && m.info.AllowCommands:
		mode = "writes + commands"
	case m.info.AllowWrites:
		mode = "writes"
	case m.info.AllowCommands:
		mode = "commands"
	}
	// Leave room for the separators and the mode badge before shortening the
	// model identifier, which is the only unbounded field.
	budget := width - lipgloss.Width(provider) - lipgloss.Width(mode) - 6
	return m.theme.muted.Render(provider) + m.theme.muted.Render(" · ") +
		m.theme.brand.Render(elide(model, budget)) + m.theme.muted.Render(" · ") +
		m.theme.muted.Render(mode)
}

// elide shortens a long identifier from the middle so the distinguishing
// prefix and suffix both stay readable.
func elide(text string, width int) string {
	if width <= 1 || lipgloss.Width(text) <= width {
		return text
	}
	runes := []rune(text)
	if width <= 2 {
		return "…"
	}
	keep := width - 1 // one rune for the ellipsis
	head := (keep + 1) / 2
	tail := keep / 2
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

func (m *model) prompt(width int) string {
	var b strings.Builder
	b.WriteString(m.inputBox(width))
	b.WriteByte('\n')
	b.WriteString(m.footer(width))
	b.WriteByte('\n')
	hint := "enter send · esc cancel run · ctrl+c quit"
	if !m.running {
		hint = "enter send · ctrl+c quit"
	}
	b.WriteString(m.theme.muted.Render(truncate(hint, width-2)))
	return b.String()
}

func (m *model) addLine(line string) {
	if line != "" {
		m.lines = append(m.lines, line)
	}
	if len(m.lines) > lineLimit {
		m.lines = append([]string(nil), m.lines[len(m.lines)-lineLimit:]...)
	}
}

// banner is the one-line greeting. It shortens rather than wrapping, because
// a wrapped brand line reads as a layout bug rather than a design.
func banner(paint theme, width int) string {
	name, suffix := "CaptainCode", "  ·  coding agent harness"
	if lipgloss.Width(name+suffix) > width {
		return paint.brand.Render(truncate(name, width))
	}
	return paint.brand.Render(name) + paint.muted.Render(suffix)
}

// asciiArt is the full-screen splash. It is decorative, so it is only drawn
// when the terminal is wide enough to show it without wrapping, and it is
// replaced by the one-line banner once the first run starts.
var asciiArt = []string{
	` ██████╗ █████╗ ██████╗ ████████╗ ██╗██╗   ██╗`,
	`██╔════╝██╔══██╗██╔═══██╗╚══██╔══╝██║██║   ██║`,
	`██║     ███████║██║   ██║   ██║   ██║██║   ██║`,
	`██║     ██╔══██║██║   ██║   ██║   ██║██║   ██║`,
	`╚██████╗██║  ██║╚██████╔╝   ██║   ╚██████╔╝`,
	` ╚═════╝╚═╝  ╚═╝ ╚═════╝    ╚═╝    ╚═════╝ `,
}

const tagline = "with <3 by SkillCaptain"

// artWidth is the widest row of asciiArt, which is what decides whether the
// terminal can show it unwrapped.
var artWidth = func() int {
	w := 0
	for _, line := range asciiArt {
		if n := lipgloss.Width(line); n > w {
			w = n
		}
	}
	return w
}()

// splash returns the startup rows: the artwork when it fits, otherwise the
// compact one-line banner. Narrow terminals get the banner rather than a
// wrapped, broken drawing.
func splash(paint theme, width int) []string {
	if width < artWidth {
		return []string{banner(paint, width)}
	}
	rows := make([]string, 0, len(asciiArt)+2)
	for _, line := range asciiArt {
		rows = append(rows, paint.brand.Render(line))
	}
	// Centre the tagline under the artwork.
	pad := (artWidth - lipgloss.Width(tagline)) / 2
	if pad < 0 {
		pad = 0
	}
	rows = append(rows, "", paint.muted.Render(strings.Repeat(" ", pad)+tagline))
	return rows
}

// boxChrome is the width the border and padding consume around the editor text.
const boxChrome = 4

// promptWidth is the column of the text cursor inside the box: border, padding,
// the glyph, and one space.
const promptWidth = boxChrome

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// phase turns an engine event into a short human label. Unknown kinds are
// passed through so a new event is visible rather than silently dropped.
func phase(event core.Event) string {
	label := event.Kind
	if event.Tool != "" {
		label = event.Kind + " " + event.Tool
	} else if event.CallID != "" {
		label = event.Kind + " " + event.CallID
	}
	switch event.Kind {
	case "model_started", "model_finished", "ready_for_model":
		return "thinking"
	case "tools_planned":
		return "planning tools"
	case "tool_started", "tool_finished", "tool_observed":
		return label
	case "completed":
		return "done"
	case "failed", "cancelled", "blocked":
		return event.Kind
	}
	return label
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func truncate(text string, width int) string {
	if width <= 1 || lipgloss.Width(text) <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// clip hard-wraps one already-styled line to the terminal width. Styling is
// applied to the text before it reaches here, so wrapping must not drop the
// escape sequences that give each role its color.
func clip(line string, width int) string {
	if width <= 1 || lipgloss.Width(line) <= width {
		return line
	}
	return lipgloss.Wrap(line, width, "")
}

// safe strips control characters from model and tool text so that untrusted
// output cannot move the cursor or rewrite the screen.
func safe(text string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return -1
		}
		return character
	}, text)
}

// colorEnabled reports whether ANSI styling should be emitted. It honors
// NO_COLOR, TERM=dumb, and a non-TTY stdout so piped output stays plain.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Styles live in theme so that the color decision is made once. See newTheme.
