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

// Probe reports whether the provider accepts a credential and serves a model.
//
// It is an interface so this package performs no network calls itself and so
// tests can supply a fake. The returned text is shown as-is, so implementations
// must never include a credential value in either string.
type Probe interface {
	// Check reports one result per model identifier given.
	Check(ctx context.Context, models []string) []Result
}

// Result is one model's connectivity outcome. Err is nil when the model
// answered.
type Result struct {
	Model string
	Err   error
}

// Model is one selectable model, as the interface needs it.
type Model struct {
	ID      string
	Name    string
	Price   string
	Context int64
	Free    bool
}

// Browser lists the models an account may choose from.
//
// It is an interface so this package makes no network calls, and so tests can
// supply a fixed list. Asking somebody to type a model identifier from memory
// is the thing this exists to remove.
type Browser interface {
	// List returns the models available to the account.
	List(ctx context.Context) ([]Model, error)
	// Search narrows the list. An empty query returns the unfiltered list.
	Search(ctx context.Context, query string) ([]Model, error)
}

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
	// Grants reports the current permissions.
	Grants() (writes, commands bool)
	// StoreGrants persists a permission change.
	StoreGrants(writes, commands bool) error
	// CredentialPath is the file a stored credential lives in, reported so the
	// operator can find and delete it.
	CredentialPath() string
}

// YesNo renders a permission for display.
func YesNo(v bool) string {
	if v {
		return "on"
	}
	return "off"
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
	// Probe checks connectivity. It may be nil, in which case the interface
	// reports state from the configuration alone and says the connection is
	// unverified rather than implying a check it did not perform.
	Probe Probe
	// Browser lists the models the account may choose from. It may be nil, in
	// which case setting a model requires typing the identifier.
	Browser Browser
	// Models are offered as suggestions when setting a model, so nobody has to
	// recall an identifier from memory or read it off a web page.
	Models []string
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
	// advice is the remedy for err, or an empty string. It is resolved through
	// a method assertion rather than by importing the provider, so this package
	// stays free of any knowledge of which service is in use.
	advice string
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
	// scroll is how far the transcript is scrolled back from the bottom. Zero
	// is the live view, which is where it snaps back to when a run produces
	// output, because that is when the newest thing matters.
	scroll int

	// secret is true while the operator is typing a credential. Input is
	// masked and never enters the transcript.
	secret bool
	notice string // transient confirmation shown under the prompt

	// menu drives the suggestion list. chosen is the highlighted row, which
	// arrow keys move and enter accepts; it is -1 while nothing is
	// highlighted, so the common case of typing a prompt never shows a
	// cursor in a menu nobody opened.
	menu struct {
		chosen int
		rows   []string
	}

	// checking is true while a connectivity check is in flight, and checked
	// records the outcome so the footer can state it.
	checking bool
	verified bool
	lastErr  string
	// startupCheck verifies a remembered key and model when the interface
	// opens, so a returning user is not left wondering whether their settings
	// survived.
	startupCheck tea.Cmd
	// quietSuccess suppresses the success line for the startup check.
	quietSuccess bool

	// pendingModel is true once a credential exists but no model does, so
	// saving the key continues to the model step instead of stopping.
	pendingModel bool

	// picking is true while the model list is open. The query filters the list
	// as it is typed, so a model can be found by any part of its name.
	picking  bool
	query    []rune
	found    []Model
	pick     int
	loading  bool
	listErr  string
	modelsAt int // list length at the last render, for the click mapping
	// pendingFilter is the text typed before the last backspace, kept so
	// deleting back to empty restores the full list rather than an empty one.
	pendingFilter []rune
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
	// A returning user should not have to run /status to find out whether
	// their saved key and model still work. Checking on start answers the
	// question they arrived with, once, rather than on demand.
	//
	// Init issues it, which is where Bubble Tea expects startup work to come
	// from; a command started here would never be run.
	if info.Model != "" && info.Settings != nil && info.Probe != nil {
		if _, _, credential := info.Settings.Status(); credential != "" && !strings.HasPrefix(credential, "not set") {
			m.checking = true
			m.quietSuccess = true
			m.startupCheck = m.check(info.Model)
		}
	}
	_, err := program.Run()
	if m.forceQuit && (errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, tea.ErrProgramPanic)) {
		return nil
	}
	return err
}

