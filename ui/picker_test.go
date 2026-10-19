package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// fakeBrowser serves a fixed catalogue.
type fakeBrowser struct {
	models  []Model
	err     error
	queries []string
}

func (f *fakeBrowser) List(context.Context) ([]Model, error) {
	f.queries = append(f.queries, "")
	return f.models, f.err
}

func (f *fakeBrowser) Search(_ context.Context, q string) ([]Model, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	var out []Model
	for _, m := range f.models {
		if strings.Contains(strings.ToLower(m.ID+" "+m.Name), strings.ToLower(q)) {
			out = append(out, m)
		}
	}
	return out, nil
}

func pickerModel(s Settings, b Browser) *model {
	m := newCmdModel(s)
	m.info.Browser = b
	return m
}

func catalogue() []Model {
	return []Model{
		{ID: "openai/gpt-4o-mini", Name: "GPT-4o mini", Price: "$0.15/M", Context: 128000},
		{ID: "anthropic/claude-sonnet-4.5", Name: "Claude Sonnet", Price: "$3.00/M", Context: 200000},
		{ID: "cohere/north-mini-code:free", Name: "North mini code", Price: "free", Context: 256000, Free: true},
	}
}

// THE BUG THIS FILE EXISTS FOR.
//
// Bubble Tea reports a paste as its own message, and the update loop only
// listened for key presses. Every paste was dropped on the floor with no
// error, so a credential pasted from a password manager simply never arrived
// and saving it reported "no key entered". This asserts the pasted text
// actually reaches the editor.
func TestPastedTextReachesTheEditor(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.Update(tea.PasteMsg{Content: "/status"})
	if got := string(m.input); got != "/status" {
		t.Fatalf("paste produced %q, want /status", got)
	}
	// And it must be usable, not merely present.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.queue) != 0 {
		t.Fatalf("pasted command was queued as a prompt: %v", m.queue)
	}
}

// The case that actually broke: a credential pasted from a browser or password
// manager, where nothing is ever typed.
func TestPastedCredentialIsStored(t *testing.T) {
	const secret = "sk-or-v1-PASTED9999"
	s := &fakeSettings{}
	m := newCmdModel(s)

	typeText(m, "/openrouter api-key")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.PasteMsg{Content: secret})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if s.stored != secret {
		t.Fatalf("pasted credential was not stored: %q", s.stored)
	}
	if strings.Contains(joined(m), secret) {
		t.Fatalf("pasted credential entered the transcript:\n%s", joined(m))
	}
}

// A pasted credential often arrives with surrounding whitespace or as a
// multi-line copy. Neither should corrupt the stored key.
func TestPastedCredentialIsTrimmed(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	typeText(m, "/openrouter api-key")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.PasteMsg{Content: "  sk-or-v1-TRIMMED1234\n"})
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})

	if s.stored != "sk-or-v1-TRIMMED1234" {
		t.Fatalf("stored %q, want the trimmed key", s.stored)
	}
}

// Multi-line paste into a single-line editor would otherwise inject newlines
// into a prompt that cannot represent them.
func TestMultiLinePasteKeepsOnlyTheFirstLine(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.Update(tea.PasteMsg{Content: "first\nsecond\nthird"})
	if got := string(m.input); got != "first" {
		t.Fatalf("paste produced %q, want only the first line", got)
	}
}

// Paste must not bypass the input cap that typing respects.
func TestPasteRespectsTheInputLimit(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.Update(tea.PasteMsg{Content: strings.Repeat("x", inputLimit+500)})
	if len(m.input) != inputLimit {
		t.Fatalf("input length = %d, want the cap %d", len(m.input), inputLimit)
	}
}

// An empty or whitespace-only paste must do nothing, not clear the editor.
func TestEmptyPasteChangesNothing(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	typeText(m, "existing")
	m.Update(tea.PasteMsg{Content: "\n\n"})
	if got := string(m.input); got != "existing" {
		t.Fatalf("empty paste changed the input to %q", got)
	}
}

