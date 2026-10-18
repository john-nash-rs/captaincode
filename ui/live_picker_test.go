package ui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/john-nash-rs/captaincode/provider"
)

// liveKeyPattern reads the api-key="…" form a key file is stored in. Sending
// the whole file as the credential would fail as "no key".
var liveKeyPattern = regexp.MustCompile(`(?i)api[-_]?key\s*=\s*"?([^"\s]+)"?`)

type liveBrowser struct{ adapter *provider.OpenRouter }

func (l liveBrowser) List(ctx context.Context) ([]Model, error) {
	models, err := l.adapter.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(models))
	for _, m := range models {
		out = append(out, Model{ID: m.ID, Name: m.Name, Price: m.PriceIn, Free: m.Free})
	}
	return out, nil
}

func (l liveBrowser) Search(ctx context.Context, q string) ([]Model, error) {
	models, err := l.adapter.SearchModels(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(models))
	for _, m := range models {
		out = append(out, Model{ID: m.ID, Name: m.Name, Price: m.PriceIn, Free: m.Free})
	}
	return out, nil
}

// Drives the real key path against the real catalogue. Skipped without a key.
//
// This lives in the interface package because the bug it guards was in how a
// keystroke dispatches work, which no test outside this package can reach.
func TestLivePickerLoadsThroughTheKeyPath(t *testing.T) {
	key := os.Getenv("CAPTAINCODE_EVAL_KEY")
	if file := os.Getenv("CAPTAINCODE_EVAL_KEY_FILE"); key == "" && file != "" {
		if data, err := os.ReadFile(file); err == nil {
			if match := liveKeyPattern.FindSubmatch(data); match != nil {
				key = string(match[1])
			}
		}
	}
	if key == "" {
		t.Skip("set CAPTAINCODE_EVAL_KEY or CAPTAINCODE_EVAL_KEY_FILE")
	}

	adapter := provider.New(key, "", "")
	m := newCmdModel(&fakeSettings{})
	m.width, m.height = 90, 26
	m.info.Browser = liveBrowser{adapter: adapter}
	m.info.Probe = &fakeProbe{}

	// Exactly what a person types.
	typeText(m, "/models")
	_, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no command returned")
	}
	m.Update(cmd())

	if m.loading {
		t.Fatal("STUCK: still loading after the command ran")
	}
	if m.listErr != "" {
		t.Fatalf("listing failed: %s", m.listErr)
	}
	if len(m.found) == 0 {
		t.Fatal("STUCK: no models arrived")
	}
	fmt.Fprintf(os.Stderr, "\n=== live picker, %d models ===\n%s\n", len(m.found), m.View().Content)

	// Filter through the real path too.
	_, filterCmd := m.key(tea.KeyPressMsg{Code: 'g', Text: "g"})
	m.Update(filterCmd())
	if len(m.found) == 0 || len(m.found) == len(catalogue()) {
		t.Fatalf("filter did not narrow: %d of %d", len(m.found), len(catalogue()))
	}
	fmt.Fprintf(os.Stderr, "=== after typing 'g': %d models, first %s ===\n", len(m.found), m.found[0].ID)

	// Select one and confirm it stores.
	m.acceptModel()
	fmt.Fprintf(os.Stderr, "=== selected %s, picking=%v ===\n", m.info.Model, m.picking)
	if strings.TrimSpace(m.info.Model) == "" {
		t.Fatal("no model was selected")
	}
}