func (m *model) Init() tea.Cmd {
	return m.startupCheck
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
		return finished{generation, text, err, adviceFor(err)}
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

	case tea.PasteMsg:
		return m.paste(item.Content)

	case listed:
		// A result arriving after the picker was closed belongs to a request
		// nobody is waiting for any more.
		if !m.picking {
			return m, nil
		}
		m.applyModelList(item)
		return m, nil

	case scrolled:
		return m, nil

	case checked:
		m.report(item)
		return m, nil

	case picked:
		// A click landed on a suggestion row. Accepting it here rather than
		// only highlighting it means the mouse does the same thing the arrow
		// keys plus enter would.
		if item.command != "" {
			m.input = nil
			m.syncMenu()
			return m, m.runCommand(item.command)
		}
		return m, nil

	case modelPicked:
		// A click landed on a model row. Only accept it when it is on screen:
		// the index is a position in the filtered list, but the row drawn may
		// be a window onto that list.
		if !m.picking || item.index < 0 || item.index >= len(m.found) {
			return m, nil
		}
		m.pick = item.index
		return m, m.acceptModel()

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
		// New output returns the view to the live end, because that is the
		// moment reading what just arrived matters most. Scrolling back to
		// re-read something is not undone by it.
		m.scroll = 0
		if item.err != nil {
			m.addLine(m.theme.failure.Render("Error: " + item.err.Error()))
			// The cause alone leaves someone guessing. When the failure has a
			// known remedy, say so, and name the command that applies.
			if advice := item.advice; advice != "" {
				m.addLine(m.theme.muted.Render("  " + advice))
				m.addLine(m.theme.muted.Render("  run /models to pick a different model"))
			}
			m.notice = "press up to see the last answer"
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

// paste inserts pasted text.
//
// Pasting was broken for a long time because Bubble Tea reports bracketed paste
// as its own message, and the update loop only listened for key presses. Every
// paste was therefore discarded silently: the terminal sent the text, nothing
// displayed it, and a credential pasted from a password manager simply never
// arrived. Insertion goes through the same path as typing so the input cap and
// the credential mask both still apply.
func (m *model) paste(text string) (tea.Model, tea.Cmd) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// A paste spanning lines is a prompt, not a slash command. Only the first
	// line belongs at the editor; sending the rest anywhere would be a
	// surprise, and dropping them silently is the conservative choice.
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	if text == "" {
		return m, nil
	}
	// A model identifier pasted into the model picker selects it directly
	// rather than being filtered character by character.
	if m.picking {
		return m, m.filterModels(text)
	}
	room := inputLimit - len(m.input)
	if room <= 0 {
		return m, nil
	}
	runes := []rune(text)
	if len(runes) > room {
		runes = runes[:room]
	}
	m.input = append(m.input, runes...)
	m.syncMenu()
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
		if m.picking {
			m.closeModels()
			m.addLine(m.theme.muted.Render("model selection cancelled"))
			return m, nil
		}
		if m.secret {
			m.secret, m.input = false, nil
			m.notice = "cancelled"
			return m, nil
		}
		if m.cancel != nil {
			m.cancel()
		}

	case "enter":
		if m.picking {
			return m, m.acceptModel()
		}
		if m.secret {
			return m.commitSecret()
		}
		// A highlighted suggestion is accepted before the input is read, so
		// the arrow keys and the mouse have the same effect as typing the
		// whole command.
		if chosen := m.chosen(); chosen != "" {
			m.input = nil
			m.syncMenu()
			// The command may start a network request, so it has to be
			// returned to the runtime. Discarding it leaves any command with
			// a pending fetch stuck on its loading message.
			return m, m.runCommand(chosen)
		}
		// While a run is active the prompt is buffered, not dispatched.
		if len(m.input) > 0 && len(m.queue) < queueLimit && !m.quitting {
			text := string(m.input)
			m.input = nil
			m.syncMenu()
			// A leading slash is an interface command, never a model prompt.
			if strings.HasPrefix(text, "/") {
				return m, m.runCommand(text)
			}
			m.queue = append(m.queue, text)
			return m, m.start()
		}

	case "tab":
		// Finish a uniquely matching command. An ambiguous prefix is left as
		// typed so the suggestion list stays the operator's guide.
		if !m.secret {
			m.input = []rune(completion(string(m.input)))
			m.syncMenu()
		}

	case "up", "ctrl+p":
		// The command menu and the model picker own the arrows while they are
		// open. Everywhere else the arrows scroll the transcript, which is the
		// only way to reach an answer that has already left the screen.
		if m.picking {
			m.moveModel(-1)
			return m, nil
		}
		if !m.secret && m.moveMenu(-1) {
			return m, nil
		}
		// While a credential is being typed the field is the only thing that
		// should respond. Scrolling behind it would move the prompt out from
		// under whatever is being entered.
		if !m.secret {
			m.scrollBy(1)
		}

	case "down", "ctrl+n":
		if m.picking {
			m.moveModel(1)
			return m, nil
		}
		if !m.secret && m.moveMenu(1) {
			return m, nil
		}
		if !m.secret {
			m.scrollBy(-1)
		}

	case "pgup":
		if !m.picking && !m.secret {
			m.scrollBy(10)
		}

	case "pgdown":
		if !m.picking && !m.secret {
			m.scrollBy(-10)
		}

	case "home":
		if !m.picking && !m.secret {
			m.scroll = len(m.lines)
		}

	case "end":
		if !m.picking && !m.secret {
			m.scroll = 0
		}

	case "backspace":
		if m.picking {
			if len(m.query) > 0 {
				m.query = m.query[:len(m.query)-1]
			}
			// Backspacing to nothing restores the full list rather than an
			// empty one, which is what filtering an empty query should mean.
			m.query = append(m.query, m.pendingFilter...)
			return m, nil
		}
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		m.syncMenu()

	default:
		for _, character := range item.Key().Text {
			if unicode.IsControl(character) || len(m.input) >= inputLimit {
				continue
			}
			if m.picking {
				m.query = append(m.query, character)
				continue
			}
			m.input = append(m.input, character)
		}
		if m.picking {
			return m, m.filterModels(string(m.query))
		}
		m.syncMenu()
	}
	return m, nil
}