// The empty secret field showed a fixed row of bullets, which read as
// "something is already entered". Saving it then said "no key entered" for
// reasons invisible on screen.
func TestEmptySecretFieldDoesNotLookFilled(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.secret = true
	empty := m.View().Content
	if strings.Contains(empty, "••") {
		t.Fatalf("an empty secret field renders as filled:\n%s", empty)
	}
	if !strings.Contains(empty, "paste your API key") {
		t.Fatalf("the empty secret field gives no instruction:\n%s", empty)
	}

	// With text typed, there should be visible feedback.
	m.input = []rune("sk-or-v1")
	filled := m.View().Content
	if !strings.Contains(filled, "••") {
		t.Fatalf("a filled secret field shows no feedback:\n%s", filled)
	}
}

// Saving nothing must say why, because "no key entered" left someone who had
// pasted a key convinced it had been saved.
func TestEmptyCredentialExplainsItself(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	typeText(m, "/openrouter api-key")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})

	if s.stored != "" {
		t.Fatal("an empty credential was stored")
	}
	if !strings.Contains(joined(m), "nothing was saved") {
		t.Fatalf("the failure does not say nothing was saved:\n%s", joined(m))
	}
	if !strings.Contains(joined(m), "paste the key again") {
		t.Fatalf("the failure gives no next step:\n%s", joined(m))
	}
}

// A saved credential must confirm unmistakably, and say where it went.
func TestSavedCredentialConfirmsAndSaysWhere(t *testing.T) {
	s := &fakeSettings{}
	m := newCmdModel(s)
	typeText(m, "/openrouter api-key")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeText(m, "sk-or-v1-KEY")
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})

	text := joined(m)
	if !strings.Contains(text, "API key saved") {
		t.Fatalf("no confirmation:\n%s", text)
	}
	if !strings.Contains(text, "openrouter.key") {
		t.Fatalf("the confirmation does not say where the key went:\n%s", text)
	}
}

// The whole point of the picker: choosing a model means picking from a list.
func TestModelCommandOpensThePicker(t *testing.T) {
	s := &fakeSettings{}
	m := pickerModel(s, &fakeBrowser{models: catalogue()})

	// Driven the way a person does it: type the command, press enter, and
	// run whatever the runtime would have run. Calling runOpenrouter directly
	// is what let a dropped command pass every earlier version of this test.
	typeText(m, "/openrouter model")
	_, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/openrouter model returned no command, so the list can never load")
	}
	if !m.picking {
		t.Fatal("the picker is not open")
	}

	// Drain the chain: one command, one list.
	msg := cmd()
	if _, ok := msg.(listed); !ok {
		t.Fatalf("first message = %T, want a listed result", msg)
	}
	m.Update(msg)
	if len(m.found) != 3 {
		t.Fatalf("found %d models, want 3", len(m.found))
	}
	if m.loading {
		t.Fatal("the picker is still loading after the list arrived")
	}
}

// Every command that starts work must return it. A dropped command strands the
// interface on a loading message with no way forward except escape.
func TestCommandsThatStartWorkReturnTheirCommand(t *testing.T) {
	for _, command := range []string{"/models", "/openrouter model", "/openrouter model some/model"} {
		s := &fakeSettings{model: "a/b", stored: "sk-or-v1-KEY"}
		m := pickerModel(s, &fakeBrowser{models: catalogue()})
		m.info.Probe = &fakeProbe{}

		typeText(m, command)
		_, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Errorf("%q returned no command; anything it starts would never run", command)
		}
	}
}

// The same, via the menu, where a highlighted row is accepted instead of the
// typed text.
func TestMenuSelectionAlsoReturnsItsCommand(t *testing.T) {
	m := pickerModel(&fakeSettings{}, &fakeBrowser{models: catalogue()})
	typeText(m, "/models")
	m.syncMenu()
	m.moveMenu(1) // /openrouter

	_, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("accepting a highlighted command dropped its work")
	}
}

