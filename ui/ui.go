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

// Settings is what the interface needs in order to show and change
// connection state. It is an interface so this package never reads or writes
// the filesystem or a credential itself, and so tests can supply a fake.
type Settings interface {
	// Status reports the current provider, model, and a credential rendered
	// safe to display. The implementation must never return a raw key.
	Status() (provider, model, credential string)
	// StoreCredential persists a credential supplied by the operator.
	StoreCredential(string) error
	// ClearCredential removes the stored credential.
	ClearCredential() error
	// StoreModel persists a model identifier.
	StoreModel(string) error
	// CredentialPath is the file a stored credential lives in, reported so the
	// operator can find and delete it.
	CredentialPath() string
}

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
	Settings      Settings
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

	// secret is true while the operator is typing a credential. Input is
	// masked and never enters the transcript.
	secret bool
	notice string // transient confirmation shown under the prompt
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
		// Leaving credential entry must not also cancel a running request.
		if m.secret {
			m.secret, m.input = false, nil
			m.notice = "cancelled"
			return m, nil
		}
		if m.cancel != nil {
			m.cancel()
		}

	case "enter":
		if m.secret {
			return m.commitSecret()
		}
		// While a run is active the prompt is buffered, not dispatched.
		if len(m.input) > 0 && len(m.queue) < queueLimit && !m.quitting {
			text := string(m.input)
			m.input = nil
			// A leading slash is an interface command, never a model prompt.
			if strings.HasPrefix(text, "/") {
				m.runCommand(text)
				return m, nil
			}
			m.queue = append(m.queue, text)
			return m, m.start()
		}

	case "tab":
		// Finish a uniquely matching command. An ambiguous prefix is left as
		// typed so the suggestion list stays the operator's guide.
		if !m.secret {
			m.input = []rune(completion(string(m.input)))
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

// runCommand executes an interface command. Commands are handled entirely
// inside the interface: they never become prompts, so they are never sent to a
// model and never reach the engine.
func (m *model) runCommand(text string) {
	fields := strings.Fields(text)
	verb := strings.ToLower(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	settings := m.info.Settings

	if verb != "/openrouter" && verb != "/config" {
		if verb == "/help" || verb == "/?" {
			m.addLine(m.theme.muted.Render(commandHelp))
			return
		}
		m.addLine(m.theme.failure.Render("Unknown command " + verb + ". Try /help."))
		return
	}

	// No argument reports status.
	if arg == "" {
		provider, model, credential := settings.Status()
		m.info.Provider, m.info.Model = provider, model
		m.addLine(m.theme.muted.Render("provider  " + orUnknown(provider)))
		m.addLine(m.theme.muted.Render("model     " + orUnknown(model)))
		// Status is required to return an already-masked credential.
		m.addLine(m.theme.muted.Render("credential " + credential))
		m.addLine(m.theme.muted.Render("stored in  " + settings.CredentialPath()))
		return
	}

	name, value, _ := strings.Cut(arg, " ")
	name = strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimSpace(value)

	switch name {
	case "api-key", "key":
		m.secret = true
		m.input = nil
		m.notice = "paste the key; input is hidden. enter to save, esc to cancel."
		m.addLine(m.theme.brand.Render("/openrouter api-key"))
	case "clear-key":
		if err := settings.ClearCredential(); err != nil {
			m.addLine(m.theme.failure.Render("could not clear: " + err.Error()))
			return
		}
		m.notice = "stored credential removed"
		m.addLine(m.theme.muted.Render("stored credential removed"))
	case "model":
		if value == "" {
			m.addLine(m.theme.failure.Render("usage: /openrouter model <model-id>"))
			return
		}
		if err := settings.StoreModel(value); err != nil {
			m.addLine(m.theme.failure.Render("could not save model: " + err.Error()))
			return
		}
		m.info.Model = value
		m.notice = "model set to " + value
		m.addLine(m.theme.muted.Render("model set to " + value))
	case "path":
		m.addLine(m.theme.muted.Render(settings.CredentialPath()))
	default:
		m.addLine(m.theme.failure.Render("unknown option " + name + ". Try /help."))
	}
}

// commitSecret stores a typed credential.
//
// The typed value is held only in m.input for the duration of entry. It is
// cleared here, never written to m.lines, and never returned to the prompt
// area, so it cannot reach the transcript that is sent to a model.
func (m *model) commitSecret() (tea.Model, tea.Cmd) {
	key := string(m.input)
	m.input = nil
	m.secret = false
	if key == "" {
		m.notice = "no key entered"
		return m, nil
	}
	if err := m.info.Settings.StoreCredential(key); err != nil {
		m.addLine(m.theme.failure.Render("could not store key: " + err.Error()))
		m.notice = "key not stored"
		return m, nil
	}
	_, _, credential := m.info.Settings.Status()
	m.notice = "credential saved · " + credential
	m.addLine(m.theme.brand.Render("credential saved · " + credential))
	return m, nil
}

func orUnknown(value string) string {
	if value == "" {
		return "not set"
	}
	return value
}

const commandHelp = "  /openrouter                 show provider, model, credential, keyfile path" +
	"\n  /openrouter api-key        store a credential (input is hidden)" +
	"\n  /openrouter model <id>     store a model identifier" +
	"\n  /openrouter clear-key      remove the stored credential" +
	"\n  /openrouter path           print the keyfile path" +
	"\n  /help                      list these commands"

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

	// Build the bottom block first; its row count drives both the transcript
	// budget and the cursor position.
	bottom, bottomRows := m.bottomBlock(width, height)

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
	b.WriteString(bottom)

	view := tea.NewView(b.String())
	// Position the real terminal cursor inside the editor so native editing and
	// copy/paste keep working. X tracks the typed text so the bar advances as
	// the operator writes, and is clamped so it never crosses the border. Y is
	// the prompt's row, which shifts down when a status line or a suggestion
	// list is present.
	view.Cursor = &tea.Cursor{
		Position: tea.Position{
			X: m.cursorColumn(width),
			Y: height - bottomRows + boxContentRow + offsetOfPrompt(m.running, bottomRows),
		},
		Shape: tea.CursorBar,
	}
	return view
}

// cursorColumn is the editor's text column plus the visible input width. The
// value is clamped to the last column inside the border.
// maskWidth is the fixed number of bullets shown while a credential is typed.
const maskWidth = 12

func (m *model) cursorColumn(width int) int {
	if m.secret {
		// Credential entry renders a fixed mask, so the caret sits after it.
		return promptWidth + maskWidth
	}
	col := promptWidth + lipgloss.Width(safe(string(m.input)))
	if limit := width - 2; col > limit { // 2 for the right border and its padding
		col = limit
	}
	if col < promptWidth {
		col = promptWidth
	}
	return col
}

// boxContentRow is the row inside the bordered box that holds the prompt text:
// one row for the top border, then the first content row.
const boxContentRow = 1

// offsetOfPrompt returns the row of the prompt within the bottom block,
// relative to the block's first row. The prompt sits inside the bordered
// editor, so the only thing above it is the editor's own top border, counted
// separately by boxContentRow, and an optional status line.
func offsetOfPrompt(running bool, _ int) int {
	if running {
		return 1 // the status line occupies the first row of the block
	}
	return 0
}

// commandList is the authoritative set of interface commands. Suggestions and
// completion both read from it so they cannot drift apart.
var commandList = []string{
	"/openrouter",
	"/openrouter api-key",
	"/openrouter model",
	"/openrouter clear-key",
	"/openrouter path",
	"/help",
}

// suggestions returns the rows shown beneath the editor while the input looks
// like a command. Nothing is shown for ordinary text, so a normal prompt is
// never crowded by a menu it does not need.
func (m *model) suggestions() []string {
	if m.secret || !strings.HasPrefix(string(m.input), "/") {
		return nil
	}
	typed := string(m.input)
	var rows []string
	for _, c := range commandList {
		if strings.HasPrefix(c, typed) {
			rows = append(rows, m.theme.muted.Render("  "+c))
		}
		if len(rows) == maxSuggestions {
			break
		}
	}
	return rows
}

const maxSuggestions = 6

// completion returns the input extended to the best matching command. It
// returns the first match when several fit, which is the same one the
// suggestion list shows first, so what Tab produces is never a surprise. It
// returns the input unchanged when nothing matches.
func completion(input string) string {
	if input == "" || !strings.HasPrefix(input, "/") {
		return input
	}
	for _, c := range commandList {
		if strings.HasPrefix(c, input) {
			return c
		}
	}
	return input
}

// bottomBlock assembles the fixed rows beneath the transcript: an optional
// status line, the bordered editor, an optional suggestion list, the footer,
// and a hint line. It returns the text and its exact row count so the
// transcript budget and the cursor stay consistent with what is drawn.
func (m *model) bottomBlock(width, height int) (string, int) {
	var b strings.Builder
	if m.running {
		b.WriteString(m.statusLine())
		b.WriteByte('\n')
	}
	b.WriteString(m.inputBox(width))
	b.WriteByte('\n')
	// Suggestions are dropped when the terminal is too short to keep the
	// prompt and the transcript on screen.
	if rows := m.suggestions(); height-len(rows) >= 8 {
		for _, row := range rows {
			b.WriteString(row)
			b.WriteByte('\n')
		}
	}
	b.WriteString(m.footer(width))
	b.WriteByte('\n')

	hint := "enter send · esc cancel run · ctrl+c quit"
	switch {
	case m.secret:
		hint = "enter save · esc cancel · input is not shown"
	case strings.HasPrefix(string(m.input), "/"):
		hint = "tab completes · enter runs · esc clears"
	case !m.running:
		hint = "enter send · ctrl+c quit · /help for commands"
	}
	if m.notice != "" {
		hint = m.notice
	}
	b.WriteString(m.theme.muted.Render(truncate(hint, width-2)))
	text := b.String()
	return text, strings.Count(text, "\n") + 1
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
	switch {
	case m.secret:
		// Credential entry never renders the typed characters, not even as
		// dots whose count would reveal the length.
		body += m.theme.muted.Render("••••••••••••")
	case len(m.input) == 0 && !m.running:
		body += m.theme.muted.Render("ask about this project…  /help")
	default:
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
//
// The artwork is a condensed 6-pixel font in Unicode quadrant glyphs, 32
// columns by 4 rows. GoldenArtHash pins its contents: a corrupted or
// accidentally edited wordmark must fail a test rather than ship.
var asciiArt = []string{
	`▞▀▖      ▐     ▗    ▞▀▖     ▌   `,
	`▌  ▝▀▖▛▀▖▜▀ ▝▀▖▄ ▛▀▖▌  ▞▀▖▞▀▌▞▀▖`,
	`▌ ▖▞▀▌▙▄▘▐ ▖▞▀▌▐ ▌ ▌▌ ▖▌ ▌▌ ▌▛▀ `,
	`▝▀ ▝▀▘▌   ▀ ▝▀▘▀▘▘ ▘▝▀ ▝▀ ▝▀▘▝▀▘`,
}

// GoldenArtHash is the expected sha256 of asciiArt joined by newlines.
const GoldenArtHash = "4a423c8c5ab4edab"

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