// runCommand executes an interface command. Commands are handled entirely
// inside the interface: they never become prompts, so they are never sent to a
// model and never reach the engine.
func (m *model) runCommand(text string) tea.Cmd {
	fields := strings.Fields(text)
	verb := strings.ToLower(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))

	switch verb {
	case "/help", "/?":
		m.addLine(m.theme.muted.Render(commandHelp))
	case "/connect":
		return m.runConnect()
	case "/status":
		return m.runStatus()
	case "/models":
		return m.openModels()
	case "/permissions", "/perm":
		return m.runPermissions(arg)
	case "/tools":
		m.runTools()
	case "/openrouter", "/config":
		// The returned command is what opens the model picker and what runs
		// the connectivity check. Dropping it left the picker showing
		// "loading models…" forever, because the request was never made.
		return m.runOpenrouter(arg)
	default:
		m.addLine(m.theme.failure.Render("Unknown command " + verb + ". Try /help."))
	}
	return nil
}

// runOpenrouter handles /openrouter and its options. No option reports status,
// which is why the verb alone is worth a command.
func (m *model) runOpenrouter(arg string) tea.Cmd {
	settings := m.info.Settings

	// No argument reports status.
	if arg == "" {
		provider, model, credential := settings.Status()
		m.info.Provider, m.info.Model = provider, model
		m.addLine(m.theme.muted.Render("provider  " + orUnknown(provider)))
		m.addLine(m.theme.muted.Render("model     " + orUnknown(model)))
		// Status is required to return an already-masked credential.
		m.addLine(m.theme.muted.Render("credential " + credential))
		m.addLine(m.theme.muted.Render("stored in  " + settings.CredentialPath()))
		return nil
	}

	name, value, _ := strings.Cut(arg, " ")
	name = strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimSpace(value)

	switch name {
	case "api-key", "key":
		m.secret = true
		m.input = nil
		// Naming paste explicitly, because the previous wording did not and
		// the natural thing to do with an API key is paste it.
		m.notice = "paste or type the key · enter saves · esc cancels"
		m.addLine(m.theme.brand.Render("API key"))
	case "clear-key":
		if err := settings.ClearCredential(); err != nil {
			m.addLine(m.theme.failure.Render("could not clear: " + err.Error()))
			return nil
		}
		m.notice = "stored credential removed"
		m.addLine(m.theme.muted.Render("stored credential removed"))
	case "model":
		// With no identifier, open the picker. Demanding a slug that the
		// operator has to know or look up is what made this unusable.
		if value == "" {
			return m.openModels()
		}
		if err := settings.StoreModel(value); err != nil {
			m.addLine(m.theme.failure.Render("could not save model: " + err.Error()))
			return nil
		}
		m.info.Model = value
		m.notice = "model set to " + value
		m.addLine(m.theme.phase.Render("model set to " + value))
		m.addLine(m.theme.muted.Render("checking it answers…"))
		return m.check(value)
	case "path":
		m.addLine(m.theme.muted.Render(settings.CredentialPath()))
	default:
		m.addLine(m.theme.failure.Render("unknown option " + name + ". Try /help."))
	}
	return nil
}

// runConnect is the guided path from nothing configured to a working setup.
//
// It asks for whatever is missing, in order, and stops as soon as both a
// credential and a model exist. The alternative, telling a new user to create a
// JSON file before anything happens, is where most people give up: the failure
// only appears once a file has been written correctly, which nothing checks
// along the way.
func (m *model) runConnect() tea.Cmd {
	_, model, credential := m.info.Settings.Status()

	if credential == "" || strings.HasPrefix(credential, "not set") {
		m.secret = true
		m.input = nil
		// Naming paste matters: the natural gesture with an API key is to
		// paste it, and the previous wording never said that.
		m.notice = "paste or type your API key · enter saves · esc cancels"
		m.addLine(m.theme.brand.Render("Step 1 of 2 — API key"))
		m.addLine(m.theme.muted.Render("  the key is hidden as you type and never shown again"))
		return nil
	}

	if model == "" {
		// Go straight to the picker rather than asking for an identifier.
		m.pendingModel = true
		return m.openModels()
	}

	m.info.Model = model
	m.addLine(m.theme.phase.Render("Checking the connection…"))
	return m.check(m.info.Model)
}

// runTools lists exactly what the model can call right now.
//
// The alternative was answering "it says it has no write tools" by guessing.
// This names the tools that are in play and, when the write ones are absent,
// says which command brings them back.
func (m *model) runTools() {
	writes, commands := m.info.Settings.Grants()
	m.addLine(m.theme.muted.Render("always available"))
	m.addLine(m.theme.muted.Render("  read_file  list_files  search_files  stat_file"))

	if writes || commands {
		m.addLine(m.theme.muted.Render("granted now"))
	} else {
		m.addLine(m.theme.muted.Render("not granted — run /permissions writes on"))
	}
	if writes {
		m.addLine(m.theme.muted.Render("  create_file  write_file  append_file"))
		m.addLine(m.theme.muted.Render("  make_directory  move_path  delete_path"))
	}
	if commands {
		m.addLine(m.theme.muted.Render("  run_command"))
	}
}