func TestPickerFilterNarrowsTheList(t *testing.T) {
	b := &fakeBrowser{models: catalogue()}
	m := pickerModel(&fakeSettings{}, b)
	m.picking = true

	// Typing returns the fetch as a command, and the runtime is what runs it.
	// Running it here is what puts results in m.found.
	runAll(m, "claude")
	if len(m.found) != 1 {
		t.Fatalf("filtering to claude found %v", m.found)
	}
	if m.found[0].ID != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("wrong match: %s", m.found[0].ID)
	}
}

// runAll types text and executes the fetch command each keystroke returns.
func runAll(m *model, text string) {
	for _, r := range text {
		_, cmd := m.key(tea.KeyPressMsg{Code: r, Text: string(r)})
		if cmd != nil {
			m.Update(cmd())
		}
	}
}

// Backspace deletes exactly one character and re-queries with what is left,
// and taken all the way back it restores the full list.
//
// It used to re-append the previous filter to the shortened query, so the
// visible query grew instead of shrinking, and it returned no command, so the
// list never moved. The old test hid both by fetching the full list by hand
// after the backspaces instead of running what the handler returned.
func TestPickerBackspaceRefilters(t *testing.T) {
	m := pickerModel(&fakeSettings{}, &fakeBrowser{models: catalogue()})
	m.picking = true
	runAll(m, "claude")
	if got := string(m.query); got != "claude" {
		t.Fatalf("query after typing = %q, want claude", got)
	}
	if len(m.found) != 1 {
		t.Fatalf("filtering to claude found %v", m.found)
	}

	// One press: one character gone, and the list re-queried through the
	// command the handler returns.
	_, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if cmd == nil {
		t.Fatal("backspace returned no command, so the list never re-queries")
	}
	m.Update(cmd())
	if got := string(m.query); got != "claud" {
		t.Fatalf("query after one backspace = %q, want claud", got)
	}
	if len(m.found) != 1 || m.found[0].ID != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("the list was not refiltered: %v", m.found)
	}

	// Delete the rest. A no-match query must not be mistaken for the end.
	runAll(m, "zzz-nothing")
	if len(m.found) != 0 {
		t.Fatalf("expected no matches, got %v", m.found)
	}
	for len(m.query) > 0 {
		_, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
		if cmd != nil {
			m.Update(cmd())
		}
	}
	if len(m.found) != 3 {
		t.Fatalf("clearing the filter did not restore the list: %v", m.found)
	}
}

// A model identifier pasted into the picker becomes the filter, and the editor
// shows it: in this mode the editor is the filter, so an empty-looking prompt
// above a one-row list would be a lie.
func TestPickerPasteBecomesTheVisibleFilter(t *testing.T) {
	m := pickerModel(&fakeSettings{}, &fakeBrowser{models: catalogue()})
	m.picking = true
	_, cmd := m.Update(tea.PasteMsg{Content: "anthropic/claude-sonnet-4.5"})
	if cmd == nil {
		t.Fatal("pasting a model id started no query")
	}
	m.Update(cmd())
	if got := string(m.query); got != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("query = %q, want the pasted id", got)
	}
	if !strings.Contains(m.inputBox(80), "anthropic/claude") {
		t.Fatalf("the editor does not show the filter:\n%s", m.inputBox(80))
	}
	if len(m.found) != 1 {
		t.Fatalf("pasting an id did not narrow the list: %v", m.found)
	}
}

// TestPickerArrowsAndEnterSelect drives the picker the way a person would.
func TestPickerArrowsAndEnterSelect(t *testing.T) {
	s := &fakeSettings{}
	m := pickerModel(s, &fakeBrowser{models: catalogue()})
	m.picking = true
	m.Update(m.fetchModels("")())

	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.pick != 1 {
		t.Fatalf("down moved to %d", m.pick)
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.pick != 0 {
		t.Fatalf("up moved to %d", m.pick)
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	want := m.found[m.pick].ID

	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s.model != want {
		t.Fatalf("model = %q, want %q", s.model, want)
	}
	if m.picking {
		t.Fatal("the picker stayed open after selecting")
	}
	if m.info.Model != want {
		t.Fatalf("the footer model was not refreshed: %q", m.info.Model)
	}
}

// Escape must leave the picker without changing anything.
func TestPickerEscapeCancelsCleanly(t *testing.T) {
	s := &fakeSettings{model: "original/model"}
	m := pickerModel(s, &fakeBrowser{models: catalogue()})
	m.picking = true
	m.Update(m.fetchModels("")())
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})

	m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.picking {
		t.Fatal("escape did not close the picker")
	}
	if s.model != "original/model" {
		t.Fatalf("escape changed the model to %q", s.model)
	}
	if len(m.modelRows(80)) != 0 {
		t.Fatal("picker rows are still drawn after escape")
	}
}