// runPermissions shows and changes what the harness may do to the workspace.
//
// This exists because the alternative was invisible: with writes off the model
// correctly refuses to save a file, and the only way to change that was to
// hand-edit a JSON file and restart. Someone asking it to save a file and being
// told it could not had no route forward from where they were sitting.
func (m *model) runPermissions(arg string) tea.Cmd {
	writes, commands := m.info.Settings.Grants()

	fields := strings.Fields(arg)
	if len(fields) > 0 {
		what := strings.ToLower(fields[0])
		value := "on"
		if len(fields) > 1 {
			value = strings.ToLower(fields[1])
		}
		var on bool
		switch value {
		case "on", "true", "yes", "enable", "enabled":
			on = true
		case "off", "false", "no", "disable", "disabled":
			on = false
		default:
			m.addLine(m.theme.failure.Render("usage: /permissions <writes|commands> <on|off>"))
			return nil
		}
		switch what {
		case "writes", "write", "w":
			writes = on
		case "commands", "command", "c":
			commands = on
		case "all":
			writes, commands = on, on
		default:
			m.addLine(m.theme.failure.Render("usage: /permissions <writes|commands|all> <on|off>"))
			return nil
		}
		if err := m.info.Settings.StoreGrants(writes, commands); err != nil {
			m.addLine(m.theme.failure.Render("could not save: " + err.Error()))
			return nil
		}
		m.info.AllowWrites, m.info.AllowCommands = writes, commands
		m.addLine(m.theme.phase.Render("writes " + YesNo(writes) + " · commands " + YesNo(commands)))
		if writes || commands {
			m.addLine(m.theme.muted.Render("  applies immediately; no restart needed"))
			m.addLine(m.theme.muted.Render("  tools stay inside the sandbox and inside this project"))
		}
		return nil
	}

	m.addLine(m.theme.muted.Render("writes    " + YesNo(writes) + "   allow editing files in this project"))
	m.addLine(m.theme.muted.Render("commands  " + YesNo(commands) + "   allow running programs in this project"))
	m.addLine(m.theme.muted.Render("change with /permissions writes on"))
	return nil
}

// runStatus reports what is configured and whether it actually works, so the
// two can be told apart. A file that names a model is not evidence the model
// can be reached.
func (m *model) runStatus() tea.Cmd {
	provider, model, credential := m.info.Settings.Status()
	m.info.Provider, m.info.Model = provider, model
	m.addLine(m.theme.muted.Render("provider  " + orUnknown(provider)))
	m.addLine(m.theme.muted.Render("model     " + orUnknown(model)))
	// Status is required to return an already-masked credential.
	m.addLine(m.theme.muted.Render("credential " + credential))
	m.addLine(m.theme.muted.Render("settings  " + m.info.Settings.CredentialPath()))

	if model == "" {
		m.addLine(m.theme.failure.Render("no model set — run /connect"))
		return nil
	}
	if strings.HasPrefix(credential, "not set") || credential == "" {
		m.addLine(m.theme.failure.Render("no API key stored — run /connect"))
		return nil
	}
	m.addLine(m.theme.phase.Render("Checking the connection…"))
	return m.check(model)
}

// check runs a connectivity probe and reports the outcome.
func (m *model) check(model string) tea.Cmd {
	if m.info.Probe == nil {
		m.addLine(m.theme.muted.Render("connection not verified in this build"))
		return nil
	}
	m.checking = true
	return func() tea.Msg {
		return checked(m.info.Probe.Check(context.Background(), []string{model}))
	}
}

// checked is the result of a connectivity probe arriving back.
type checked []Result

// report renders a probe outcome, distinguishing the cases that have different
// fixes: a rejected key is not the same problem as a model that does not exist.
func (m *model) report(results []Result) {
	// Captured before clearing: the startup check is distinguished from an
	// explicit one by checking being set, and clearing it first made the
	// distinction unreachable.
	wasAutomatic := m.checking && m.quietSuccess
	m.checking = false
	if len(results) == 0 {
		m.lastErr = "nothing to check"
		m.addLine(m.theme.failure.Render("Nothing to check."))
		return
	}
	r := results[0]
	if r.Err == nil {
		m.verified = true
		m.lastErr = ""
		// The startup check stays quiet on success. It exists to catch a
		// remembered setup that has stopped working; announcing that it still
		// works every single start is noise, and the footer already reads
		// "connected".
		if wasAutomatic {
			return
		}
		m.addLine(m.theme.phase.Render("✓ connected — " + r.Model + " is reachable"))
		return
	}
	m.verified = false
	m.lastErr = r.Err.Error()
	m.addLine(m.theme.failure.Render("✗ " + r.Err.Error()))
	switch r.Err.Error() {
	case "no API key is configured":
		m.addLine(m.theme.muted.Render("  run /openrouter api-key, then /connect"))
	case "the API key was rejected":
		m.addLine(m.theme.muted.Render("  the key reached OpenRouter and was refused; check for a typo or a revoked key"))
	case "no such model":
		m.addLine(m.theme.muted.Render("  the key worked but that model does not exist; try /openrouter model openrouter/free"))
	default:
		m.addLine(m.theme.muted.Render("  check the network, then run /status"))
	}
}

// commitSecret stores a typed credential.
//
// The typed value is held only in m.input for the duration of entry. It is
// cleared here, never written to m.lines, and never returned to the prompt
// area, so it cannot reach the transcript that is sent to a model.
func (m *model) commitSecret() (tea.Model, tea.Cmd) {
	key := strings.TrimSpace(string(m.input))
	m.input = nil
	m.secret = false
	if key == "" {
		// Saying why, rather than only that it failed. The previous wording
		// left someone who had pasted a key convinced it had been saved.
		m.notice = "no key entered — the field was empty"
		m.addLine(m.theme.failure.Render("No key entered — nothing was saved."))
		m.addLine(m.theme.muted.Render("  paste the key again, or press esc to cancel"))
		return m, nil
	}
	if err := m.info.Settings.StoreCredential(key); err != nil {
		m.addLine(m.theme.failure.Render("could not store key: " + err.Error()))
		m.notice = "key not stored"
		return m, nil
	}
	_, _, credential := m.info.Settings.Status()
	m.notice = "credential saved · " + credential
	// Unmistakable, and states what to do next. A quiet confirmation here is
	// indistinguishable from the interface having ignored the input.
	m.addLine(m.theme.phase.Render("✓ API key saved (" + credential + ")"))
	m.addLine(m.theme.muted.Render("  stored at " + m.info.Settings.CredentialPath()))
	// Partway through /connect, saving the key moves on to the model rather
	// than dropping the operator back at a prompt that does not say what to
	// do next.
	if m.pendingModel {
		m.addLine(m.theme.brand.Render("Step 2 of 2 — model"))
		m.addLine(m.theme.muted.Render("  picking from the models your account can use"))
		return m, m.openModels()
	}
	return m, nil
}

// menuRow is the screen row where the first suggestion is drawn.
//
// The layout is fixed from the bottom: hint, footer, then any menu rows, then
// the editor. Counting up from the bottom is what keeps a click landing on the
// right line as the status line and the menu grow and shrink, which a running
// total would get wrong the moment the run starts.
func (m *model) menuRow(height, bottomRows int) int {
	// hint + footer
	offset := height - bottomRows + 2
	if m.running {
		offset++ // the status line above the editor
	}
	return offset + boxRows
}

// maxModelRows bounds how much vertical space the picker takes. The list is
// scrollable by filtering, so showing every entry would push the prompt off a
// short terminal and scroll the highlight out of sight.
const maxModelRows = 8

// modelRows renders the picker, scrolling the highlight into view.
//
// It draws nothing once the picker is closed. Closing clears the list, but a
// resize arriving in the same frame as the close would otherwise still render
// the empty-list message underneath the prompt.
func (m *model) modelRows(width int) []string {
	if !m.picking {
		return nil
	}
	var out []string
	if m.loading {
		out = append(out, m.theme.muted.Render("  loading models…"))
	}
	if m.listErr != "" {
		out = append(out, m.theme.failure.Render("  "+m.listErr))
		out = append(out, m.theme.muted.Render("  type a model id instead, or /help"))
		return out
	}

	// The filter typed so far is shown in the editor, so the rows need only
	// carry the choices.
	start := 0
	if m.pick >= maxModelRows {
		start = m.pick - maxModelRows + 1
	}
	for i := start; i < len(m.found) && len(out) < maxModelRows; i++ {
		out = append(out, m.modelRow(m.found[i], i == m.pick, width))
	}
	if len(out) == 0 && !m.loading {
		out = append(out, m.theme.muted.Render("  no match — backspace to widen the search"))
	}
	return out
}

// modelRow renders one model with enough detail to choose on price and size
// rather than on the identifier alone.
func (m *model) modelRow(entry Model, active bool, width int) string {
	badge := " "
	if entry.Free {
		badge = "F"
	}
	price := entry.Price
	if price == "" {
		price = "—"
	}
	// The identifier is the value that matters, so it gets the space left over
	// rather than being truncated behind the metadata.
	id := entry.ID
	meta := badge + " " + price
	budget := width - lipgloss.Width(meta) - 6
	if budget < 8 {
		budget = 8
	}

	line := "  " + elide(id, budget)
	if lipgloss.Width(line)+lipgloss.Width(meta)+2 <= width {
		line = padTo(line, width-lipgloss.Width(meta)-2) + m.theme.muted.Render(meta)
	}
	if active {
		return m.theme.prompt.Render("› " + strings.TrimPrefix(line, "  "))
	}
	return line
}