// The picker is only offered when it can work. Saying so is better than an
// empty screen or a panic.
func TestPickerWithoutABrowserExplainsItself(t *testing.T) {
	m := pickerModel(&fakeSettings{}, nil)
	m.runOpenrouter("model")
	if m.picking {
		t.Fatal("the picker opened with no browser")
	}
	if !strings.Contains(joined(m), "/openrouter model <id>") {
		t.Fatalf("no fallback is offered:\n%s", joined(m))
	}
}

// A catalogue request that fails must be reported, not shown as "no models".
func TestPickerReportsAListingFailure(t *testing.T) {
	m := pickerModel(&fakeSettings{}, &fakeBrowser{err: errors.New("no API key is configured")})
	m.picking = true
	m.Update(m.fetchModels("")())
	if m.listErr == "" {
		t.Fatal("a listing failure was not reported")
	}
	if !strings.Contains(strings.Join(m.modelRows(80), "\n"), "no API key") {
		t.Fatalf("the failure is not shown:\n%v", m.modelRows(80))
	}
}

// The free tier has to be visible, or someone trying this out sees only paid
// models and assumes it costs money.
func TestPickerMarksFreeModels(t *testing.T) {
	m := pickerModel(&fakeSettings{}, &fakeBrowser{models: catalogue()})
	m.picking = true
	m.Update(m.fetchModels("")())

	rendered := strings.Join(m.modelRows(120), "\n")
	if !strings.Contains(rendered, "F") {
		t.Fatalf("free models are not marked:\n%s", rendered)
	}
}

// Selecting a model checks it answers, rather than claiming success silently.
func TestSelectingAModelChecksItAnswers(t *testing.T) {
	s := &fakeSettings{}
	b := &fakeBrowser{models: catalogue()}
	m := pickerModel(s, b)
	m.info.Probe = &fakeProbe{}
	m.picking = true
	m.Update(m.fetchModels("")())

	cmd := m.acceptModel()
	if cmd == nil {
		t.Fatal("selecting a model started no check")
	}
	if _, ok := cmd().(checked); !ok {
		t.Fatalf("got %T, want a check", cmd())
	}
}

// The picker must never swallow the command menu, or typing a slash would show
// the wrong list.
func TestPickerSuppressesTheCommandMenu(t *testing.T) {
	m := pickerModel(&fakeSettings{}, &fakeBrowser{models: catalogue()})
	m.input = []rune("/")
	m.syncMenu()
	m.picking = true
	if len(m.suggestions()) != 0 {
		t.Fatal("the command menu shows through the picker")
	}
}

// A long identifier must be shortened rather than pushed the price off screen.
func TestModelRowElidesLongIdentifiers(t *testing.T) {
	m := pickerModel(&fakeSettings{}, &fakeBrowser{})
	long := "some-provider/" + strings.Repeat("very-long-model-name-", 6)
	row := m.modelRow(Model{ID: long, Price: "$3.00/M"}, false, 60)
	if lipglossWidth(row) > 60 {
		t.Fatalf("row is %d wide, over the 60 column budget: %q", lipglossWidth(row), row)
	}
	if !strings.Contains(row, "…") {
		t.Fatalf("a long identifier was not elided: %q", row)
	}
}

// lipglossWidth measures rendered width, ignoring escape sequences.
func lipglossWidth(s string) int { return lipgloss.Width(s) }