// padTo right-pads plain text to a column, ignoring escape sequences.
func padTo(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// adviceFor returns the remedy an error carries, or an empty string.
//
// It is a method assertion so the interface can act on a provider's guidance
// without importing any provider. An error that offers no advice simply has no
// Advice method, and is reported on its own terms.
func adviceFor(err error) string {
	if err == nil {
		return ""
	}
	type advised interface{ Advice() string }
	var a advised
	if errors.As(err, &a) {
		return a.Advice()
	}
	return ""
}

// picked is a suggestion row chosen with the mouse.
type picked struct{ command string }

// scrolled reports that the wheel moved the transcript, so the frame redraws.
// The state is already on the model; this exists only to make the update
// loopable through a command.
type scrolled int

// modelPicked is a model row chosen with the mouse.
type modelPicked struct{ index int }

// listed is a model list arriving back from the browser.
type listed struct {
	models []Model
	err    error
}

// openModels starts the model picker.
//
// Reaching this required a model identifier nobody could be expected to
// remember. The only previous route was typing a slug copied from a web page,
// which fails silently when it is wrong and gives nothing to choose between.
func (m *model) openModels() tea.Cmd {
	if m.info.Browser == nil {
		m.addLine(m.theme.failure.Render("this build cannot list models; type /openrouter model <id>"))
		return nil
	}
	m.picking = true
	m.query, m.pick, m.listErr = nil, 0, ""
	m.loading = true
	m.pendingFilter = nil
	m.addLine(m.theme.brand.Render("Choose a model"))
	m.addLine(m.theme.muted.Render("  type to filter · up/down or click · enter to select · esc to cancel"))
	return m.fetchModels("")
}

// fetchModels asks the browser for the models, optionally filtered.
func (m *model) fetchModels(query string) tea.Cmd {
	return func() tea.Msg {
		var models []Model
		var err error
		if query == "" {
			models, err = m.info.Browser.List(context.Background())
		} else {
			models, err = m.info.Browser.Search(context.Background(), query)
		}
		return listed{models: models, err: err}
	}
}

// filterModels re-queries the list as text is typed.
func (m *model) filterModels(query string) tea.Cmd {
	m.pendingFilter = []rune(query)
	m.loading = true
	return m.fetchModels(query)
}

// closeModels leaves the picker, discarding whatever was half typed.
func (m *model) closeModels() {
	m.picking = false
	m.query, m.found, m.pick, m.loading, m.listErr = nil, nil, 0, false, ""
	m.notice = ""
	m.syncMenu()
}

// moveModel steps the highlight through the list.
func (m *model) moveModel(delta int) {
	if len(m.found) == 0 {
		return
	}
	m.pick = (m.pick + delta + len(m.found)) % len(m.found)
}

// acceptModel stores the highlighted model and checks it answers.
func (m *model) acceptModel() tea.Cmd {
	if m.pick < 0 || m.pick >= len(m.found) {
		return nil
	}
	id := m.found[m.pick].ID
	m.closeModels()
	if err := m.info.Settings.StoreModel(id); err != nil {
		m.addLine(m.theme.failure.Render("could not save model: " + err.Error()))
		return nil
	}
	m.info.Model = id
	m.notice = "model set to " + id
	m.addLine(m.theme.phase.Render("model set to " + id))
	m.addLine(m.theme.muted.Render("checking it answers…"))
	return m.check(id)
}

// applyModelList stores a list result, keeping the highlight in range.
func (m *model) applyModelList(result listed) {
	m.loading = false
	if result.err != nil {
		m.listErr = result.err.Error()
		return
	}
	m.listErr = ""
	m.found = result.models
	m.modelsAt = len(m.found)
	if m.pick >= len(m.found) {
		m.pick = 0
	}
	// An empty result is only worth announcing when a filter was applied. With
	// no filter it means the account has nothing to offer, which the picker
	// already shows as an empty list.
	if len(m.found) == 0 && len(m.query) > 0 {
		m.notice = "no model matches that"
	}
}

// modelRowOffset is the screen row of the picker's first row.
//
// The picker sits between the editor and the footer, so it is one row lower
// than the command menu, which has no footer gap of its own.
func (m *model) modelRowOffset(height, bottomRows int) int {
	return m.menuRow(height, bottomRows) + 2
}

// SuggestedFor is the package-level accessor other packages use to obtain the
// same remedy, so the one-shot path and the interface stay consistent.
func SuggestedFor(err error) string { return adviceFor(err) }

func orUnknown(value string) string {
	if value == "" {
		return "not set"
	}
	return value
}

const commandHelp = "  /connect                    set up a key and model, then verify" +
	"\n  /status                     show settings and check the connection" +
	"\n  /openrouter                 show provider, model, credential, keyfile path" +
	"\n  /openrouter api-key        store a credential (paste or type; hidden)" +
	"\n  /openrouter model          pick a model from your account" +
	"\n  /openrouter model <id>     set a model by identifier" +
	"\n  /openrouter clear-key      remove the stored credential" +
	"\n  /openrouter path           print the keyfile path" +
	"\n  /models                    pick a model from your account" +
	"\n  /permissions               show or change what may be edited or run" +
	"\n  /tools                    list what the model can call right now" +
	"\n  /help                      list these commands" +
	"\n\n  Type / to choose a command. up/down or click selects, enter runs."

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
	transcript := append(head, visible...)
	for _, line := range transcript {
		// The budget is spent from the end, so a reply taller than the
		// transcript area is trimmed rather than allowed to push the prompt
		// off the bottom of the terminal.
		remaining := height - bottomRows - rows
		if remaining < 1 {
			break
		}
		wrapped := clipRows(line, width, remaining)
		if wrapped == "" {
			break
		}
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
	// The wheel scrolls the transcript, which is the one gesture that must work
	// for a window with no scrollbar.
	if !m.picking {
		view.MouseMode = tea.MouseModeCellMotion
		view.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
			wheel, ok := msg.(tea.MouseWheelMsg)
			if !ok {
				return nil
			}
			switch wheel.Button {
			case tea.MouseWheelUp:
				m.scrollBy(3)
			case tea.MouseWheelDown:
				m.scrollBy(-3)
			default:
				return nil
			}
			return func() tea.Msg { return scrolled(m.scroll) }
		}
	}
	// Clicking a row acts on it. Mouse reporting is enabled only while a list
	// is on screen: asking for mouse events the whole time would steal clicks
	// and drags from the terminal's own scrollback for the entire session, in
	// exchange for a list that is usually not there.
	switch {
	case m.picking:
		view.MouseMode = tea.MouseModeCellMotion
		first := m.modelRowOffset(height, bottomRows)
		view.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
			click, ok := msg.(tea.MouseClickMsg)
			if !ok || click.Button != tea.MouseLeft {
				return nil
			}
			index := click.Y - first
			if index < 0 {
				return nil
			}
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
	case len(m.menu.rows) > 0:
		view.MouseMode = tea.MouseModeCellMotion
		firstRow := m.menuRow(height, bottomRows)
		view.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
			click, ok := msg.(tea.MouseClickMsg)
			if !ok || click.Button != tea.MouseLeft {
				return nil
			}
			index := click.Y - firstRow
			if index < 0 || index >= len(m.menu.rows) {
				return nil
			}
			command := m.menu.rows[index]
			return func() tea.Msg { return picked{command} }
		}
	}
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

// boxRows is the total height of the bordered editor: a top border, one
// content row, and a bottom border. The suggestion list begins directly after
// this, which is what lets a click be mapped to a row without searching the
// rendered text.
const boxRows = 3

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
	"/connect",
	"/status",
	"/openrouter",
	"/openrouter api-key",
	"/openrouter model",
	"/openrouter clear-key",
	"/openrouter path",
	"/models",
	"/permissions",
	"/tools",
	"/help",
}

// matches returns the commands that the current input could complete to.
//
// It returns nothing for text that is not a command, and nothing once the
// operator has typed a space, because by then they are filling in an argument
// rather than choosing a command.
func (m *model) matches() []string {
	if m.secret {
		return nil
	}
	typed := string(m.input)
	if !strings.HasPrefix(typed, "/") || strings.Contains(typed, " ") {
		return nil
	}
	var rows []string
	for _, c := range commandList {
		if strings.HasPrefix(c, typed) {
			rows = append(rows, c)
		}
		if len(rows) == maxSuggestions {
			break
		}
	}
	return rows
}

// syncMenu refreshes the highlighted row after the input changes.
//
// The highlight is clamped rather than reset to the top on every keystroke, so
// holding down an arrow key and then typing a character does not throw the
// selection back to the first row.
func (m *model) syncMenu() {
	m.menu.rows = m.matches()
	if len(m.menu.rows) == 0 {
		m.menu.chosen = -1
		return
	}
	if m.menu.chosen >= len(m.menu.rows) {
		m.menu.chosen = len(m.menu.rows) - 1
	}
	if m.menu.chosen < -1 {
		m.menu.chosen = -1
	}
}

// moveMenu steps the highlight. It wraps, because a list this short is easiest
// to scan by cycling rather than by stopping at the end.
func (m *model) moveMenu(delta int) bool {
	rows := m.menu.rows
	if len(rows) == 0 {
		return false
	}
	m.menu.chosen = (m.menu.chosen + delta + len(rows)) % len(rows)
	if m.menu.chosen < 0 {
		m.menu.chosen += len(rows)
	}
	return true
}

// chosen returns the highlighted command, if any.
func (m *model) chosen() string {
	if m.menu.chosen < 0 || m.menu.chosen >= len(m.menu.rows) {
		return ""
	}
	return m.menu.rows[m.menu.chosen]
}

// suggestions renders the menu, marking the highlighted row.
//
// Nothing is drawn while the model picker is open. The command menu and the
// model list occupy the same space, and showing both would mean typing a
// character during model selection also filtered slash commands.
func (m *model) suggestions() []string {
	if m.picking {
		return nil
	}
	rows := m.menu.rows
	if len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for i, row := range rows {
		switch {
		case i == m.menu.chosen:
			out = append(out, m.theme.prompt.Render("› "+row))
		default:
			out = append(out, m.theme.muted.Render("  "+row))
		}
	}
	return out
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
	if m.picking {
		for _, row := range m.modelRows(width) {
			b.WriteString(row)
			b.WriteByte('\n')
		}
	}
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
	case m.picking:
		hint = "type to filter · up/down or click selects · enter uses it · esc cancels"
	case m.secret:
		hint = "enter save · esc cancel · input is not shown"
	case strings.HasPrefix(string(m.input), "/"):
		hint = "up/down or click selects · enter runs · tab completes"
	case !m.running:
		hint = "enter send · up/down scroll · ctrl+c quit · /help"
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
// tail returns the window of transcript lines currently visible.
//
// scroll is the number of lines scrolled back from the bottom; zero is the
// live view, which is where the transcript stays by default. Scrolling exists
// because a fixed window with no way back makes an answer that has left the
// screen unreachable, which is not the same as keeping it out of the record.
func (m *model) tail(budget, width int) []string {
	heights := make([]int, len(m.lines))
	for i, line := range m.lines {
		heights[i] = strings.Count(clip(line, width), "\n") + 1
	}

	// The live window: walk up from the bottom until the budget is spent.
	first, last, rows := len(m.lines), len(m.lines), 0
	for i := len(m.lines) - 1; i >= 0; i-- {
		if rows+heights[i] > budget {
			break
		}
		rows += heights[i]
		first = i
	}
	last = len(m.lines)

	// Nothing fit, which happens whenever the newest line is taller than the
	// window. Returning an empty slice blanked the whole transcript, so a code
	// block left the screen empty while a one-line answer rendered fine. The
	// line is returned anyway and trimmed to the budget by the caller.
	if first == len(m.lines) && len(m.lines) > 0 {
		first = len(m.lines) - 1
	}

	// Scrolling back moves the window towards older lines and, to keep the size
	// the same, drops the same number from the newest end. Trimming from the
	// front instead would cancel the scroll out exactly, which is what an
	// earlier version did and why scrolling appeared to do nothing.
	for n := m.scroll; n > 0 && first > 0; n-- {
		rows += heights[first-1]
		first--
		if last > first {
			rows -= heights[last-1]
			last--
		}
	}

	// The budget can still be exceeded when a single line is taller than the
	// window. Give up lines from the newest end until it fits.
	for rows > budget && last > first {
		rows -= heights[last-1]
		last--
	}
	return m.lines[first:last]
}

// maxScroll is the furthest back the transcript can be taken, bounded by the
// number of retained lines so a runaway scroll cannot wrap or panic.
func (m *model) maxScroll() int {
	if m.scroll > len(m.lines) {
		return len(m.lines)
	}
	return m.scroll
}

// scrollBy moves the view back (positive) or forward (negative).
func (m *model) scrollBy(delta int) {
	m.scroll += delta
	if m.scroll < 0 {
		m.scroll = 0
	}
	if m.scroll > len(m.lines) {
		m.scroll = len(m.lines)
	}
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
	line := spinnerFrames[m.frame%len(spinnerFrames)] + " " + m.theme.phase.Render(phase+elapsed+queued)
	// While scrolled back, the elapsed timer and the spinner are describing
	// something off screen. Saying how far back the view is, and how to
	// return, is more use than a clock nobody is looking at.
	if back := m.maxScroll(); back > 0 {
		return m.theme.muted.Render(fmt.Sprintf("↑ scrolled back %d line%s · end returns", back, plural(back)))
	}
	return line
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// inputBox draws the bordered editor that holds the prompt. The border is
// structural, not decorative: it shows exactly how much room the editor has.
func (m *model) inputBox(width int) string {
	body := m.theme.prompt.Render("›") + " "
	switch {
	case m.picking:
		// The filter is the prompt in this mode, so the editor has to show it.
		if len(m.query) == 0 {
			body += m.theme.muted.Render("filter models…")
		} else {
			body += safe(string(m.query))
		}
	case m.secret:
		// The placeholder is deliberately not a row of bullets. A fixed mask
		// reads as "something has already been entered", so an operator who
		// had not pasted anything yet could not tell the field was empty, and
		// saving it produced "no key entered" for reasons invisible on screen.
		if len(m.input) == 0 {
			body += m.theme.muted.Render("paste your API key — nothing shown")
		} else {
			// Bullets grow with what was typed. The count reveals length, which
			// is acceptable for a field where the operator is the only party
			// and it is the only feedback they get.
			body += m.theme.muted.Render(strings.Repeat("•", len(m.input)))
		}
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
	budget := width - lipgloss.Width(provider) - lipgloss.Width(mode) - lipgloss.Width(m.connection()) - 6
	return m.theme.muted.Render(provider) + m.theme.muted.Render(" · ") +
		m.theme.brand.Render(elide(model, budget)) + m.theme.muted.Render(" · ") +
		m.connection() + m.theme.muted.Render(" · ") +
		m.theme.muted.Render(mode)
}

// connection states whether the model in force has been shown to answer.
//
// It distinguishes "configured" from "working" because those are the two
// things people conflate: a model in the settings file looks like a working
// setup right up until the first request fails. An unverified connection is
// labelled as such rather than left to look healthy.
func (m *model) connection() string {
	switch {
	case m.checking:
		return m.theme.muted.Render("checking…")
	case m.verified:
		return m.theme.phase.Render("connected")
	case m.lastErr != "":
		return m.theme.failure.Render("not connected")
	case m.info.Model == "":
		return m.theme.muted.Render("unconfigured")
	default:
		return m.theme.muted.Render("unverified")
	}
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

// clipRows trims wrapped text to at most maxRows, keeping the end.
//
// The end, not the start: the newest rows of a long answer are the ones being
// read, and a code block that does not fit should show its closing lines
// rather than its opening ones with the tail pushed off the screen. This also
// keeps the frame a known height, which is what stops a long reply from
// shoving the prompt off the bottom.
func clipRows(text string, width, maxRows int) string {
	if maxRows < 1 {
		return ""
	}
	rows := strings.Split(clip(text, width), "\n")
	if len(rows) <= maxRows {
		return strings.Join(rows, "\n")
	}
	return strings.Join(rows[len(rows)-maxRows:], "\n")
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
